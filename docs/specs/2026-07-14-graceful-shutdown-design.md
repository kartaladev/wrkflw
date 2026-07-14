# Graceful Shutdown for `runtime.ProcessDriver` — Design

- **Date:** 2026-07-14
- **Status:** Approved (pending implementation)
- **Owner:** runtime
- **ADR:** to be recorded during implementation (next number: 0133)

## 1. Context & Problem

An audit of the runtime shutdown path found that `ProcessDriver.Shutdown` releases
**only** the one resource the driver owns — the in-process default `scheduling.Scheduler`'s
gocron goroutine — and relies on the consumer to drain their own `Run(ctx)` workers
(outbox relay, call notifier, chainer) by cancelling their context. This is the
intended library boundary (ADR-0054): the library does not own worker-goroutine
startup.

However, for the driver's **own** execution paths the current behaviour does **not**
satisfy two properties a graceful shutdown requires:

- **No admission control.** `Drive`, `createAtNode`, `ApplyTrigger`, `DeliverMessage`,
  and `BroadcastSignal` have no "draining/closed" guard. A command that arrives during
  or after `Shutdown` still runs a full `deliverLoop`, mutating instance state and
  emitting outbox events. A **timer-start** firing during shutdown
  (`startTimerFireFunc` → `createAtNode`) even spins up a brand-new instance.
- **No in-flight drain.** `ProcessDriver` holds no `sync.WaitGroup` or active-instance
  counter (the `instActive` OTel gauge is metrics-only and cannot be waited on).
  `Shutdown` returns as soon as the scheduler closes, without waiting for consumer-initiated
  `Drive`/`ApplyTrigger` calls still executing on their own goroutines.

Two related bugs surfaced in the same audit:

- **Finding 3 — the `ctx` deadline is ignored by the one closer it has.** `ProcessDriver.Shutdown`
  documents that it "honours ctx for a bounded drain," but the scheduler is registered via
  `ShutdownGroup.AddCloser(sched)`, which wraps `sched.Close()` — and `Close()` takes no
  context. It calls gocron `Shutdown()`, which uses gocron's own internal stop timeout
  (default ~10s; `WithStopTimeout` is never configured), fully ignoring the caller's `ctx`
  deadline.
- **Finding 4 — a timer-arm-after-scheduler-close is silently dropped.** `armTimer` logs
  `ErrSchedulerClosed` at WARN and skips, so a timer armed during teardown is lost from
  memory (it survives in the durable timer store and rehydrates on next boot, but the WARN
  reads as a lost timer).

The only path that drains correctly today is the timer-fire callback, and only
incidentally: it runs inside gocron's job goroutine, and gocron `Shutdown()` blocks
until running jobs complete.

## 2. Goals & Non-Goals

### Goals

- Once shutdown begins, **reject all new externally-initiated work** with a typed sentinel.
- Make `Shutdown` **wait for in-flight instance execution to complete** before returning.
- **Honour the `ctx` deadline** across the whole drain (scheduler close + in-flight wait).
- Provide a **`WithShutdownTimeout` fallback** so a consumer who calls `Shutdown` with a
  deadline-less context is not exposed to an unbounded hang.
- Keep the **engine core pure** — no shutdown context threaded through `engine.Step`.

### Non-Goals

- **No force-cancellation** of in-flight work on timeout. When the drain deadline expires,
  in-flight `deliverLoop`s keep running to completion on their own goroutines against the
  live store; `Shutdown` returns a timeout error and the caller decides what to do.
- **No change to worker-goroutine ownership.** The relay/notifier/chainer stay
  consumer-owned and `Run(ctx)`-driven (ADR-0054). This work covers only the driver's own
  scheduler and execution paths.
- **No hard-coded default shutdown timeout.** Unset means "respect `ctx` as-is"
  (see § 6, decision D3).

## 3. Design Decisions (resolved forks)

- **D1 — Strict quiescence.** During drain, *all* new external entry points reject,
  including a message/signal delivered to an already-parked instance. In-flight
  `deliverLoop`s and in-flight timer fires still complete. Chosen over permissive
  delivery-to-parked because it matches the requirement literally and keeps the gate simple.
- **D2 — Wait, don't force-cancel, on timeout.** On deadline expiry `Shutdown` returns a
  wrapped `ErrDrainTimeout`; in-flight work finishes naturally. Avoids threading a shutdown
  context through the engine core and defining partial-commit-on-cancel semantics.
