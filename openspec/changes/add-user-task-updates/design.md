# Design

## Context

See proposal.md for motivation and the re-measured premises. The design is kartaladev/wrkflw#29 (decided): the Decision comment, the round-2 answers it cites (Q1 to Q9), and audit items A1 to A11. This document records only how the decision maps onto the code on `b8576841`, plus the few implementation choices the decision left to #41. Where the decision is silent or contradicts itself, the point is an Open Question and is not settled here.

Measured facts this design rests on:
- `engine.TargetNode` (`engine/target_node.go:15`) locates the node a trigger validates against, and `runtime.inputOf` (`runtime/processdriver.go:939-950`) extracts its payload from a closed three-case switch (`StartInstance.Vars`, `MessageReceived.Payload`, `HumanCompleted.Output`). A trigger missing from either switch is not validated, and nothing reports that.
- A user-task token is found by `tokenAwaiting(taskID)`, which reads `AwaitCommand`. A token parked on an action command is not found as a task, as #29 round 2 measured. That gives the in-flight guard for free on the completion path, which uses `tokenAwaiting`. Claim and reassign find the task by `TaskByID` and never consult the token (`step_triggers.go:625-640, 680-698`), so for them the guard must be explicit.
- Tasks close at `engine/state.go:726`, `step_cancel.go:39`, `step_stale_commands.go:170` and `step_timers.go:95`.
- The completion handler validates the outcome, merges `Output`, records the completion, cancels timers and parks on the completion action (`step_triggers.go:930-1015`).
- Error vocabulary: task-state refusals wrap `engine.ErrInvalidTransition` (for example `ErrTaskNotOpen`, `engine/errors.go:66`), which the HTTP classifier maps to 422 `conflict_state`. Caller-fixable payload refusals are plain sentinels with explicit 400 arms (`ErrInvalidOutcome`, `ErrEmptyReassignTarget`).

## Goals / Non-Goals

**Goals:**
- Build #29's model end to end in the library: declaration, Build rules, trigger, state machine, audit rows in the commit transaction, completion with updates, and reads.
- Keep the no-updates path byte-identical: snapshot, journal, variables and task row.

**Non-Goals:**
- HTTP routes, outbox events and the envelope's other kinds, which belong to the sibling changes (proposal, out of scope).
- Re-driving an unperformed update action, which belongs to `persist-pending-commands`.

## Decisions

### D1. `InputTrigger` replaces `inputOf`; `TargetNode` stays

