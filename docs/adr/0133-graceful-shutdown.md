# 133. Graceful shutdown for `runtime.ProcessDriver`

- Status: Accepted
- Date: 2026-07-14

## Context

An audit of the runtime shutdown path found that `ProcessDriver.Shutdown` released
**only** the one resource the driver owns — the in-process default scheduler's gocron
goroutine — and relied on the consumer to drain their own `Run(ctx)` workers (outbox
relay, call notifier, chainer) by cancelling their context. That worker boundary is the
intended library contract (ADR-0054). But for the driver's **own** execution paths, the
behaviour did not satisfy two properties a graceful shutdown requires:

- **No admission control.** `Drive`, `ApplyTrigger`, `DeliverMessage`, `BroadcastSignal`,
  `CancelInstance`, `ResolveIncident`, `ReverseInstance`, and `createAtNode` had no
  "draining/closed" guard. A command arriving during or after `Shutdown` still ran a full
  `deliverLoop`, mutating instance state and emitting outbox events; a **timer-start**
  firing during shutdown even spun up a brand-new instance.
- **No in-flight drain.** `ProcessDriver` held no counter that could be waited on (the
  `instActive` OTel gauge is metrics-only). `Shutdown` returned as soon as the scheduler
  closed, without waiting for consumer-initiated `Drive`/`ApplyTrigger` calls still running
  on their own goroutines.

Two related bugs surfaced in the same audit:

- **Finding 3** — the `ctx` deadline was ignored by the one closer `Shutdown` had. The
  scheduler was registered via `ShutdownGroup.AddCloser(sched)`, wrapping `sched.Close()`,
  which takes no context and blocks on gocron's own internal stop timeout — fully ignoring
  the caller's `ctx` deadline, contradicting the documented "bounded drain".
- **Finding 4** — a timer-arm-after-scheduler-close was logged at WARN and skipped in a way
  that read as a *lost* timer, though it in fact survives in the durable timer store and
  rehydrates on next boot.

The engine core must stay pure — no shutdown context threaded through `engine.Step`.

The design is recorded in full at `docs/specs/2026-07-14-graceful-shutdown-design.md`.

## Decision

Add admission control and in-flight drain to `ProcessDriver`, keyed off two new fields —
`draining atomic.Bool` and `inflight sync.WaitGroup` — plus an opt-in `shutdownTimeout`.

**D1 — Strict quiescence.** Once `Shutdown` begins, *all* new external entry points reject
with the typed sentinel `ErrDriverShuttingDown`, including a message/signal delivered to an
already-parked instance and a timer-start fire. Each gated entry point takes an `admit()`
slot at entry (returning `nil, false` when draining) and `defer release()`s it. A timer-start
fire is *new* work, so it admits and drops when draining (benign: the durable arm rehydrates
on next boot). Gate prologues sit **above** any trace span or empty-name no-op, so a drained
driver rejects even a would-be no-op.

`ApplyTrigger`'s body is extracted into an ungated internal `applyTrigger` (load →
`deliverLoop` → save); the public `ApplyTrigger` wraps it with the `admit()` gate. Every
in-driver continuation that is already inside a gated method (cancel cascade, message
correlate, incident resolve, reverse, and the timer-fire continuation) calls the ungated
`applyTrigger` so there is no nested re-admit or spurious mid-cascade rejection. A timer-fire
continuation advances an already-running instance, so it reserves an inflight slot via
`reserveInternal()` (counted by the drain wait, but never rejected). The one intentional
nesting — the consumer-wired SignalBus `DeliverFunc` → public `ApplyTrigger` — is harmless
for the WaitGroup and consistent with strict quiescence.

**D2 — Wait, don't force-cancel, on timeout.** On drain-deadline expiry `Shutdown` returns a
wrapped `ErrDrainTimeout`; in-flight `deliverLoop`s keep running to completion on their own
goroutines against the live store. This avoids threading a shutdown context through the
engine core and defining partial-commit-on-cancel semantics.

**D3 — `WithShutdownTimeout` is an opt-in fallback, not a built-in default.** Precedence: a
`ctx` deadline always wins; if `ctx` has no deadline and the option is set, `Shutdown`
derives `now + d`; if neither, the drain is unbounded (respect `ctx` as-is). No implicit
non-zero default, so an intentional long-lived-context caller is never silently truncated.

**Shutdown sequence (ordering is load-bearing).** `Shutdown` (1) sets `draining`, (2) applies
the D3 fallback, (3) closes the owned scheduler — via a **deadline-raced closer** that runs
`sched.Close()` in a goroutine and `select`s it against `ctx.Done()` (Finding 3 fix), so ctx
bounds the scheduler drain and gocron also waits for in-flight timer fires — then (4) waits
on `inflight` bounded by ctx. Step 3 precedes step 4 so the only post-draining source of a
new `inflight.Add` (an in-flight timer continuation) is fully drained before `WaitGroup.Wait`
runs, ruling out an `Add`-after-`Wait` panic. Errors from the two steps are `errors.Join`ed.
`Shutdown` remains idempotent.

**Finding 4** — `armTimer`/`armStartTimer` keep their WARN-and-skip but the message now
states the durable arm/definition rehydrates on next boot and carries a
`driver_shutting_down` attribute, so a skip during drain is not mistaken for a lost timer.
No behavioural change.

**`service.Engine` propagation.** Rejection is inherited because every Engine entry point
funnels through the driver's gated methods. The one nuance: `ClaimTask`/`CompleteTask`/
`ReassignTask` write to the task store *before* reaching the driver, so they gain an early
`driver.IsShuttingDown()` check that rejects with `ErrDriverShuttingDown` before any
task-store side effect.

## Consequences

- `Shutdown` now genuinely quiesces the driver's own execution paths and honours `ctx`
  across the whole drain; the documented "bounded drain" contract is true.
- New public surface: `ErrDriverShuttingDown`, `ErrDrainTimeout`, `WithShutdownTimeout(d)`,
  and `ProcessDriver.IsShuttingDown()`. Consumers can distinguish "refused, retry elsewhere"
  from "drained cleanly" from "drain timed out".
- On timeout, in-flight work is **not** cancelled — it keeps running against the live store.
  Callers who need a hard stop must cancel their own operation contexts; the library does not
  force-abort a `deliverLoop` mid-flight (D2).
- The deadline-raced closer means that if `ctx` wins, `sched.Close()` keeps running briefly in
  its goroutine and finishes shortly after (bounded by gocron's stop timeout) — not leaked
  indefinitely. An empty owned scheduler closes fast, so the ctx-bounding benefit is only
  observable under contention; the behaviour is a contract guarantee, not a hot path.
- The engine core is untouched: no shutdown context threads through `engine.Step`, preserving
  deterministic replay.
- A follow-up (out of scope) may expose `IsShuttingDown()` on `service.Engine` and map
  `ErrDriverShuttingDown` to HTTP `503` in the transport layer.
