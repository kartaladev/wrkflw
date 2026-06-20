# 7. Per-step atomic Store with optimistic concurrency (transactional outbox)

- Status: Accepted
- Date: 2026-06-21
- Amends: ADR-0005 (the `store, jnl, out` persistence parameters of `NewRunner`).

## Context

The engine-core design (`docs/specs/2026-06-20-engine-core-design.md` §6) requires
domain events to be written to the outbox **in the same transaction as state**.
But the reference runtime scatters the three persistence writes across one
`deliverLoop` iteration: `jnl.Append` before `Step`, `store.Save` after `Step`,
and `out.Write` inside `perform` (only for `CompleteInstance`/`FailInstance`,
*after* the save). They are three independent calls on three independent ports —
not atomic. A crash between them, or a transactional-outbox relay, would observe
state without its events or vice versa.

Two constraints shape any fix:

1. **`perform()` does external I/O** — `InvokeAction` calls a consumer service,
   `ThrowSignal` fans out, `ScheduleTimer` registers a waiter. A DB transaction
   must **never** be held open across that I/O. So the atomic unit is one `Step`,
   not the whole cascade-draining loop.
2. **The engine is pure and must stay version-agnostic** — a concurrency token in
   `InstanceState` would pollute determinism and `cloneState`. Concurrency
   control belongs in the persistence layer.

`engine.EmitEvent` was specced as a command but never implemented; outbox topics
are currently synthesised by the runtime from terminal commands.

## Decision

**Collapse `StateStore` + `Journal` + `OutboxWriter` into one transactional
`Store` port, and persist exactly one applied trigger per short transaction with
optimistic-version CAS.**

```go
type Token int64 // opaque optimistic-concurrency token (Postgres: bigint version)

type AppliedStep struct {
    State   engine.InstanceState
    Trigger engine.Trigger
    Events  []OutboxEvent
}

type Store interface {
    Create(ctx, step AppliedStep) (Token, error)
    Load(ctx, id string) (engine.InstanceState, Token, error)
    Commit(ctx, expected Token, step AppliedStep) (Token, error) // ErrConcurrentUpdate if stale
}
```

`Commit` performs, in one DB transaction: `UPDATE instances SET snapshot,
version=version+1, <projected cols> WHERE instance_id AND version=expected`
(0 rows ⇒ `ErrConcurrentUpdate`), `INSERT` the journal row, and `INSERT` the
outbox rows.

The `deliverLoop` is restructured to: `Step` first (pure) → derive outbox events
from `res.Commands` via a **pure runtime helper `outboxEventsFor`** → `Commit`
(one short tx) → then `perform` the non-outbox commands (external I/O, outside any
tx). `perform` no longer writes the outbox. The in-memory `(st, token)` are held
across the loop; only the CAS guards a racing writer, so there is no per-step
re-`SELECT`. On `ErrConcurrentUpdate` (or SQLSTATE `40001`) the whole `Deliver`
aborts and the caller reloads and retries with bounded backoff + jitter; the
journal `(instance_id, seq)` PK and outbox `dedup_key` make a redelivered step
idempotent.

`NewRunner`'s three persistence positionals become **one** `Store` positional
(this amends ADR-0005's persistence parameters; the human-task/scheduler options
are unchanged). The in-memory fakes become a single `MemStore` satisfying `Store`
(+ `JournalReader`), whose `Commit` buffers and applies on success so a failed
step never half-applies.

We deliberately do **not** add `EmitEvent` to the engine's sealed command set;
outbox topic derivation stays a runtime concern (`outboxEventsFor`), keeping the
pure core untouched.

## Consequences

**Easier:** state + journal + outbox are atomic per applied trigger (true
transactional outbox); concurrency is handled with zero happy-path cost and
surfaces cleanly as a typed error; the engine core stays pure and untouched; one
`Store` port is simpler to implement and reason about than three coupled ports;
adding the Postgres impl is a single seam.

**Harder / trade-offs:** a breaking change to `NewRunner` and to every test/site
that wired `MemStateStore`/`MemJournal`/`MemOutbox` separately (mechanical
migration; the module is not publicly released). `perform` loses its outbox
writes, so `outboxEventsFor` must enumerate every outbox-producing command — an
exhaustiveness test guards this. Triggers must be (de)serialisable for the
journal (a sealed-set codec, its own plan task). Retry-on-conflict shifts
idempotency responsibility onto the journal PK + outbox dedup key.