`engine.InputTrigger{ ValidatedInputs(target model.Node) []ValidatedInput }`, where `ValidatedInput{Slot, Strategy, Input}`, is implemented by `StartInstance`, `MessageReceived`, `HumanCompleted` and `HumanProgressed`. `HumanProgressed` returns the named update's `Validation` and its `Input`. The others return today's singular slot. The runtime's gate iterates the returned list. A guard test walks the trigger registry by reflection and fails for any trigger type with a `map[string]any` field that does not implement `InputTrigger`.
- Rejected: adding a fifth case to both switches (#29 Q1 (a) rationale). That keeps the silent opt-out.
- Rejected: widening `NodeSpec` to a map of slots. It breaks a public registration type (#29, rejected list).

### D2. Sequence numbers are allocated by the engine from a per-task counter in the snapshot

`Pending.Seq`, `UpdateRef.Seq`, the envelope's `idempotency_key` (`task_id:seq`) and the 202 response all need the seq before the row exists, and `Step` sees only the snapshot. `HumanTask` therefore carries `UpdateSeq int64`, the last allocated seq. Every row written for the task (`submitted`, `applied`, `rejected` or `abandoned`) takes the next value, starting at 1. A closing row carries `submitted_seq`, the seq of the row it closes.
- Rejected: a database sequence. The memory store would need its own, the seq would be unknown inside `Step`, and the envelope key could not be built.
- Rejected: reusing the journal seq. It is per instance, not per task, and an action-less update and its journal row would be indistinguishable from other steps.

Evidence: the table's primary key `(task_id, seq)` is decided (#29 Decision, Storage), and #29 A4's example numbers the rows of one update 7 and 8.

### D3. Error vocabulary and its classification

- `engine.ErrUpdateInFlight`, `engine.ErrRequiredUpdatesMissing`, `engine.ErrUpdateDependencyMissing` and `engine.ErrUpdateRefused` wrap `engine.ErrInvalidTransition`, because they are task-state refusals, like `ErrTaskNotOpen`. The two that name updates are typed errors (`*UpdateDependencyMissingError{Update, Missing}` and `*RequiredUpdatesMissingError{Missing}`), and `errors.Is` matches the sentinel.
- `engine.ErrUnknownUpdate` is new and plain. Submitting an update the node does not declare is a payload the caller can fix, like `ErrInvalidOutcome`. The project rule "an unknown value is refused with a named error" requires it, and #29 did not name it, so it is exported here. The proposal lists it.
- The HTTP classification of these sentinels is part 3's work. With these wrappings, the existing 422 arm already covers the four state refusals.

### D4. Who writes the rows: the engine describes them, the store writes them

`StepResult` gains `TaskUpdates []humantask.TaskUpdate`, the rows the step produced, built by the engine where the transition happens. The driver copies them into `kernel.AppliedStep.TaskUpdates`, and `Store.Create`/`Commit` insert them in the same transaction as snapshot, journal and outbox (#29 round 2 Q3 (i)). The memory store appends them under the instance lock.
- Rejected: deriving rows in the runtime by diffing snapshots. The `rejected` reason and the `abandoned` cause are known only inside the handler.
- Rejected: a performed command after commit. That is the gap kartaladev/wrkflw#110 exists to close (#29, rejected list).

### D5. Update-action failure is intercepted before the recovery chain

`handleActionFailed` first asks `s.taskPendingOn(commandID)`. If the command is a pending update, the node's retry policy runs as for any action. When the failure is terminal, the step writes `rejected` with `reason = err.Error()`, clears `Pending`, restores `AwaitCommand = taskID`, and returns without reaching `RecoveryFlow`, the error boundary or an incident. `handleActionCompleted` likewise writes `applied` with `value = Output`, sets `Updates[name]`, and enters `Progressed` (applying the implicit claim if it was not recorded yet). Then it restores `AwaitCommand`. Neither merges the output into variables. Values reach variables only at completion (#29 Q3 (a)).

The retry must re-invoke the update action with the same envelope. A sibling change measured a hypothesis that node-policy retries re-emit `mainActionName(node)`. Task 4.4 tests the update path first and fixes it if the hypothesis holds.

### D6. Completion with updates

The order is fixed by #29 (Decision, Completion): `Completable` → `ErrRequiredUpdatesMissing` → the whole-output descriptor over the assembled values → a namespaced merge (`vars[name] = value` for every applied update) → outcome and completion record → the completion action, whose envelope `input` is the assembled map. The assembled values come from `HumanCompleted.Applied`, which the runtime loaded from `LatestApplied`. A node with zero declared updates runs exactly today's path, including `mergeVars(s, t.Output)`, and a byte-equality test pins the snapshot and journal against a golden. What `Output` means when updates are declared is Q2.

### D7. Implicit claim at submission

`HumanProgressed` on an `Unclaimed` task records `Claim{Actor, At}` in the same step (#29 A7). An action-less update then enters `Progressed` in that step. An action-backed one stays `Claimed` with `Pending` until the action returns. Authorization is `OpProgress`, evaluated by the ownership decider as `OpComplete` is (`replace-casbin-with-composed-deciders` owns that decider and hands this arm to this change, per its design's coupling table).

### D8. Snapshot shape

`HumanTask.Updates map[string]UpdateRef{Seq, At}`, `Pending *PendingUpdate{Update, Seq, CommandID, Actor}` (the actor is Q4), and `UpdateSeq`. No input, no value. The snapshot therefore grows with the number of declared updates, not with the number of submissions (#29 Q2 rationale). `Clone` deep-copies all three. The keys are encoded by name, so `encode-persisted-enums-by-name`'s reflective guard sees them.

### D9. A claim, like a reassignment, keeps a progressed task progressed

#29 A6 decided that reassign keeps `Progressed` and its updates. `handleHumanClaimed` also sets `Claimed` unconditionally (`step_triggers.go:639`), and a claim is accepted on an open task that is already claimed. Left alone, a claim would regress a progressed task, which is the defect A6 found in reassign. The same rule is therefore applied to claim: the claim is replaced and the state is kept. This is A6's reasoning extended to the one sibling handler it did not list, and it opens no new question.

### D10. The task row does not carry updates; readers use the snapshot and the table

`wrkflw_human_task` gains no column. Its `state` column holds `progressed` by name. Applied references live in the snapshot, values live in `wrkflw_task_update`, and the row is a post-commit projection written by `UpdateTask`. Because of that, the task-store write invariant checks only what the row holds (claim against state). The update invariants (`progressed` implies an applied update; a pending update implies claimed or progressed) are enforced by `humantask.Validate` on the snapshot's task record. Readers that need `completable` load the instance snapshot, which `add-user-task-updates-http` does for `GET /tasks/{token}`.
- Rejected: an `updates` JSON column on the task row. That is #29 round 1's Q2 (a), superseded by the table in round 2.

### Cross-change coupling

| Change | Coupling | Owner |
|---|---|---|
| `add-user-task-updates-call-envelope` | `humantask.Call`, `CallKind`, `taskCall`. This change adds kind `update`, `previous` and `applied`, and the assembled completion input | envelope change owns the type; this change extends it |
| `authorize-task-triggers-in-driver` | the driver gate (its D1) must list `HumanProgressed` with `OpProgress` | that change owns the gate; this change adds the case |
| `replace-casbin-with-composed-deciders` | `OpProgress` and its `OwnershipDecider` arm; implicit claim; `PrincipalID()` for the recorded actor. Its Blocks line does not list this change, and should | this change owns the arm |
| `persist-pending-commands` | the update's `InvokeAction` is re-driven by its worker. Migration-window ruling (its Q3) decides whether the table goes into `0001_init.sql` or `0002` | that change owns re-drive and the ruling |
| `encode-persisted-enums-by-name` | `progressed` and the update `status` names; its reflective guard gains the new fields | that change owns the guard |
| `split-persistence-modules` | the migration files and store move to the `persistence` module first, so the table is written once in its final home | that change owns the move |
| `guard-authz-field-totality` | any reflective field-totality guard over `HumanTask` or the projection must classify `Updates`, `Pending` and `UpdateSeq` | that change owns its guard |
| `bound-untrusted-input` | an update's `input` is untrusted request data and must pass the same size and depth ceilings as a completion output | that change owns the ceilings |
| `add-user-task-entry-action` | depends on this change's `Progressed` state machine and `taskCall` | that change |
| `add-user-task-updates-http`, `add-user-task-updates-outbox` | read `TaskUpdateReader`, `Completable`, and the `TaskUpdates` of the applied step | sibling changes |

### Findings to export a sentinel

- A blank update name has no decided sentinel. #29 lists six Build rules and none covers it. The spec says "an error naming node and the blank update name" (definition-validation). Recommended export: `model.ErrBlankUpdateName`, so that `ErrDuplicateUpdateName` is not stretched over a different defect.

## Risks / Trade-offs

- [A `replace` update resubmitted without limit grows `wrkflw_task_update` without bound] → Rows are per submission by design, because the audit is the feature. No pruner exists for any table (measured by `persist-pending-commands`). Recorded for a future retention change. No owner exists today.
- [`HumanProgressed` and `HumanCompleted` journal rows carry the applied values the action saw (#29 A8), so journal rows grow with the applied data] → Accepted by the decision. The journal has no consumer-facing reader.
- [Adding `Progressed` changes `IsOpen` and every consumer `switch` on `TaskState`] → v0.x. The release note goes to `cut-v0-release`. `htParseTaskState`'s silent fallback to `Unclaimed` would misread `progressed` before this change's parser update, so the parser is updated in the same commit (task 2.2).
- [The in-flight guard has two mechanisms (token parked elsewhere, and `Pending != nil`)] → One predicate, `task.Pending != nil`, is checked at the top of every human handler, with a test per handler (#29 A6).
- [A deadline fires while an action is in flight. The task closes `abandoned`, and the action's later `ActionCompleted` finds no token] → The existing stale-command path handles a late completion. A test pins that no `applied` row follows an `abandoned` one.

## Migration Plan

Pre-release. The table is added under the ruling of `persist-pending-commands` Q3, in place or as `0002`. Under the in-place option, a database created by an earlier build must be recreated. Snapshots and journal rows written before this change decode with empty `Updates`, nil `Pending`, zero `UpdateSeq` and no applied values, so running instances continue unaffected. A running instance on a definition version without `updates` never sees the feature. Adding `updates` to a published definition makes a new version (kartaladev/wrkflw#111).

## Open Questions

The specs mark the rows that depend on these as *pending Qn*. Every other row holds under every option.

- **Q1. Reporting a resubmission refused by policy.** #29 round 2 Q5 (ii) a decided that a refused resubmission is recorded as `rejected`. #41 names `ErrUpdateRefused` for the same check. A step that returns an error commits nothing, so the two cannot both hold as written.
  - (a) Commit a step whose only effect is a `rejected` row, and have the driver return `ErrUpdateRefused` carrying the seq after the commit. This is the first "committed, then reported as an error" path.
  - (b) Commit the `rejected` row and answer as for any accepted submission (202 with the seq). The refusal is then visible only through history, as an action rejection is.
  - (c) Refuse with `ErrUpdateRefused` and write no row, which reverses Q5 (ii) a.

  **Recommendation: (a).** The caller learns synchronously what the engine knew synchronously, and the audit promise holds. Specs carry "the applied value is unchanged" and "a rejected row is recorded" unconditionally (both decided). The error the caller sees is marked pending.
- **Q2. A completion's own payload on a task that declares updates.** #29 round 1 required updates to be "submittable one at a time or all at once to complete". The Decision orders completion as "validate carried updates → `Completable` → …". Round 2 Q7 rejected a list body on the update route because one request would mean N actions with no clean partial failure. The Decision does not say what `HumanCompleted.Output` means when updates are declared.
  - (a) A completion carries no update submissions. A non-empty `Output` on a task that declares updates is refused with a new `engine.ErrOutputWithUpdates`, which classifies as 400.
  - (b) As (a) for submissions, but `Output` is merged flat before the namespaced merge, and update names win on collision.
  - (c) A completion may carry submissions for action-less updates, each validated and recorded as `applied` in the completion step. Action-backed submissions are refused.

  **Recommendation: (a).** Every value then reaches variables through exactly one audited path, and nothing is merged silently. Specs do not specify `Output` on a task with updates.
- **Q3. `ErrUpdateActionUnknown`.** Its premise, "the service-task catalog check, reused", is stale (proposal).
  - (a) No Build rule. An unknown action name fails at dispatch with a non-retryable `ActionFailed`, which the update failure model turns into a `rejected` row naming the action. `add-user-task-entry-action` chose this for `entry_action`.
  - (b) A Build rule checked only against the definition-scoped catalog (`ProcessDefinition.ScopedCatalog`) when one is present. Names resolved from the global catalog are still answered at dispatch.

  **Recommendation: (a).** It is consistent across every action kind. A rule that sees only the scoped catalog would pass the commonest misspelling. The dispatch-time row holds under both options and is specified.
- **Q4. The pending update's actor in the snapshot.** #41 sets `Pending = {Update, Seq, CommandID, Actor}`, and the #29 Decision says "no values, no actor in the snapshot". The closing row needs the submitting actor (#29 A4's example shows `actor=A` on `applied`).
  - (a) Keep `Actor` on `Pending` while the action is in flight, clear it when the update closes, and project it only under `DiscloseActors`.
  - (b) Leave it out of the snapshot. The runtime loads the `submitted` row before `ActionCompleted` and `ActionFailed` and carries the actor on the trigger, which applies the Decision's own rule ("a trigger that needs values carries them").

  **Recommendation: (a).** It is one field, bounded by one in-flight update per task, and it is already disclosed like a claim. Option (b) adds a store read to every action completion. The disclosure row is specified for (a) and marked pending.
