# Compensation on error/cancel — Implementation Plan

> Executed via superpowers:subagent-driven-development. STRICT TDD (this is a core engine change): a visible RED (`go test ./engine/...` failing) precedes every behavioural change, in its own Bash call. Determinism + `cloneState` + import-purity are load-bearing.

**Goal:** On unhandled terminal error and on cancel, run the existing compensation walk before terminating (when `RootCompensations` is non-empty), with the walk's terminal outcome parametrized on the cursor. Engine-only; no model change; no migration.

**Architecture:** Extend `compensationCursor` with `FinalStatus`/`FinalErr`; extract `beginCompensation`; make `stepCompensationFinish` apply the outcome; wire `CancelRequested` + `propagateError`-terminal to begin compensation when records exist; route compensation-action `ActionFailed` to advance (best-effort).

**Tech Stack:** Go 1.25, engine pure state machine, testify, clockwork (test-only at runtime layer).

## Global Constraints
- Module `github.com/kartaladev/wrkflw`; no `pkg/` prefix.
- **STRICT TDD**, RED before GREEN, visible in its own `go test` call.
- **`Step` stays PURE + DETERMINISTIC** (no clock/random; commands a pure fn of `(def, state, trigger)`). **`cloneState` must copy the new cursor fields** (extend the cloneState test). **Engine import-purity** (no transport/storage/bus/time-vendor imports).
- **No model change, no migration.** Changes only in `engine/` (+ a runtime e2e test).
- `workflow-` error prefix; assert `errors.Is`; black-box `engine_test`/`runtime_test`; table-test assert-closure; `t.Context()`.
- **Backward-compat (load-bearing):** empty-records cancel/error paths AND the admin `CompensateRequested` path are byte-for-byte unchanged — existing engine tests stay green.
- Gate: `go test -race -p 1 ./...` green; ≥85% on engine + runtime; `golangci-lint` clean.
- Spec: docs/specs/2026-06-23-compensation-on-error-cancel-design.md. ADR-0034.

## File Structure
- `engine/state.go` (**modify**) — `compensationCursor.FinalStatus/FinalErr`; `cloneState` copies them.
- `engine/step.go` (**modify**) — `beginCompensation` extracted from `stepCompensateRequested`; `stepCompensationFinish` applies outcome; `CancelRequested` + `propagateError`-terminal wiring; `ActionFailed`-during-compensation → advance.
- `engine/step_compensation_test.go` / new `engine/step_compensation_error_cancel_test.go` (**create/extend**) — new cases.
- `engine/state_test.go` (**modify**) — cloneState covers new fields.
- `runtime/compensation_oncancel_test.go` (**create**) — e2e.

---

### Task 1: cursor outcome + finish parametrization + beginCompensation (refactor-preserving)

**Files:** modify `engine/state.go`, `engine/step.go`; extend `engine/state_test.go`.

**Goal:** Pure refactor + new plumbing that leaves ALL existing behaviour identical (admin compensation path unchanged), preparing the outcome-carrying walk. No new terminal-path wiring yet (Task 2).