- **D3 — `WithShutdownTimeout` is an opt-in fallback, not a built-in default.** Precedence:
  a `ctx` deadline always wins; if `ctx` has no deadline and the option is set, `Shutdown`
  derives `now + d`; if neither, the drain is unbounded (respect `ctx` as-is). No implicit
  non-zero default, so an intentional long-lived-context caller is never silently truncated.

## 4. Core Mechanism

Three additions to `ProcessDriver`:

- `draining atomic.Bool` — set true at the start of `Shutdown`; gates new external work.
- `inflight sync.WaitGroup` — counts admitted, currently-executing units of work.
- The existing `shutdown ShutdownGroup` is retained for the scheduler close, but the
  scheduler is re-registered so its close honours `ctx` (§ 7, Finding 3 fix).

The admission contract is a single helper:

```go
// admit reserves an in-flight slot for a new externally-initiated unit of work.
// It returns a release func and true when work may proceed; it returns nil, false
// once Shutdown has begun draining, so the caller rejects with ErrDriverShuttingDown.
func (driver *ProcessDriver) admit() (release func(), ok bool) {
	if driver.draining.Load() {
		return nil, false
	}
	driver.inflight.Add(1)
	return driver.inflight.Done, true
}
```

Continuations that must complete even while draining (timer fires — see § 5) take **no**
WaitGroup slot. An in-flight owned-scheduler timer fire is drained by the scheduler `Close`
(§ 6 step 2), which blocks until gocron joins its running fire jobs — so `Shutdown` still
waits for a mid-flight fire to finish, and because those fires never touch `inflight`, no
timer-fire `Add` can race `waitInflight`'s `Wait` (this removes the timeout-path
Add-vs-Wait window that a WaitGroup reservation would otherwise create). An earlier draft
used a `reserveInternal()` WaitGroup slot for this; it was removed as redundant with the
scheduler-close join and unsafe on the deadline-timeout path.

## 5. Admission Gate

New sentinel (per the `workflow-<pkg>:` error-prefix convention):

```go
var ErrDriverShuttingDown = errors.New("workflow-runtime: driver is shutting down")
var ErrDrainTimeout       = errors.New("workflow-runtime: shutdown drain timed out")
```

**Gated — external, new work → reject when draining.** The gate lives on the *true external
entry points* — the exported `ProcessDriver` methods a consumer or `service.Engine` calls —
each grabbing `admit()` at entry, returning `ErrDriverShuttingDown` when `!ok`, and
`defer release()`:

- `Drive` — new manual-start instance.
- `ApplyTrigger` — external raw trigger (human claim/complete/reassign funnel here via
  `service.Engine.deliverTaskTrigger`; signal-bus resume also lands here).
- `DeliverMessage`, `BroadcastSignal`.
- `CancelInstance`, `ResolveIncident`, `ReverseInstance` — these also mutate instance state
  via a trigger and are external commands, so strict quiescence (D1) rejects them during drain.
- `startTimerFireFunc` — scheduler-triggered **new-instance** timer-start; admits directly
  and, when draining, drops the fire (benign: logged, arm survives durably).

`createAtNode` is **not** itself gated — it is an unexported worker shared by `DeliverMessage`,
`BroadcastSignal`, and `startTimerFireFunc`. Gating happens at those callers, so a
double-admit is avoided.

To let a continuation complete while the public entry point is gated, `ApplyTrigger`'s body
is extracted into an **internal, ungated** `applyTrigger(ctx, def, id, trg)` (load →
`deliverLoop` → save). The public `ApplyTrigger` wraps it with the `admit()` gate. **Every
in-driver caller that is itself already inside a gated method switches from the public
`ApplyTrigger` to the ungated `applyTrigger`** so there is no nested re-admit or spurious
mid-cascade rejection:

- `CancelInstance` top-level trigger (`processdriver_cancel.go:20`) and its child cascade
  `propagateCancel` (`:77`).
- `DeliverMessage` correlate path (`processdriver_message.go:63`).
- `ResolveIncident` (`processdriver_incident.go:18`).
- `ReverseInstance` (`processdriver_reverse.go:98`, `:105`).

