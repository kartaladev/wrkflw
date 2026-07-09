# ReverseInstance Follow-ups Plan (FU#1 + FU#2)

> Two follow-ups the user asked to clear before merging `feat/reverse-instance`. Grounded by the
> opus assessment (2026-07-10). SDD: fresh implementer per task (TDD), task review after each,
> whole-branch re-review before merge. Land **FU#2 first** (contained bug fix), then **FU#1**.

**Base:** `13169e3` (branch head after the hardening wave).

## User-confirmed decisions
- **FU#1:** `WithTargetNode(X)` restores variables to X's **start-of-visit snapshot** by DEFAULT — reversing
  ADR-0109's documented "keeps current variables" contract. Documented as breaking in **new ADR-0116**.
  Raw `NewCompensateRequested(at, toNode)` (admin partial-rollback / cancel-preempt) is UNCHANGED (keeps current vars).
- **FU#2:** fix the ESP-arm timer leak at **all 3** terminate sites (compensation-terminate + immediate-cancel +
  immediate-fail), giving a coherent "terminate never leaks an ESP timer" invariant.

## Global Constraints
- **TDD strict:** every behavioral change preceded by a *failing* `go test ./<pkg>/...` with visible red.
- Error sentinels: `workflow-engine:` (engine), `workflow-runtime:` (facade). Load always-on Go skills + task-specific + custom `table-test`.
- Black-box `_test` where the SUT allows; white-box `package engine` only for the internal helper unit test.
- The compensation-**terminate** path was verified byte-identical during hardening; FU#2 is a DELIBERATE change (terminate now also cancels ESP timers) — the test must pin the new `CancelTimer` emission.
- `go build ./...`, `go test ./...`, `-race` on touched pkgs, `golangci-lint run ./...` clean; touched pkgs ≥ 85%.

---

## FU#2 — ESP-arm timer leak on terminate (land FIRST; 3 tasks)

Neither `cancelAllTimers` (`engine/state.go:639`) nor `cancelAllArmsAndBoundaries` (`engine/state.go:669`) drains
`s.EventSubprocesses` (deliberate exclusion, `state.go:664-668`, because `cancelAllArmsAndBoundaries` also runs at
walk-START in `beginCompensation` where ESP arms must survive for the T4 re-arm). `removeEventSubprocessArmsForScope`
drains one scope only. Three terminate sites emit the two sweeps but never drain ESP arms:
`applyTerminate` (`step_compensation.go:576-577`), `handleCancelRequested` immediate/no-records
(`step_triggers.go:188-189`), `propagateError` immediate/no-records (`step_errors.go:413-414`).

### Task F2.1 — `removeAllEventSubprocessArms` sweep-all helper
- Files: `engine/state.go` (+ white-box `engine/state_esp_test.go` or extend an engine white-box test).
- Red: construct an `InstanceState` with two ESP arms across two scopes — one timer-armed (`TimerID:"esp-t1"`), one
  signal-armed (no TimerID). Assert `s.removeAllEventSubprocessArms()` returns `["esp-t1"]` and leaves
  `s.EventSubprocesses == nil`. Compile-fail red (`undefined`).
- Impl: add the helper near `removeEventSubprocessArmsForScope`, iterating all arms (deterministic slice order),
  collecting non-empty `TimerID`s, setting `s.EventSubprocesses = nil`. Godoc it.
- Commit: `feat(engine): removeAllEventSubprocessArms sweep-all helper for terminal paths`.

### Task F2.2 — `applyTerminate` cancels ESP timers
- Files: `engine/step_compensation.go` (`applyTerminate`), test in the compensation-terminate suite.
- Red: drive an instance with a root ESP armed (timer-triggered) AND one completed compensable activity, then
  `CancelRequested` → compensation walk → `applyFinish` → `applyTerminate`. Assert `StepResult.Commands` include
  `CancelTimer{TimerID:<esp timer>}`, and resulting state `EventSubprocesses == nil`, `Status == StatusTerminated`.
  Red today (no ESP CancelTimer).
- Impl: after the existing `cancelAllTimers`/`cancelAllArmsAndBoundaries` appends, `for _, id := range
  s.removeAllEventSubprocessArms() { cmds = append(cmds, CancelTimer{TimerID: id}) }`.
- Commit: `fix(engine): cancel event-subprocess timers on compensation terminate`.

### Task F2.3 — immediate-cancel + immediate-fail paths
- Files: `engine/step_triggers.go` (`handleCancelRequested` no-records ~189), `engine/step_errors.go`
  (`propagateError` no-records ~414), tests.
- Red: table — (i) instance with a root ESP armed and NO compensation records → `CancelRequested` → assert ESP
  `CancelTimer`, `EventSubprocesses==nil`, `Status==Terminated`; (ii) same reaching `propagateError` immediate-fail
  (unhandled error, no records) → assert ESP `CancelTimer`, `Status==Failed`. Red today (both leak).
- Impl: the same three-line sweep at both sites.
- Commit: `fix(engine): cancel event-subprocess timers on immediate cancel and fail paths`.

---

## FU#1 — restore target-node start-of-visit vars on `WithTargetNode` (land SECOND; 6 tasks)

