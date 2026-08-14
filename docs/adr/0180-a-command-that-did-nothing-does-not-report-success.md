# 180. A command that did nothing does not report success

- Status: Proposed (**audited** — rule-#9, 3 lenses; both decisions were corrected by it)
- Date: 2026-08-13

> Design and every measurement:
> [`docs/specs/2026-08-13-engine-visibility-and-truthfulness.md`](../specs/2026-08-13-engine-visibility-and-truthfulness.md) §4.
> Premise evidence: `docs/specs/2026-08-13-adr-0178-0180-premise-evidence.md`, defects (2) and (3).
> Plan: [`docs/plans/2026-08-13-engine-visibility-and-truthfulness.md`](../plans/2026-08-13-engine-visibility-and-truthfulness.md).
>
> Closes backlog **2** and **3**. ⚠ **BREAKING** — see Consequences.

## Context

Two engine commands return `nil` — success — for work they did not do.

### Duplicate start

Executed: `engine.Step(StartInstance)` on a live compensating instance returns `err=<nil>` and
**superimposes a second start**:

```
   BEFORE: status=compensating tokens=1 tasks=1
   Step(StartInstance) err = <nil>
   AFTER:  status=running     tokens=3 tasks=2   cursor UNCHANGED across the restart? true
```

The old parked token, its human task and its armed timers all survive beside the new ones — one
human task became two — and `StartVariables`, the audit record of what the process was started
with, is overwritten. Measured consequence: the worker still executing the in-flight compensation
reports back and is rejected with `ErrTokenNotFound` (→ 422), because the compensation route is
gated on `StatusCompensating`, which the restart cleared. A control row proves the same trigger
succeeds without the restart.

Two corrections to the inherited framing, **both widening the defect**: it does not "restart from
the top" (it superimposes), and it is **not specific to `compensating`** — `StartInstance` is
accepted on any non-terminal instance, a plain `running` one going tokens 1 → 2. `Drive`'s refusal
is store-level id-uniqueness, which refuses a `running` id identically and has nothing to do with
the walk.

Of **seven** entry points that can start an instance, **two are unguarded** — `engine.Step` and
`ProcessDriver.ApplyTrigger`. Three are guarded by id-uniqueness, two by fresh-id minting. No HTTP
route accepts an arbitrary trigger.

### Dropped cancel

Executed against an admin **partial** rollback in flight, all three layers:

| layer | result | did anything change? |
|---|---|---|
| `engine.Step` | `err=<nil>`, 0 commands | **state byte-identical to before the cancel** |
| `ProcessDriver.CancelInstance` | `err=<nil>` | persisted snapshot unchanged |
| `service.CancelInstance` | `err=<nil>` → **HTTP 200** | same |

and then: `after the walk finishes: status=running tokens=1`. The operator who cancelled gets a
200, and the instance goes back to running. The engine comment at `step_triggers.go:210` calls this
an accepted limitation and never surfaces it.

`handleCancelRequested` has **five** return sites: one error, two doing real work, and **two
returning `nil` without terminating** — `:196` (deferred, `PendingCancel=true`) and `:210`
(dropped). They are semantically different: *"will terminate later"* vs *"will not terminate at
all"*.

## Decision

1. **`handleStartInstance` refuses an already-started instance** with a new sentinel
   `ErrInstanceAlreadyStarted`.
   ⚠ **Not a `Status` test.** `StatusRunning = iota` is the **zero value**, so a fresh never-started
   state already reads as `StatusRunning`; a status-keyed guard would refuse every legitimate start.
   ⚠⚠ **And `StartedAt.IsZero()` alone is insufficient.** `s.StartedAt = t.OccurredAt()` is its only
   writer and `engine.Step` is public API where the caller supplies `at`. Executed with the naive
   guard patched in:
   ```
   CONTROL   start#1 err=<nil> tokens=1 StartedAt=2026-06-20 10:00:00 UTC
   CONTROL   start#2 err=<already started> tokens=0     <- refused
   ZERO-TIME start#1 err=<nil> tokens=1 StartedAt=0001-01-01 IsZero=true
   ZERO-TIME start#2 err=<nil> tokens=2                 <- SUPERIMPOSED
   ```
   The predicate is therefore
   **`s.StartedAt.IsZero() && len(s.Tokens) == 0 && len(s.History) == 0`** — refuse when any
   evidence of a prior start exists.
2. **The dropped-cancel site returns `ErrCancelNotApplicable`**, mapped by `service` to
   `ErrConflict` → **422**. The deferred site keeps returning `nil`: it recorded intent, and the
   instance really will terminate.
   ⚠ **Amended in-bundle — 422, not 409.** `service.ErrConflict` and `engine.ErrInvalidTransition`
   both classify to **422** (`transport/http/httpcore/errors.go:48`); **409 is
   `kernel.ErrConcurrentUpdate` alone** (`:34`). The pre-implementation text said 409 in three
   places, inherited from the evidence record. The behaviour is as designed; the number was wrong.
   ⚠⚠ **Amended in-bundle — the site serves TWO situations and this ADR generalised from the one it
   measured.** It is reached (i) by an admin **partial** rollback, which resumes, so the cancel is
   genuinely lost — the measured defect; and (ii) by a redundant cancel during a **terminal**
   cancel/error walk, where the instance *will* terminate — ADR-0034's post-acceptance *idempotent
   re-cancel*. Returning the sentinel from both turned
   `TestSecondCancelMidCompensationWalkDoesNotDoubleCompensate` **RED**. The refusal is therefore
   gated on the existing shared predicate: `if !s.Compensating.walkTerminates(s.PendingCancel)`.
3. **Both sentinels wrap `ErrInvalidTransition`.** Not merely stylistic: measured, without the
   wrapping the driver's answer reaches HTTP as a **500 with an empty body**, because
   `httpcore.ClassifyError` has no arm for a bare sentinel.
   ⚠⚠ **The sentinel must be a REPORTING outcome, not a propagation-halting error.** Required shape:
   in `ProcessDriver.CancelInstance`,
   `if err != nil && !errors.Is(err, engine.ErrCancelNotApplicable) { return st, err }` so
   propagation still runs, returning the sentinel **after** it; and in `propagateCancel`'s child
   loop, on that sentinel log and **recurse anyway**. See Consequences for the measurement that
   forced this.

Rejected: **an outcome field on `StepResult`** — touches every handler for the same information a
sentinel carries. Rejected: **observability only** (log the drop, keep returning `nil`) — it closes
the "invisible" half and leaves the "lied to" half, which is the operator's actual complaint.

## Consequences

**Positive.** Two commands stop lying. State corruption via a superimposed start becomes
impossible through the two unguarded entry points. An operator whose cancel was dropped learns it
was dropped instead of watching the instance resume.

**Negative / accepted.**

- ⚠ **BREAKING.** A consumer treating `err == nil` from `CancelInstance` as "cancelled" now gets a
  **422** for the dropped case; a consumer relying on `StartInstance`'s current superimposition gets
  an error. The latter corrupts state, so no correct consumer depends on it. **Release note
  required.**
- ✅ **`transport/http` needed no production edit** — the plan's "likely no-op" prediction held.
  `httpcore.ClassifyError` already classifies `ErrInvalidTransition`, which both sentinels wrap; only
  pin rows were added to its tests.
- ✅ **Start-path compatibility — EXECUTED, no regression.** The message-, signal- and timer-start
  paths lean on `ErrInstanceExists` from `Store.Create` for their at-least-once no-op. All three
  build a **fresh** `InstanceState` and never re-enter `handleStartInstance` on an existing one; the
  audit ran the full container-free suite under a patched guard and it stayed green.
- ⚠⚠ **The dropped-cancel sentinel nearly shipped as a subtree-orphaning regression.** The
  pre-audit draft reasoned that `propagateCancel`'s child loop would absorb a new error because it
  logs-and-continues. The loop *does* absorb it, and the conclusion was still wrong — proved by
  mutation:
  ```
  (a) child status BEFORE parent cancel = running
      CancelInstance err = … cancel not applicable
      child status AFTER  parent cancel = running   (IsTerminal=false)

  (b) WARN runtime: propagateCancel: cancel child instance failed child_id=…
      child must be Terminated:      expected 4 (terminated) actual 0 (running)
      grandchild must be Terminated: expected 4 (terminated) actual 0 (running)
  ```
  Two independent failures: `runtime/processdriver_cancel.go:26-29` returns the error **before** the
  propagation block at `:30-33`; and inside the loop `continue` at `:89` skips the **recursion** at
  `:92`. Today those children terminate. Implemented naively, this decision would leave a terminated
  parent with a permanently running subtree — strictly worse than the silent `nil` it replaces.
  Hence the reporting-outcome shape in Decision 2.
- ⚠ **Amended in-bundle after `/code-review` (owner gate): the drop is not logged as a failure.**
  The WARN line quoted in (b) above is the *mutation's* output and stayed correct as history, but
  the shipped loop kept it unconditional — so the by-design outcome (a child owned by an admin
  partial rollback keeping its own walk) was reported at WARN as
  `"cancel child instance failed"`. That trains an operator to ignore the one line that means the
  propagation genuinely could not reach a child. `propagateCancel` now logs the two apart: the WARN
  failure line moved **inside** the `!errors.Is(cancelErr, engine.ErrCancelNotApplicable)` branch,
  and the drop gets its own `slog.LevelDebug` line,
  `"runtime: propagateCancel: child kept its own compensation walk; cancel dropped"`, whose
  attribute is `reason` rather than `error`. The recursion behaviour is unchanged.
- ⚠ The pre-audit draft cited `CancelRequested.terminalPolicy() == rejectSilently` as the safety
  argument. That is a **category error**: the policy governs triggers on *terminal* instances, and
  `:210` is reachable only on a non-terminal compensating one. Removed.
- An instance cancelled while a compensation retry backoff is pending (bundle C) receives this 409
  for a walk that is *waiting*, not stalled, with no way to distinguish. Noted for bundle C.
- The already-terminal cancel route keeps its current behaviour: `service` refuses it with
  `ErrConflict` and it never reaches HTTP, though `ProcessDriver.CancelInstance` still answers
  `err=<nil> status=terminated`. Narrowing that is a separate decision and is **not** made here.