The one unavoidable nesting: the SignalBus `DeliverFunc` is consumer-wired to the *public*
`ApplyTrigger` (`signalbus.go:16-36`), so `BroadcastSignal` (gated) → `sigbus.Publish` →
public `ApplyTrigger` (gated) double-admits. This is harmless for the WaitGroup (count rises
then falls); its only visible effect is that a signal broadcast admitted just before draining
may have an inner resume rejected mid-fan-out during the shutdown race — the joined error
surfaces to the caller, consistent with strict quiescence.

```go
// public: external human triggers — gated.
func (driver *ProcessDriver) ApplyTrigger(...) (engine.InstanceState, error) {
	release, ok := driver.admit()
	if !ok {
		return engine.InstanceState{}, ErrDriverShuttingDown
	}
	defer release()
	return driver.applyTrigger(...)
}
```

**Not gated — in-flight continuation → must complete:**

- Follow-up triggers inside a single `deliverLoop` queue, the synchronous `runChild`
  recursion, and `perform` — all part of an already-admitted call, holding the same slot;
  no second reservation.
- **`timerFireFunc`** advances an *already-running* instance (delivers `TimerFired` via the
  internal `applyTrigger`). It is a continuation, so it is never rejected mid-fire. It takes
  **no** WaitGroup slot: an in-flight owned-scheduler fire is drained by the scheduler `Close`
  (§ 6 step 2, gocron joins running jobs), so `Shutdown` still waits for it to finish, and no
  timer-fire `Add` can race `waitInflight`'s `Wait` (closes the F2 timeout-path window).

**Gated — timer-start is new work:** `startTimerFireFunc` creates a *new* instance and
routes through the **gated** `createAtNode`. So a timer-start that fires during drain is
correctly rejected (`ErrDriverShuttingDown`) rather than starting a new instance — exactly
the strict-quiescence intent (D1). A timer-start that has not begun firing when the scheduler
closes is simply not fired and rehydrates on next boot from the durable timer store. This
rejection is benign: the fire callback already tolerates a dropped fire (its error is logged,
and the arm survives durably).

## 6. Shutdown Sequence (ordering is load-bearing)

```go
func (driver *ProcessDriver) Shutdown(ctx context.Context) error {
	driver.draining.Store(true)                     // 1. stop admitting new external work

	ctx, cancel := driver.effectiveShutdownCtx(ctx) // apply WithShutdownTimeout fallback
	defer cancel()                                  //    iff ctx carries no deadline (D3)

	schedErr := driver.shutdown.Shutdown(ctx)       // 2. close scheduler: gocron stops
	                                                //    dispatch AND waits for in-flight
	                                                //    timer fires (bounded by ctx)

	drainErr := driver.waitInflight(ctx)            // 3. wait for consumer-initiated
	                                                //    deliverLoops still running (bounded by ctx)
	return errors.Join(schedErr, drainErr)
}
```

**Ordering invariant (why step 2 precedes step 3):** after `draining` is set (under
`gateMu`), no path Adds to `inflight`: every gated entry point — including a timer-start's
`createAtNode` — rejects via `admit` without adding, and timer-fire continuations take no
slot at all. In-flight `timerFireFunc` continuations still finish during step 2, because
gocron `Shutdown()` joins running jobs and refuses to start pending ones — so `Shutdown`
waits for a mid-flight fire regardless of the WaitGroup. By the time step 3's `Wait` runs,
no `Add` can occur, ruling out an `Add`-vs-`Wait` panic. `admit`'s check-and-Add is
serialized with the `draining` set via `gateMu`, so no *external* `Add` can race the `Wait`
either. This ordering is a documented, test-locked invariant.

`waitInflight(ctx)` waits on `inflight` in a goroutine and `select`s against `ctx.Done()`:

```go
func (driver *ProcessDriver) waitInflight(ctx context.Context) error {
	done := make(chan struct{})
	go func() { driver.inflight.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", ErrDrainTimeout, ctx.Err())
	}
}
```

`effectiveShutdownCtx` applies the D3 precedence:

```go
func (driver *ProcessDriver) effectiveShutdownCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {} // caller was explicit; honour it
	}
	if driver.shutdownTimeout > 0 {
		return context.WithTimeout(ctx, driver.shutdownTimeout)
	}
	return ctx, func() {} // unbounded (respect ctx as-is)
}
```

`Shutdown` remains idempotent: `draining` is already set and `ShutdownGroup.done` guards a
second scheduler close; a second call sees an already-drained WaitGroup and returns nil
(joined errors are nil).

## 7. `WithShutdownTimeout` Option

