# Process-Instance Chaining — Design

**Status:** Approved (brainstorming), ready for planning — 2026-06-24
**Reserved ADRs:** **0045** (process-instance chaining), **0046** (status-accurate terminal outbox events)
**Branch:** `claude/process-instance-chaining-iazvza`

## 1. Problem

A consumer wants to **automatically start a new, independent top-level process
instance when another instance reaches a terminal state** (completed, failed, or
terminated). Example: when an `approval` process completes, start a `fulfillment`
process seeded with the approval's result variables; when a process *fails*, start a
`cleanup`/alerting process.

This is **sequential chaining of independent instances** — the predecessor fully
ends and releases its resources, and the successor is a brand-new root instance that
outlives it. It is explicitly **not** the parent→child *nesting* the async call
activity already provides (where the parent parks and waits for a depth-bounded
child — ADR-0024). Chaining must:

- be **durable** (survive a crash between predecessor-end and successor-start),
- keep the **engine core pure** (no transport/broker leak; ADR-0004/0019),
- avoid **vendor lock-in** (go through the eventing abstraction; never import
  watermill from `runtime`/`engine`),
- support **all terminal outcomes** (completed / failed / terminated),
- give **exactly-once effect** under at-least-once delivery.

## 2. Current behaviour (what the codebase gives us today)

- When `engine.Step` drives an instance terminal it emits a terminal **command**:
  `CompleteInstance` (→ `StatusCompleted`) or `FailInstance` (→ `StatusFailed`
  **or** `StatusTerminated`). The cancel path (`engine/step_triggers.go:165-176`)
  sets `StatusTerminated` **and emits `FailInstance{Err:"cancelled"}`**; the admin
  full-rollback path (`engine/step_compensation.go:321-347`) sets
  `StatusTerminated` with **no terminal command at all**.
- `runtime.outboxEventsFor` (`runtime/outbox.go`) maps those **commands** to outbox
  events: `CompleteInstance → "instance.completed"` (payload = result vars),
  `FailInstance → "instance.failed"` (payload = `{"error": …}`). Events are written
  in the same tx as state (`AppliedStep.Events`), relayed at-least-once to watermill
  via the `eventing` abstraction. On the wire the message **body is the JSON-encoded
  payload** and `instance_id`/`DedupKey` are metadata
  (`internal/eventing/watermill/publisher.go`).
- **Consequences for "all terminal outcomes":** the command-driven mapping is
  **not status-accurate**. A *cancelled* instance (`StatusTerminated`) emits
  `instance.failed`; a *full-rollback* termination emits **nothing**. So a chaining
  consumer cannot reliably distinguish failed from terminated, and misses one
  terminal path entirely.
- Statuses (`engine/state.go:196`): `Running, Completed, Failed, Compensating,
  Terminated`. `runtime.isTerminal` (`runtime/observability.go:65`) = Completed ∨
  Failed ∨ Terminated. `deliverLoop` already computes the terminal **edge**
  (`isTerminal(st.Status) && !isTerminal(prevStatus)`) for metrics and `CallOutcome`
  derivation (`runtime/runner.go:360,380`).
- There is **no** existing "start a successor instance" mechanism. The closest is
  the async call-activity nesting (`StartSubInstance` + `CallLink` + `CallNotifier`),
  which is the wrong semantics here.

## 3. Approach (chosen)

**Event-driven chaining over the durable outbox**, in three layers, with a new
**status-accurate** terminal event so all three outcomes are routable.

Two architectural decisions, each its own ADR:

### 3a. Status-accurate terminal outbox events (ADR-0046)

Move terminal-event derivation from **command-driven** to **status-driven**, computed
at the `deliverLoop` terminal edge (the same place `CallOutcome` is derived), keeping
the engine core untouched:

| `st.Status` at the terminal edge | Topic | Payload |
|---|---|---|
| `StatusCompleted` | `instance.completed` | `st.Variables` (unchanged) |
| `StatusFailed` | `instance.failed` | `{"error": <terminalErr>}` (unchanged shape) |
| `StatusTerminated` | `instance.terminated` (**new**) | `{"error": <terminalErr>}` |

`terminalErr(st)` is the existing helper (`runtime/runner.go:449`: first incident
error, else status-keyed message; cancel → `"cancelled"`).

This **replaces** the command-driven terminal mapping in `outboxEventsFor`
(which only ever handled terminal commands). Each terminal status now emits **exactly
one** status-accurate event. This is a deliberate, documented **behavioural change**:

- A cancelled instance now emits **`instance.terminated`** (was `instance.failed`).
- A full-rollback termination now emits **`instance.terminated`** (was *nothing*).
- `instance.completed` / `instance.failed` (genuine failure) are **unchanged** in
  topic and payload.