**Steps (TDD):**
1. Extend `engine/state_test.go` cloneState test: set `Compensating.FinalStatus = StatusFailed`, `FinalErr = "x"` on a state, `cloneState`, assert the clone carries them (and mutating the clone doesn't affect the original). Run RED (fields undefined).
2. Add `FinalStatus Status` + `FinalErr string` to `compensationCursor` (engine/state.go), with godoc per spec §2.1. Confirm `cloneState` copies `Compensating` by value (it should already, as a value field — verify; if it deep-copies fields individually, add the two). Run GREEN.
3. Refactor: extract `beginCompensation(def *model.ProcessDefinition, s *InstanceState, finalStatus Status, finalErr string, at time.Time, mode StepMode) (StepResult, error)` containing the current body of `stepCompensateRequested` from the token-cancel pre-commands through the first `InvokeAction` (set `cursor.FinalStatus=finalStatus, FinalErr=finalErr`). Have `stepCompensateRequested` call `beginCompensation(def, s, 0, "", t.OccurredAt(), mode)` (preserving the admin ToNode handling — keep ToNode logic in stepCompensateRequested or thread it; simplest: keep stepCompensateRequested computing the ToNode/records then delegate the emit, OR pass ToNode into beginCompensation. Choose the cleaner factoring; the admin path MUST stay behaviourally identical). Update `stepCompensationFinish` to read `s.Compensating.FinalStatus`/`FinalErr` on the `toNode==""` branch: set `Status = FinalStatus or StatusTerminated`; if `FinalErr != ""` append `FailInstance{Err: FinalErr}`; append `cancelAllTimers`+`cancelAllArmsAndBoundaries`; clear cursor. Admin path (zero outcome) ⇒ Terminated, no FailInstance (unchanged).
4. Run the FULL existing engine compensation tests — they MUST stay green (proves the refactor is behaviour-preserving). `go test ./engine/ -run Compensat`. 
5. `golangci-lint ./engine/...`. Commit `refactor(engine): outcome-carrying compensation cursor + beginCompensation`.

---

### Task 2: wire cancel + error terminal paths + best-effort comp-action failure

**Files:** modify `engine/step.go`; create `engine/step_compensation_error_cancel_test.go`.

**Steps (TDD):**
1. Write the new engine tests (per spec §4): cancel-with-compensation (Compensating→Terminated+FailInstance{cancelled}, refund action emitted); error-with-compensation (→Failed+FailInstance{errorCode}); empty-records cancel/error unchanged; best-effort comp-action failure (ActionFailed on a comp action → walk continues to next record → terminal outcome). Run RED (cancel/error don't compensate yet; comp-action ActionFailed mishandled).
2. Implement:
   - **CancelRequested** (step.go:118-154): keep `InvokeCancelAction` emission; if `len(s.RootCompensations) > 0`, delegate to `beginCompensation(def, s, StatusTerminated, "cancelled", t.OccurredAt(), mode)` (which clears tokens + cancels timers/arms + emits first comp action), prepend the `InvokeCancelAction` cmds, return. Else verbatim current path.
   - **propagateError terminal-unhandled** (step.go:~1995): if `len(s.RootCompensations) > 0`, return `beginCompensation(def, s, StatusFailed, errorCode, at, mode)` cmds (merge with anything already required). Else verbatim current path. Touch ONLY the terminal-unhandled branch.
   - **ActionFailed during compensation:** in the `ActionFailed` dispatch, when `s.Status == StatusCompensating && t.CommandID == s.Compensating.ActiveCmdID`, route to `stepCompensationAdvance(def, s, t.OccurredAt(), mode)` (best-effort skip). Mirror the existing `ActionCompleted`→advance dispatch.
3. Run new + existing engine tests green. `go test -race ./engine/...`. Lint.
4. Commit `feat(engine): compensate on error and cancel before terminating`.

---

### Task 3: runtime e2e + docs/gate (controller does docs)

**Files:** create `runtime/compensation_oncancel_test.go`.

**Steps (TDD):**
1. Write a runtime e2e (MemStore + a catalog with a compensable action + its compensation action): start a process whose first service task is compensable and completes, park at a user task, `CancelInstance` → assert the compensation action ran (recorded) and the instance is `StatusTerminated`. Run RED→GREEN.
2. Commit `test(runtime): compensation-on-cancel e2e`.
3. Controller: ADR-0034 verify, HANDOVER + memory, full gate, whole-branch review, merge.

## Verification Checklist
- [ ] `go test -race -p 1 ./...` green; ≥85% engine + runtime.
- [ ] `golangci-lint run ./...` clean.
- [ ] Model production diff ZERO; engine import-purity intact (no new imports).
- [ ] `Step` determinism test + `cloneState` test (incl. new fields) green.
- [ ] Existing admin-compensation + cancel + error tests unchanged and green (back-compat).
- [ ] Whole-branch review (opus); merge + push; HANDOVER + memory.

## Spec coverage self-check
- §2.1 cursor fields + cloneState → Task 1. §2.2 finish outcome → Task 1. §2.3 beginCompensation → Task 1. §2.4 cancel/error wiring → Task 2. §2.5 best-effort comp-fail → Task 2. §3 determinism → tests. §4 runtime e2e → Task 3. ✓
