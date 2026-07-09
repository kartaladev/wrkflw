# ReverseInstance Hardening Plan (post-review)

> Executes the confirmed findings from the whole-branch `/code-review` of `feat/reverse-instance`,
> using the opus architecture assessment (2026-07-09). SDD: fresh implementer per task (TDD), task
> review after each, whole-branch re-review before merge.

**Goal:** Close the confirmed correctness gaps in `ProcessDriver.ReverseInstance` and refactor the four
compensation-finish outcomes into one parameterized resume/terminate operation so invariant-restoration
lives in one place.

**Base:** `a3d5d3d` (branch head after the 7 feature tasks). New free ADR after 0109 = 0116 (not used here;
this is a fix wave on the same ADR-0109 feature — amend ADR-0109 Consequences at the end).

## Design decisions (from the assessment — locked)

- **Fork A — engine terminal guard (fixes TOCTOU #6):** `stepCompensateRequested` rejects a reverse trigger
  (`t.ReverseNode != ""`) when `s.Status` is terminal (Completed/Failed/Terminated) with a `workflow-engine:`
  error. Scoped to reverse intent only — plain `NewCompensateRequested` (admin/partial) keeps today's behavior.
  Consequence: the existing reverse tests (which drive to Completed then reverse) must be reshaped to reverse a
  **Running** instance (park after the compensable node). `EndedAt`-clear on resume becomes defensive-only
  (a non-terminal instance never carries EndedAt) — keep it, it's one cheap assignment.
- **Fork B — cancel preempts in-flight reverse (#2):** a `CancelRequested` mid-reverse-walk sets `PendingCancel`
  (widen `handleCancelRequested`'s defer condition from `ResumeNode != ""` to `ResumeNode != "" || ReverseNode != ""`);
  the unified resume op consumes `PendingCancel` → terminates over remaining records (`FailInstance{"cancelled"}`,
  StatusTerminated), mirroring the existing throw-walk protocol. (Semantic call: cancel WINS over reverse.)
- **Fork C — reverse-during-active-walk (#3):** the mid-walk guard in `stepCompensateRequested`
  (`StatusCompensating && ActiveCmdID != ""`) returns a `workflow-engine:` error when the trigger is a reverse
  (`t.ReverseNode != ""`); the partial-admin no-op path is unchanged.
- **Fork D — ESP re-arm (#1):** the full-reverse resume path re-arms ROOT-scope event sub-processes via
  `armEventSubprocesses(def, s, "", at, eval)` (matching `handleStartInstance`), prepending its `ScheduleTimer`
  commands. Gated to the full-reverse branch (`scopeID == ""`); partial/throw resumes do NOT re-arm.
- **Unified-resume refactor:** collapse the four finish outcomes in `stepCompensationFinish` (throw-resume,
  partial-toNode, full-reverse, terminate) into a `finishPlan` descriptor + one `applyFinish` function.
  Land behavior-preserving FIRST (all existing engine tests green), then layer Fork D + Fork B on the unified path.

## Global Constraints

- **TDD strict (CLAUDE.md):** every behavioral change preceded by a *failing* `go test ./<pkg>/...` with visible red.
- Error sentinels: `workflow-engine:` (engine), `workflow-runtime:` (facade).
- Black-box `engine_test` / `runtime_test`; table tests per project `table-test` skill (assert-closure, `t.Context()`).
- Go 1.25; no new dependencies. Load the always-on Go skills + task-specific (`golang-testing`, `golang-safety`,
  `golang-structs-interfaces`, `golang-concurrency`, `golang-error-handling`) + custom `table-test`.
- **Terminate path (cancel/error/admin) must stay behavior-identical** — the refactor's terminate branch is a
  byte-for-byte reproduction of the old 383-421 block; existing cancel/compensation suites are the regression gate.
- Coverage ≥ 85% touched pkgs; `go build ./...`, `go test ./...`, `golangci-lint run ./...` clean before done.

## File map

- `engine/step_compensation.go` — `stepCompensateRequested` (guards), `stepCompensationFinish` → `finishPlan`+`applyFinish`,
  helpers `clearRecords`/`popOneDeferredThrow`.
- `engine/step_triggers.go` — `handleCancelRequested` defer-condition widening.
- `engine/state.go` — `isTerminal`/`(Status).IsTerminal()` helper.
- `engine/reverse_instance_test.go` — fixture reshape (park-after-compensable) + new red cases.
- `internal/persistence/store/trigger_codec.go` — envelope + encode/decode of ReverseNode/ResetVars.
- `engine/step_state.go` — (optional T8) shallow-share StartVariables in cloneState.
- `docs/adr/0109-reverse-instance.md` — amend Consequences with the hardening decisions.

---

## Task T0: reshape reverse-test fixtures to reverse a Running instance (test-only prerequisite)

**Files:** `engine/reverse_instance_test.go`.
**Why:** Fork A's guard forbids reverse-on-terminal; the current fixtures drive to Completed then reverse.
Reshape `reverseSvcDef` / `reverseLoopDef` / `userTaskCompletionReversibleDef` to park after the compensable
node (append a trailing parking `ServiceTask` whose action is never completed in the test), so the instance is
`StatusRunning` with `RootCompensations` already logged when reversed. `TestReverseToStart_ZeroRecords_StillResumesAtStart`
already reverses a Running instance — leave it.

- [ ] Step 1: Reshape fixtures; change preconditions from `StatusCompleted`/`EndedAt != nil` to
  `StatusRunning`/`RootCompensations` len N. Keep the LIFO `Input["attempts"]` newest-first assertions and the
  completion-action `unrecord` assertion intact (only the resumed token position changes: it lands on the new
  park node, not `end`).
- [ ] Step 2: `go test ./engine/... -run TestReverse` — all green (Running instances reverse fine TODAY, so T0 is
  green on its own; it exists to unblock T1's guard without breaking these).
- [ ] Step 3: Commit `test(engine): reverse a Running instance in reverse fixtures (park after compensable)`.

## Task T1: engine terminal guard for reverse (Fork A, #6)

**Files:** `engine/step_compensation.go` (`stepCompensateRequested`), `engine/state.go` (`IsTerminal`),
`engine/reverse_instance_test.go`.

- [ ] Step 1 (red): new case — `NewReverseToStart(t0,"start")` against a **Completed** instance ⇒ expect a
  `workflow-engine:` error. Fails today (silently reverses). Run it, show red.
- [ ] Step 2 (impl): add `func (s Status) IsTerminal() bool` (Completed/Failed/Terminated) in `state.go`; in
  `stepCompensateRequested`, before setting `StatusCompensating`: `if t.ReverseNode != "" && s.Status.IsTerminal()
  { return StepResult{}, fmt.Errorf("workflow-engine: cannot reverse a terminal instance (status %v)", s.Status) }`.
  Delete the stale comment at ~365-367 ("primary use case reverses a COMPLETED instance"); update `NewReverseToStart`
  godoc.
- [ ] Step 3: green; run whole `engine` package. Commit `fix(engine): reject reverse of a terminal instance`.

## Task T2: engine guard for reverse-during-active-walk (Fork C, #3)

**Files:** `engine/step_compensation.go` (`stepCompensateRequested` mid-walk guard), test.

- [ ] Step 1 (red): drive an instance into an in-flight compensation walk (`ActiveCmdID != ""`), deliver
  `NewReverseToStart` ⇒ expect `workflow-engine:` error. Today it's a silent `(state, nil)` no-op. Show red.
  Also add/keep a green case: partial `NewCompensateRequested` during a walk still no-ops (unchanged).
- [ ] Step 2 (impl): inside the `StatusCompensating && ActiveCmdID != ""` guard, `if t.ReverseNode != "" { return
  StepResult{}, fmt.Errorf("workflow-engine: cannot reverse instance while a compensation walk is in flight") }`;
  else keep the no-op.
- [ ] Step 3: green; whole engine. Commit `fix(engine): error on reverse while a compensation walk is in flight`.

## Task T3: unified-resume refactor, behavior-preserving (the approved refactor, #4)

**Files:** `engine/step_compensation.go` (`stepCompensationFinish` → `finishPlan` + `applyFinish` +
`clearRecords`/`popOneDeferredThrow`), test.

- [ ] Step 1 (red): a focused test that a partial-resume (and throw-resume) clears `EndedAt`. Since post-T1 a
  non-terminal instance never has EndedAt set, construct the red by hand-crafting an `InstanceState` with a
  non-nil `EndedAt` + a mid-walk cursor for the partial/throw case and asserting the resume clears it. Fails today
  (partial/throw set StatusRunning without clearing EndedAt).
- [ ] Step 2 (impl): introduce the `finishPlan` descriptor + `applyFinish` per the assessment. Map the four
  outcomes to descriptors. Reverse branch initially `rearmRootESP=false`, no PendingCancel widening (that's T4/T5).
  `EndedAt=nil` on every resume path. Terminate branch = byte-for-byte the old 383-421 block (diff carefully:
  `finalStatus==0 → Terminated`, `cancelOpenTasks`/`FailInstance`/timers/arms ordering).
- [ ] Step 3 (green): the new EndedAt test + the ENTIRE `engine` suite (throw/partial/terminate/reverse/cancel)
  must be green — this is the behavior-preservation gate. Commit
  `refactor(engine): unify compensation-finish into finishPlan/applyFinish (clears EndedAt on all resumes)`.

## Task T4: root-ESP re-arm on full reverse (Fork D, #1)

**Files:** `engine/step_compensation.go` (`applyFinish` full-reverse branch), `engine/reverse_instance_test.go`
(new ESP fixture modeled on `rootLevelESPDef`, `engine/step_subprocess_test.go:775`).

- [ ] Step 1 (red): reverse a Running instance whose def has a root-level event sub-process; assert
  `r.State.EventSubprocesses` is re-populated (an entry with `EnclosingScopeID == ""`) after the reverse resume.
  Fails today (reverse branch never re-arms). For a timer-triggered ESP, also assert a `ScheduleTimer` command on
  the resume Step.
- [ ] Step 2 (impl): on the full-reverse descriptor set `rearmRootESP = (scopeID == "")`; in `applyFinish`
  prepend `armEventSubprocesses(def, s, "", at, eval)` commands on that branch only.
- [ ] Step 3: green; whole engine. Commit `fix(engine): re-arm root event sub-processes on full reverse`.

## Task T5: cancel preempts in-flight reverse (Fork B, #2)

**Files:** `engine/step_triggers.go` (`handleCancelRequested` defer condition), `engine/step_compensation.go`
(`applyFinish` PendingCancel consume), test.

- [ ] Step 1 (red): start a reverse walk (Running instance, `ActiveCmdID != ""`, cursor `ReverseNode` set),
  deliver `CancelRequested`, complete the in-flight `undo` ⇒ expect `StatusTerminated` + `FailInstance{"cancelled"}`.
  Today the cancel hits the no-op and the walk finishes into `StatusRunning`. Show red.
- [ ] Step 2 (impl): widen `handleCancelRequested`'s defer condition to `ResumeNode != "" || ReverseNode != ""`
  (set `PendingCancel`, return cancel-action commands unchanged). In `applyFinish`, on the resume path BEFORE
  clearing records, `if plan.resume && s.PendingCancel { s.PendingCancel = false; s.Status = StatusCompensating;
  return beginCompensation(def, s, "", StatusTerminated, "cancelled", at, mode, eval, "", false) }` — mirroring the
  throw-walk consumption. Confirm the reverse path checks PendingCancel BEFORE clearing RootCompensations.
- [ ] Step 3: green; whole engine (esp. existing cancel-during-throw-walk tests unchanged). Commit
  `fix(engine): cancel preempts an in-flight reverse walk (terminate, not resume)`.

## Task T6: engine guard — ResetVars without ReverseNode (#5)

**Files:** `engine/step_compensation.go` (`stepCompensateRequested`), test.

- [ ] Step 1 (red): `CompensateRequested{ResetVars:true, ReverseNode:""}` ⇒ expect `workflow-engine:` error.
  Today it silently terminates (ResetVars ignored). Show red.
- [ ] Step 2 (impl): in `stepCompensateRequested`, `if t.ResetVars && t.ReverseNode == "" { return StepResult{},
  fmt.Errorf("workflow-engine: ResetVars requires ReverseNode (use NewReverseToStart)") }`.
- [ ] Step 3: green; whole engine. Commit `fix(engine): reject ResetVars without ReverseNode`.

## Task T7: trigger codec round-trips ReverseNode/ResetVars (#7, audit fidelity)

**Files:** `internal/persistence/store/trigger_codec.go`, its test.

- [ ] Step 1 (red): encode `NewReverseToStart(t0,"start")` → decode → assert `ReverseNode=="start"` &&
  `ResetVars==true`. Today decodes as `NewCompensateRequested(at,"")` losing both. Show red.
- [ ] Step 2 (impl): add `reverse_node,omitempty` + `reset_vars,omitempty` to the envelope; set them in the
  `CompensateRequested` encode case; reconstruct in decode (struct literal or a constructor that sets all fields).
  (State rehydration is already correct — snapshot-based; this is journal/audit fidelity only.)
- [ ] Step 3: green; `go test ./internal/persistence/...`. Commit `fix(persistence): round-trip reverse trigger fields in the journal codec`.

## Task T8: cleanup + ADR amendment

**Files:** `engine/step_state.go` (optional), `runtime/processdriver_reverse.go` (godoc), `docs/adr/0109-reverse-instance.md`.

- [ ] Step 1 (optional, only if a test proves StartVariables is never mutated post-start): `cloneState`
  shallow-shares `StartVariables` instead of deep-copying each Step. Low value — DROP if it complicates the
  Clone-independence contract; note the decision either way.
- [ ] Step 2: facade godoc note that the engine now also rejects terminal / active-walk reverses (defense in depth).
- [ ] Step 3: amend ADR-0109 Consequences with the hardening decisions (engine guards, cancel-preempts-reverse,
  ESP re-arm, unified finish, codec fidelity). Commit `docs(adr): amend ADR-0109 with ReverseInstance hardening`.

## Verification checklist

- [ ] Reverse of a terminal instance ⇒ engine `workflow-engine:` error (T1); TOCTOU closed.
- [ ] Reverse during an active walk ⇒ engine error, not silent success (T2).
- [ ] All four finish outcomes go through `applyFinish`; terminate path behavior-identical (T3); every resume clears EndedAt.
- [ ] Full reverse re-arms root ESPs + re-schedules their timers (T4).
- [ ] Cancel mid-reverse-walk ⇒ Terminated, not Running (T5).
- [ ] `ResetVars` without `ReverseNode` ⇒ engine error (T6).
- [ ] Journal codec round-trips ReverseNode/ResetVars (T7).
- [ ] Reshaped reverse fixtures still prove LIFO ordering + completion-action reversibility (T0).
- [ ] `go build ./...`, `go test ./...`, `-race` on touched pkgs, `golangci-lint run ./...` clean; touched pkgs ≥ 85%.
- [ ] Whole-branch re-review of the hardening diff before merge.

## Residual notes (assessment)

- popOneDeferredThrow + a PendingCancel-preempted reverse: terminating with deferred throws pending is pre-existing
  throw-walk behavior (they die with the instance) — acceptable; keep an eye during T5 review.
- `IsTerminal` added as a `Status` method (idiomatic, reusable by runtime view).