The correct snapshot is **X's own compensation record's `Input`** (`records[toNodeIdx].Input`) — vars as they were when
execution arrived at X, before X ran (`Input` captured in `handleActionCompleted` `step_triggers.go:71` via `copyVars`
BEFORE `mergeVars`, per `CompensationRecord.Input` godoc `state.go:161-164`). X always has a record on the
`WithTargetNode` path (`beginCompensation` errors otherwise, `step_compensation.go:213`); records are RETAINED on
partial finish, so the lookup happens in `stepCompensationFinish`. Opt-in signal so admin partial-rollback is
untouched: a new `RestoreTargetVars bool` on `CompensateRequested` + a `NewReverseToNode` constructor the facade uses.
Carry the snapshot on the `finishPlan` (a map — keep the `compensationCursor` all-scalar: cursor gets only the
`RestoreTargetVars bool`).

### Task F1.1 — `NewReverseToNode` + `RestoreTargetVars` field
- Files: `engine/trigger.go`, `engine/trigger_test.go`.
- Red: assert `NewReverseToNode(at,"X")` == `CompensateRequested{ToNode:"X", RestoreTargetVars:true, ReverseNode:"", ResetVars:false}`; `NewCompensateRequested` leaves `RestoreTargetVars` false. Compile-fail red.
- Impl: add field (godoc) + constructor (godoc). Commit `feat(engine): CompensateRequested.RestoreTargetVars + NewReverseToNode`.

### Task F1.2 — shape guard
- Files: `engine/step_compensation.go` (`stepCompensateRequested`), test.
- Red: `CompensateRequested{RestoreTargetVars:true}` (empty ToNode) ⇒ `workflow-engine:` error (RestoreTargetVars requires ToNode). Red.
- Impl: guard beside the existing `ResetVars`/`ReverseNode` guard. Commit `fix(engine): reject RestoreTargetVars without ToNode`.

### Task F1.3 — CORE: cursor + finish restores target vars
- Files: `engine/state.go` (`compensationCursor.RestoreTargetVars` scalar), `engine/step_compensation.go`
  (`finishPlan.restoreVars map[string]any`, `lastCompensationRecordByNode` helper, cursor stamps in
  `beginCompensation` early-finish + main + `stepCompensationAdvance` rebuild, plan build in `case toNode != ""`,
  apply in `applyFinish` resume block), `engine/reverse_instance_test.go`.
- Red: X ran with vars `{a:1}` (its record Input), later nodes mutated to `{a:9,b:2}`; deliver `NewReverseToNode(at,"X")`,
  drive to finish. Assert resume: `Status==Running`, token at X, `Variables == {a:1}` (X's Input, not `{a:9,b:2}`).
  Companion case: admin `NewCompensateRequested(at,"X")` still resumes at X with `{a:9,b:2}` (unchanged). Red today.
- Impl: per assessment — `restoreVars` on the PLAN (not cursor); `applyFinish` resume block:
  `if plan.resetVars { s.Variables = copyVars(s.StartVariables) } else if plan.restoreVars != nil { s.Variables = copyVars(plan.restoreVars) }`.
  Commit `fix(engine): restore target-node start-of-visit variables on target reverse`.

### Task F1.4 — wire codec round-trip
- Files: `internal/persistence/store/trigger_codec.go`, test.
- Red: marshal `NewReverseToNode(at,"X")` → unmarshal → assert `RestoreTargetVars==true` (and ToNode) survive; plain `NewCompensateRequested` leaves it false. Red.
- Impl: `restore_target_vars,omitempty` envelope field + marshal + unmarshal (explicit field set). Commit `fix(persistence): round-trip RestoreTargetVars in the journal codec`.

### Task F1.5 — facade uses `NewReverseToNode` + godoc
- Files: `runtime/processdriver_reverse.go`, `runtime/processdriver_reverse_test.go`.
- Red: `ReverseInstance(..., WithTargetNode("X"))` end-to-end asserts resumed `Variables` == X's start-of-visit snapshot. Red (facade emits `NewCompensateRequested`).
- Impl: swap `WithTargetNode` branch to `NewReverseToNode`; update `WithTargetNode`/`ReverseInstance` godoc (vars restored to target's start-of-visit snapshot; note ToNode most-recent-visit + the ambiguity note stays). Commit `feat(runtime): WithTargetNode restores target-node start-of-visit variables`.

### Task F1.6 — ADR-0116 + memory
- Files: `docs/adr/0116-reverse-target-node-variables.md`, update the deferred-followup memory reference.
- Nygard: Context (ADR-0109's target-reverse contract), Decision (restore X's `Input` snapshot via opt-in
  `RestoreTargetVars`/`NewReverseToNode`; admin path unchanged; new wire field), Consequences (BREAKING change to
  `WithTargetNode`; supersedes the relevant ADR-0109 clause). Commit `docs(adr): ADR-0116 target-reverse variable restore`.

---

## Verification checklist
- [ ] FU#2: all 3 terminate sites cancel ESP timers + drain `EventSubprocesses`; existing cancel/terminate tests still green (verify, don't assume).
- [ ] FU#1: `WithTargetNode(X)` restores X's `Input` snapshot; raw `NewCompensateRequested(at,X)` keeps current vars (admin unchanged); shape guard; codec round-trip; facade + godoc; ADR-0116.
- [ ] `go build ./...`, `go test ./...`, `-race` touched pkgs, `golangci-lint run ./...` clean; touched pkgs ≥ 85%.
- [ ] Whole-branch re-review of the combined follow-ups diff before merge.

## Residual note
- `finishPlan` gains its first map field (`restoreVars`) — kept on the transient plan, NOT the cursor, preserving the
  cursor's all-scalar/value-copied invariant (`state.go:375`). No `cloneState` impact.