```go
// WithShutdownTimeout sets a FALLBACK drain deadline applied by Shutdown only when the
// ctx passed to Shutdown carries no deadline of its own. A ctx deadline always wins.
// Zero or unset means no fallback — Shutdown respects ctx as-is (unbounded when ctx has
// no deadline). A non-positive d is ignored (treated as unset), consistent with
// WithActionTimeout.
func WithShutdownTimeout(d time.Duration) Option
```

Stored as `shutdownTimeout time.Duration` on `ProcessDriver`. No `clock.Clock` dependency:
the bound comes from `context.WithTimeout` (wall clock), and shutdown is not part of the
deterministic-replay surface.

## 8. Bugs Fixed in the Same Change

1. **Finding 3 — honour `ctx` in the scheduler close.** Re-register the owned scheduler
   with `shutdown.Add(func(ctx) error { ... })` that races `sched.Close()` (run in a
   goroutine) against `ctx.Done()`, returning `ctx.Err()` if the deadline wins. This makes
   the documented "bounded drain" contract true. (`gocron.WithStopTimeout` may also be
   plumbed from the deadline as defence-in-depth, but the race is the reliable bound.)
2. **Finding 4 — clarify the timer-arm-after-close WARN.** With the gate in place, a
   timer-arm-after-close can only occur for genuinely in-flight work during the drain window.
   `armTimer` keeps its WARN-and-skip, but gains a note tying the skip to shutdown so it is
   not mistaken for a lost timer, and the durable-store rehydration guarantee is documented.
   No behavioural change.

## 9. `service.Engine` Propagation

`service.Engine` inherits the mechanism with minimal work:

- **Drain** is inherited: `Engine.Shutdown` already delegates to `driver.Shutdown`.
- **Rejection** is inherited: `StartInstance`, `DeliverMessage`, `DeliverSignal`,
  `Claim/Complete/ReassignTask`, `CancelInstance`, and `ResolveIncident` all funnel through
  the driver's gated methods, so `ErrDriverShuttingDown` surfaces from them automatically.
- **One nuance — human-task pre-writes.** `ClaimTask`/`CompleteTask`/`ReassignTask` write
  to the task store *before* reaching the driver's `ApplyTrigger`, so on a shutdown race the
  task-store write could land just before rejection. To keep quiescence honest, add an early
  `draining` check at the top of those Engine methods (via an exported
  `driver.IsShuttingDown() bool`) that rejects with `ErrDriverShuttingDown` before any
  task-store side effect.

## 10. Testing Plan (TDD, red-first)

Per the project's TDD Operational Discipline, every new symbol is preceded by an observable
failing test. Planned cases (black-box `runtime_test` where possible):

- **Gate:** `Drive`, `ApplyTrigger`, `DeliverMessage`, `BroadcastSignal`, and a timer-start
  after `Shutdown` each return `ErrDriverShuttingDown`.
- **Drain:** `Shutdown` blocks until an in-flight `Drive` (held on a barrier action) returns;
  assert ordering with a channel/latch.
- **Timeout (D2):** in-flight work exceeding the `ctx` deadline → `Shutdown` returns
  `ErrDrainTimeout`, and the in-flight instance still completes afterward.
- **`WithShutdownTimeout` fallback (D3):** `Shutdown(context.Background())` with a hung
  instance returns `ErrDrainTimeout` after ~`d`; a `ctx` with its own deadline overrides `d`.
- **Deadline honoured (Finding 3):** the scheduler close respects a short `ctx` deadline.
- **Idempotency:** a second `Shutdown` returns nil.
- **Ordering invariant:** an in-flight timer fire during drain completes (no
  `Add`-after-`Wait` panic); run the whole suite under `-race`.
- **Engine propagation:** `Engine` entry points return `ErrDriverShuttingDown` after
  `Engine.Shutdown`; human-task methods reject before any task-store write.

Coverage target ≥ 85% on touched packages; `go test ./...` and `golangci-lint run ./...`
clean.

## 11. Follow-ups / Open Items

- Whether to expose a read-only `Draining()`/`IsShuttingDown()` on `service.Engine` for
  consumer health checks (HTTP `503` during drain). Deferred; not required for this change.
- Whether transport handlers (`transport/http/*`) should map `ErrDriverShuttingDown` to
  `503 Service Unavailable`. Noted for a transport-layer follow-up; out of scope here.