**Migration note (ADR-0046 Consequences):** any consumer that relied on
`instance.failed` firing for *cancelled* instances must also subscribe
`instance.terminated`. No in-repo consumer does. Determinism/`Step` purity are
unaffected — derivation reads only `prevStatus`/`st.Status` in `runtime`.

### 3b. Chaining components (ADR-0045)

Broker-agnostic **core** in `runtime`; watermill **adapter** in `eventing`; durable
**lineage** in `runtime` (port + mem) and `persistence` (Postgres). No code at the
module root — the user adds public root **type aliases** for the exported types in a
separate follow-up.

```
                       ┌────────────────────────────────────────────┐
   instance.completed  │ eventing (watermill boundary)              │
   instance.failed   ──┤  NewChainHandler() message.NoPublishHandler│
   instance.terminated │  Chainer{}.Run(ctx, sub)  ── thin wrapper  │
                       └───────────────────┬────────────────────────┘
                                           │ ChainEvent
                                           ▼
                       ┌────────────────────────────────────────────┐
                       │ runtime (broker-agnostic core)             │
                       │  Chainer core: Handle(ctx, ChainEvent)     │
                       │   1. policy(ev) → SuccessorDecision        │
                       │   2. ChainLinkStore.Record (idempotent)    │
                       │   3. InstanceStarter.Run(successor)        │
                       │  SuccessorPolicy, SuccessorDecision        │
                       │  ChainLink, ChainLinkStore, MemChainLink…  │
                       └───────────────────┬────────────────────────┘
                                           │
                       ┌───────────────────▼────────────────────────┐
                       │ persistence: NewChainLinkStore(pool)       │
                       │ migration 0008_chain_links.sql             │
                       └────────────────────────────────────────────┘
```

## 4. Components

### 4.1 `runtime` — value types & policy seam

```go
// Outcome is the terminal outcome that triggered a chaining decision.
type Outcome string

const (
    OutcomeCompleted  Outcome = "completed"
    OutcomeFailed     Outcome = "failed"
    OutcomeTerminated Outcome = "terminated"
)

// ChainEvent is the broker-agnostic input to a chaining decision, projected from a
// terminal outbox event.
type ChainEvent struct {
    PredecessorID  string         // the instance that reached a terminal state
    PredecessorDef string         // "defID:version" (from event metadata, when present)
    Outcome        Outcome        // completed | failed | terminated
    Result         map[string]any // event payload: vars (completed) or {"error":…} (failed/terminated)
}

// SuccessorDecision is what a policy decides to start. A zero Def means "no successor".
type SuccessorDecision struct {
    Def  *model.ProcessDefinition
    Vars map[string]any
}

// SuccessorPolicy decides the successor for a terminal predecessor. ok=false ⇒ chain ends.
// v1 is a Go callback; a declarative (expr-driven) ruleset is a deferred follow-up that
// plugs in here.
type SuccessorPolicy func(ctx context.Context, ev ChainEvent) (SuccessorDecision, bool)
```

### 4.2 `runtime` — durable lineage (`ChainLinkStore`)

Mirrors the `CallLinkStore` pattern (port + `Mem…` here, Postgres in `persistence`).

```go
type ChainLink struct {
    PredecessorID  string
    PredecessorDef string
    Outcome        Outcome
    SuccessorID    string
    SuccessorDef   string
    StartVars      map[string]any
    CreatedAt      time.Time
}

// ErrChainLinkExists signals an already-recorded (PredecessorID, Outcome) hop — the
// exactly-once backstop. The Chainer treats it as "already chained" (skip, ack).
var ErrChainLinkExists = errors.New("workflow-runtime: chain link already exists")

type ChainLinkStore interface {
    // Record durably stores one predecessor→successor hop. It MUST be idempotent on
    // (PredecessorID, Outcome): a duplicate returns ErrChainLinkExists.
    Record(ctx context.Context, link ChainLink) error
    // LookupBySuccessor returns the link that produced successorID (ancestry).
    LookupBySuccessor(ctx context.Context, successorID string) (ChainLink, bool, error)
    // ListByPredecessor returns all hops fanned out from predecessorID (admin/audit).
    ListByPredecessor(ctx context.Context, predecessorID string) ([]ChainLink, error)
}
```

`MemChainLinkStore` is the reference fake (map keyed by `(PredecessorID, Outcome)`,
mutex-guarded), used by unit tests and embedded consumers.

### 4.3 `runtime` — the chaining core

