# 34. Compensation on error and cancel

- Status: Accepted
- Date: 2026-06-23

## Context

The engine records `CompensationRecord`s for completed compensable activities and hoists them to
`RootCompensations` on scope close (ADR-0013). These are run only by the admin `CompensateRequested`
trigger (reverse-order walk via `StatusCompensating` + `compensationCursor`, finishing with
`StatusTerminated`). The two terminal saga paths — an unhandled error (`propagateError` →
`StatusFailed`+`FailInstance`) and a cancel (`CancelRequested` → `StatusTerminated`+`FailInstance{cancelled}`)
— drop the records and terminate without undoing completed work. ADR-0013 deferred
compensation-on-error/cancel as "different semantics, out of scope." The user has now authorized it.

## Decision

Route the terminal unhandled-error path and the cancel path through the existing compensation walk
when `RootCompensations` is non-empty, with an engine-only change (no model types, no migration):

1. **Parametrize the walk's terminal outcome** on `compensationCursor` via two additive fields,
   `FinalStatus Status` and `FinalErr string`. `stepCompensationFinish` (full-rollback branch) applies
   them: error ⇒ `StatusFailed` + `FailInstance{errorCode}`; cancel ⇒ `StatusTerminated` +
   `FailInstance{"cancelled"}`; admin full-rollback ⇒ zero fields ⇒ `StatusTerminated`, no
   `FailInstance` (unchanged).
2. **Extract `beginCompensation(def, s, finalStatus, finalErr, at, mode)`** from
   `stepCompensateRequested` (token cancel + record lookup + first compensation `InvokeAction` +
   cursor set, now stamping the outcome). With no records it finish-immediately applies the outcome,
   so empty-records cancel/error terminate exactly as today.
3. **Wire CancelRequested and propagateError-terminal** to call `beginCompensation` when records
   exist; otherwise verbatim prior behaviour. `InvokeCancelAction` (ADR-0028) still fires alongside.
4. **Best-effort compensation:** an `ActionFailed` matching the cursor's `ActiveCmdID` while
   `StatusCompensating` routes to advance (skip+continue), never back into `propagateError`/retry.

`cloneState` copies the new fields; `Step` stays pure and deterministic.

## Consequences

**Positive**
- Failed and cancelled processes now undo completed compensable work (proper saga semantics), reusing
  the proven reverse-order walk.
- Engine-only, additive: no model change, no migration; persisted in-flight cursors deserialize with
  zero outcome ⇒ prior admin behaviour.
- Empty-records and admin-compensation paths are byte-for-byte unchanged (existing tests stay green).
- Best-effort compensation prevents a failing compensation action from stranding the instance.

**Negative / trade-offs**
- A cancelled/failed instance with compensable nodes now spends time in `StatusCompensating` before
  reaching its terminal state — a visible lifecycle change (cancel/fail is no longer instantaneous).
  Documented; correct for sagas.
- Best-effort means a compensation action's failure is logged/skipped, not retried — a compensation
  that must succeed needs its own internal retry. (Compensation-action retry policy is future work.)
- The terminal `FailInstance{errorCode}`/`{cancelled}` is now emitted at the *end* of the walk rather
  than immediately; consumers observing `FailInstance` see it after compensation, not before.

**Deferred**
- Scope-targeted compensation / `Compensate` producer (ADR-0035).
- Per-node & definition cancel handlers (ADR-0036).
- Compensation-action retry/incident on repeated failure.

## Post-acceptance fix (2026-06-23): idempotent re-cancel

**Found by whole-branch review:** `RootCompensations` was never cleared after the walk completed.
The `CancelRequested` guard (`len(s.RootCompensations) > 0`) stays true on a terminal instance, and
`deliverLoop` has no terminal-state guard, so a second `Runner.CancelInstance` call on an
already-terminated compensable instance re-entered the entire compensation walk — re-emitting every
`InvokeAction` (double-compensation of money-moving actions such as `"refund"`).

**Fix:** In `stepCompensationFinish`, on the full-rollback (`toNode == ""`) branch, after applying the
terminal outcome (`Status`/`EndedAt`), the compensation records for the cursor's scope are cleared:

- If `scopeID == ""` (root scope, the only walk today): `s.RootCompensations = nil`
- Else: find the scope by ID and set `sc.Compensations = nil` (future scope-targeted walks, ADR-0035)

This makes a re-delivered `CancelRequested` on a terminal instance a clean no-op: the guard
`len(s.RootCompensations) > 0` is now false, so the immediate-termination path runs, which detects the
already-set `Status`/`EndedAt` and produces no new `InvokeAction`.

**Scope:** `engine/step.go` (`stepCompensationFinish`) only. The partial-rollback (`toNode != ""`)
branch is admin-only with no public trigger — records are not cleared on that path (deferred).

**Coverage:** `TestRedeliveredCancelIdempotent` in `engine/step_compensation_error_cancel_test.go` asserts
that a second `CancelRequested` on a terminal instance emits no new `InvokeAction` and that
`RootCompensations` is nil after the walk. All existing compensation tests stay green.
