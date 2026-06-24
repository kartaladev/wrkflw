# 45. Process-instance chaining over the durable outbox

- Status: Accepted
- Date: 2026-06-24

## Context

A consumer wants to **automatically start a new, independent top-level process
instance when another reaches a terminal state** (completed, failed, or
terminated): e.g. when an `approval` process completes, start a `fulfillment`
process seeded with the approval result; when a process *fails*, start a
`cleanup`/alerting process.

This is **sequential chaining of independent instances** — the predecessor fully
ends and releases its resources, and the successor is a brand-new root instance
that outlives it. It is explicitly **not** the parent→child *nesting* the async
call activity provides (where the parent parks and waits for a depth-bounded
child — ADR-0024). Chaining must be durable (survive a crash between
predecessor-end and successor-start), keep the engine core pure (no
transport/broker leak — ADR-0004/0019), avoid vendor lock-in (go through the
eventing abstraction; never import watermill from `runtime`/`engine`), support
all terminal outcomes, and give **exactly-once effect** under at-least-once
delivery.

The natural integration seam already exists: terminal outbox events
(`instance.completed` / `instance.failed`) are written transactionally with
state and relayed at-least-once via the `eventing` abstraction. Chaining can be a
**subscriber** of those events. (Their *status accuracy* is fixed separately in
ADR-0046, a prerequisite for routing all three outcomes.)

We considered modelling chaining as another call-activity nesting, but the
semantics are wrong: the predecessor must end and release resources, not park and
wait, and the successor must outlive it as an independent root. Event-driven
chaining over the durable outbox matches the semantics and reuses the existing
crash-safe relay.

## Decision

Chaining is **event-driven over the durable outbox**, in three layers, with the
engine core untouched:

- **Broker-agnostic core in `runtime`.** A `Chainer` whose `Handle(ctx,
  ChainEvent)` (1) applies a consumer-supplied `SuccessorPolicy` (a Go callback —
  `func(ctx, ChainEvent) (SuccessorDecision, bool)`), (2) records the lineage
  link, then (3) starts the successor via a narrow `InstanceStarter` seam
  (`*runtime.Runner` satisfies it). `ChainEvent`, `SuccessorDecision`,
  `SuccessorPolicy`, `Outcome` (+ constants) are value types here.
- **Callback policy in v1; declarative/expr ruleset deferred.** The `SuccessorPolicy`
  callback is the seam a future declarative ruleset plugs into; YAGNI for v1.
- **Durable lineage (`ChainLinkStore`).** A `ChainLink`
  (predecessor→successor, outcome, start vars, timestamp) persisted through a
  `ChainLinkStore` port — `MemChainLinkStore` in `runtime`, Postgres in
  `persistence` (migration `0008_chain_links.sql`). It enables admin
  chain-ancestry queries (`LookupBySuccessor`, `ListByPredecessor`) **and** a
  DB-level exactly-once backstop: a unique `(predecessor, outcome)` rejects a
  duplicate hop with `ErrChainLinkExists`.
- **Watermill adapter in `eventing` only.** `NewChainHandler(core)` adapts the
  core to a `message.NoPublishHandlerFunc` a consumer mounts on their own router;
  `NewChainerRunner(core).Run(ctx, sub)` is a turnkey wrapper subscribing the
  three terminal topics. All watermill imports stay in this package
  (`runtime`/`engine` remain watermill-free).
- **Idempotency ⇒ exactly-once effect.** The exactly-once backstop is the
  successor's *existence*: a deterministic successor id
  `<PredecessorID>-next-<Outcome>` + `Store.Create` duplicate rejection
  (`ErrInstanceExists`, added here and mapped in Postgres from SQLSTATE 23505)
  make a redelivered terminal event a clean no-op ack. The unique
  `(predecessor, outcome)` chain link is durable *lineage*, not a start-suppressing
  gate: `Handle` records the link as intent and, on `ErrChainLinkExists`, still
  (re)attempts the start. (An earlier design returned on `ErrChainLinkExists`,
  which the whole-branch review showed would permanently drop a successor whose
  start failed transiently after its link was written — fixed.)
- **No code at the module root.** No `.go` files are added at the repo root; the
  public root **type aliases** for the exported chaining types are a separate,
  user-owned follow-up.

## Consequences

- A consumer embeds chaining by supplying a `SuccessorPolicy`, wiring a
  `ChainLinkStore` (mem or Postgres), and mounting `NewChainHandler` (or running
  `NewChainerRunner`) against their broker subscriber. Chaining is **best-effort
  relative to the predecessor**: a failing successor start never affects the
  already-terminal predecessor (decoupled by the outbox).
- Error discipline: no successor (`ok=false`) → ack; duplicate
  (`ErrChainLinkExists`/`ErrInstanceExists`) → ack no-op; transient store/db
  failure → propagate → Nack → re-delivery; malformed payload → log + ack (never
  infinite-loop / route to the consumer's DLQ middleware).
- Engine/model production diff is **ZERO** — this track is `runtime` + `eventing`
  + `persistence` only. `Step` purity/determinism (ADR-0002) is untouched: the
  core reads only the projected `ChainEvent`.
- `Store.Create` gains a typed `ErrInstanceExists` (additive; `MemStore` no
  longer silently overwrites a duplicate id, Postgres maps 23505). This is a
  small behavioural sharpening of `Create` that existing single-start call sites
  never hit.
- Deferred follow-ups: declarative/expr successor ruleset; consumer-supplied
  successor-id scheme; loading full predecessor variables for failed/terminated
  chaining (v1 carries only the event payload); multi-successor fan-out beyond
  one-per-`(predecessor, outcome)`; an admin REST/gRPC surface for chain-ancestry
  queries; the root type aliases.