```go
// InstanceStarter is the minimal seam the Chainer core needs to start a successor.
// *Runner satisfies it (Run). Kept narrow so the core is unit-testable without a Runner.
type InstanceStarter interface {
    Run(ctx context.Context, def *model.ProcessDefinition, instanceID string, vars map[string]any) (engine.InstanceState, error)
}

type Chainer struct { /* starter, policy, links (optional), clk, obs */ }

func NewChainer(starter InstanceStarter, policy SuccessorPolicy, opts ...ChainerOption) *Chainer

// Handle applies the policy to one ChainEvent and, if a successor is decided,
// records the lineage link then starts the successor. Idempotent and safe under
// at-least-once delivery. Returns nil on "no successor" and on duplicate.
func (c *Chainer) Handle(ctx context.Context, ev ChainEvent) error
```

`Handle` algorithm:

1. `dec, ok := policy(ctx, ev)`; if `!ok || dec.Def == nil` → return nil (chain ends).
2. Compute the deterministic successor id: **`<PredecessorID>-next-<Outcome>`**.
3. If `links != nil`: `Record(ChainLink{…})`. On `ErrChainLinkExists` → **do NOT
   return; fall through to step 4** (the link is *intent*: a prior delivery may
   have recorded it and then failed to start the successor, so the start must
   still be attempted). Other `Record` errors propagate (retry).
4. `starter.Run(ctx, dec.Def, successorID, dec.Vars)`. If the store reports the
   instance already exists (`ErrInstanceExists`-class), treat as already-started →
   return nil. Other errors propagate (so the message is re-delivered/retried).
5. Emit observability (span + `wrkflw_chain_started_total{outcome}` counter).

**Idempotency.** The *real* exactly-once backstop for the successor is its
**existence**: the deterministic successor id + `Store.Create`'s `ErrInstanceExists`
guarantee at-most-one successor instance no matter how many times the terminal
event is delivered. The `ChainLinkStore` unique `(PredecessorID, Outcome)` is
durable *lineage* (and an early-dedup signal), **not** a start-suppressing gate —
recording it before the start is intent, so an `ErrChainLinkExists` must never
short-circuit the start (else a transient start failure after the link is written
would drop the successor permanently). This ordering was corrected after the
whole-branch review caught the lost-successor window.

> **New runtime port needed:** `Store.Create` must surface a typed
> "instance already exists" error so the Chainer can distinguish duplicate-start from
> a real failure. Add `runtime.ErrInstanceExists` and have `MemStore`/Postgres
> `Create` return it on primary-key conflict (today MemStore overwrites / Postgres
> errors untyped). Small, additive, with its own RED test.

### 4.4 `eventing` — watermill adapter (the only watermill import)

`eventing` is the sanctioned watermill boundary (it already wraps the publisher).
The subscriber-side chaining helper lives here so `runtime` stays watermill-free.

```go
// NewChainHandler adapts the broker-agnostic core to a watermill handler. The
// consumer mounts it on their own message.Router (their retry/poison/DLQ middleware
// wraps it). The handler maps: topic → Outcome, metadata["instance_id"] →
// PredecessorID, JSON body → Result.
func NewChainHandler(core *runtime.Chainer) message.NoPublishHandlerFunc

// Chainer is the turnkey convenience wrapper for consumers who don't run a router.
// Run subscribes the three terminal topics and drives core.Handle until ctx is done
// (mirrors runtime.CallNotifier.Run / eventing.NewGoChannelPublisher).
type Chainer struct { /* core, log, topics */ }
func NewChainerRunner(core *runtime.Chainer, opts ...Option) *Chainer
func (c *Chainer) Run(ctx context.Context, sub message.Subscriber) error
```

Topic→Outcome map: `instance.completed→OutcomeCompleted`,
`instance.failed→OutcomeFailed`, `instance.terminated→OutcomeTerminated`.
Ack on success and on `nil` (no successor / duplicate); Nack on a propagated error
so the broker re-delivers; malformed payload → log + Ack (poison, don't loop) or
route to the consumer's DLQ middleware.

### 4.5 `persistence` — Postgres `ChainLinkStore`

- Migration **`0008_chain_links.sql`**: table `wrkflw_chain_links` with
  `predecessor_instance_id text`, `outcome text`, `successor_instance_id text`,
  `predecessor_def text`, `successor_def text`, `start_vars jsonb`,
  `created_at timestamptz`, **`PRIMARY KEY (predecessor_instance_id, outcome)`**
  (the dedup backstop), plus an index on `successor_instance_id` for ancestry lookup.
- `postgres.ChainLinkStore` implementing the port; `Record` maps a unique-violation
  (SQLSTATE 23505) to `runtime.ErrChainLinkExists`.
- `persistence.NewChainLinkStore(pool) runtime.ChainLinkStore` façade + compile-time
  assertion (mirrors `NewCallNotifier`/`NewTimerStore`).

### 4.6 Root type aliases (out of scope here — user-owned follow-up)

Per the no-root-code constraint, the implementation adds **no** `.go` files at the
module root. The user will later add root aliases for the public types (e.g.
`type Chainer = runtime.Chainer`, `type SuccessorPolicy = runtime.SuccessorPolicy`,
`type ChainLink = runtime.ChainLink`, …). The plan lists the intended alias set as a
final, separate, user-confirmed task — it is **not** implemented in this track.

## 5. Data flow (happy path: complete → successor)

1. Predecessor instance reaches `StatusCompleted`; `deliverLoop` derives the
   status-driven `instance.completed` event (payload = vars), committed in-tx.
2. Relay publishes it; the consumer's subscriber delivers it to the chain handler.
3. Handler projects → `ChainEvent{PredecessorID, OutcomeCompleted, Result:vars}`.
4. `core.Handle`: policy returns `fulfillmentDef` + mapped vars → `Record` link
   (`<pred>-next-completed`) → `starter.Run(fulfillmentDef, "<pred>-next-completed", vars)`.
5. Successor starts as a fresh root instance; the predecessor stays terminal.
6. Redelivery of the same event ⇒ `Record` returns `ErrChainLinkExists` ⇒ no-op ack.

## 6. Error handling

- **No successor** (`ok=false`): clean ack, chain ends.
- **Duplicate delivery**: `ErrChainLinkExists` / `ErrInstanceExists` ⇒ ack, no-op.
- **Transient start failure** (store/db): propagate ⇒ Nack ⇒ re-delivered.
- **Malformed event payload**: log + ack (or consumer DLQ); never infinite-loop.
- **Policy panic**: the watermill adapter recovers (consumer `Recoverer` middleware)
  / the core does not swallow — documented; policies should be total.
- Chaining is **best-effort relative to the predecessor**: a failing successor start
  never affects the already-terminal predecessor (decoupled by the outbox).

## 7. Testing (strict TDD; visible RED→GREEN per symbol)

- **runtime (unit):** status-driven terminal events for all three outcomes incl.
  cancel→`instance.terminated` and full-rollback→`instance.terminated` (table test
  over `(prevStatus, status) → topic/payload`); `MemChainLinkStore` Record/idempotency/
  Lookup/List; `Chainer.Handle` — no-successor, happy-path start, duplicate link skip,
  duplicate-instance skip, transient-error propagation (mock `InstanceStarter` +
  `ChainLinkStore` via mockgen); deterministic id scheme; `ErrInstanceExists` from
  `MemStore.Create`.
- **eventing (unit + integration):** topic→outcome projection; metadata/body mapping;
  ack/nack discipline; end-to-end `instance.completed → successor` over
  `NewGoChannelPublisher` + a real `Runner` + `MemStore` + `MemChainLinkStore`.
- **persistence (integration, testcontainers):** Postgres `ChainLinkStore` Record +
  23505→`ErrChainLinkExists` + Lookup/List via `database.RunTestDatabase`.
- **Example:** a testable `Example` showing complete→successor wiring (root-package
  ergonomics once aliases exist; until then, `runtime`/`eventing`).
- **Gate:** `go test -race ./...` green; ≥85% line coverage on touched packages;
  `golangci-lint run ./...` clean; engine/model import-pure (no transport/vendor);
  **engine/model production diff ZERO** (this track is runtime+eventing+persistence
  only — no engine change).

## 8. Scope / YAGNI

**In:** status-accurate terminal events (incl. new `instance.terminated`); the
callback `SuccessorPolicy`; `ChainLink` + `ChainLinkStore` (mem + Postgres);
`Chainer` core; watermill handler + `Chainer.Run` wrapper; `ErrInstanceExists`.

**Out (deferred follow-ups):** declarative/expr-driven successor ruleset (the policy
seam is built for it); consumer-supplied successor-id scheme; loading full
predecessor variables for failed/terminated chaining (v1 carries only the event
payload); multi-successor fan-out beyond one-per-`(predecessor,outcome)`;
admin REST/gRPC surface for chain ancestry queries; root type aliases (user-owned).

## 9. Reserved ADRs

- **ADR-0045 — Process-instance chaining.** Event-driven successor start over the
  durable outbox; callback policy v1; durable `ChainLinkStore`; runtime core +
  eventing adapter split; deterministic-id + unique-link idempotency.
- **ADR-0046 — Status-accurate terminal outbox events.** Replace command-driven
  terminal mapping with status-driven derivation; add `instance.terminated`; fix
  cancel→failed conflation and full-rollback no-event gap; migration note.
