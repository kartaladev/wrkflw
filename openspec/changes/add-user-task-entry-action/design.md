# Design

## Context

See proposal.md, *Why*, for the motivation and the re-measured premises. The shape of the feature was decided on kartaladev/wrkflw#119 (four rounds plus an adversarial audit). That issue's *Decision* comment holds the options it rejected, with reasons:
- a fire-and-forget entry: its output cannot reach the task, and its failure is silent;
- a `Preparing` task state: a sixth state for a window nobody observes;
- an entry-only failure knob: a second word for the same thing on completion later;
- leaving `TaskID` out of the entry `Call`: the service-task key shape, including its loop wrinkle.

This design does not reopen them. It records how the decided shape lands on the current engine, and the questions #119 left open.

The constraints the engine imposes, all measured on `main` at `eebe55ba`:

- `userTaskStrategy.enter` (`engine/step_nodes.go:740-834`) does all of the following in one step: mints the task id (`:743`), links the visit to it (`setVisitTask`, `:769`), handles `ManualImmediate` (`:770-787`), schedules the deadline and reminder, appends the task, emits `AwaitHuman`, parks the token on the task id and arms the boundaries.
- `handleActionCompleted` (`engine/step_triggers.go:104`) does not know which action of a node completed. It records a compensation entry whenever the node declares `compensate_action` (`:150-154`), merges the output, and advances the token.
- `handleActionFailed` (`:357`) does not know which action failed either. It runs retry, then `RecoveryFlow`, then the error boundary, then the incident, keyed on the node.
- `handleRetryFired` and the `ResolveIncident` retry both call `reinvokeServiceAction` (`engine/step_timers.go:233`). That function re-emits `mainActionName(node)`, which resolves to the node's service action or, failing that, the node id.

The engine therefore has no notion of *which* awaited action a parked token waits on. The entry action is the first case where a node has two awaited actions that can be in flight at different times: entry, then completion.

## Goals / Non-Goals

**Goals:**
- The parked token says which task action it awaits, so completion, failure, retry and incident resolution each act on the right action.
- The pending task id survives every step boundary: snapshot, restart and retry. It is cleared on every path that removes the token.

**Non-Goals:**
- No change to update actions (`add-user-task-updates`) or to deadline and reminder actions, which stay fire-and-forget with no knob, per #119.
- No new task state and no new store column.

## Decisions

### D1. The token records the awaited task action, not only its command id

The token gains two fields: the pending task id (the decided `PendingTaskID`) and an awaited-action marker (`entry` or `completion`; empty for every other park). The marker is set in the same assignment that parks the token on a task action's command, and cleared by `clearAwait`. The four sites that need to know which action is awaited read it:
- `handleActionCompleted`: an entry success creates the task; a completion success advances the token.
- `handleActionFailed`: chooses which `on_failure` policy applies.
- `reinvokeServiceAction`: chooses which action to re-emit.
- the compensation record (D4).

- *Rejected: infer the action from `PendingTaskID != ""`.* That covers entry against completion, but not the retry path. A retry timer re-parks the token on a timer id. When the timer fires, the token has no command and still carries `PendingTaskID`, so this inference holds. But completion is then identified only by "no pending id on a user-task node". That is the same blind spot `reinvokeServiceAction` has today (proposal table), and a third awaited action would break it. An explicit marker is one small field, and it removes the guessing.
- *Rejected: look the action up from the command id in a side table.* The engine keeps no per-command record for awaited actions. Adding one is a larger snapshot change than a field on the token.
- Evidence: the reinvoke path is measured above (`step_timers.go:233-247`, `main_action.go:7-12`). Whether the completion retry is wrong *today* is a hypothesis until task 1.1 runs.

### D2. Entry splits `userTaskStrategy.enter` at a single point

The split goes before the `ManualImmediate` branch: `ErrEntryActionOnImmediate` makes the two exclusive. The steps are:
1. If the node has an entry action and the token carries no pending id, mint the id, keep it on the token, emit the entry `InvokeAction` with the `Call` envelope from `add-user-task-updates-call-envelope`, park and return. Nothing else runs on this branch: no visit-task link, no timers, no boundaries (Q3), no task.
2. Otherwise, take the pending id or mint one, clear the pending id, and continue exactly as today.

Entry success (`ActionCompleted`, marker `entry`) clears the await, merges the output and re-enters the strategy. Entry `ignore` (`ActionFailed`, marker `entry`, terminal) logs a WARN and re-enters the strategy without merging. The deadline then starts at task creation by construction: it is resolved in step 2 at that step's time.

- *Rejected: create the task in the `ActionCompleted` handler directly.* That duplicates the deadline, reminder, visit-link and boundary logic of `enter`, which is the drift the strategy table exists to prevent.

### D3. The wire form is a string or an object; the encoder emits the string for `stop`

A decoder modelled on the reserved `rule` key (`ErrInvalidRule`) reads both `entry_action` and `completion_action`. The encoder writes the bare string whenever the policy is `stop`, and the object form only for `ignore`.

- *Rejected: always emit the object form.* Every stored definition with a `completion_action` would then re-encode differently. The definition store compares re-encodings on republish (`harden-definition-store-parity`), and the spec *Library-encoded definitions decode back unchanged* would need an exception. The golden-file scenario in the `definition-wire-format` delta pins this.
- *Rejected: separate `entry_failure` and `completion_failure` keys.* #119 A1 decided a single `on_failure` vocabulary that travels with its action.
- Sentinels. `ErrInvalidOnFailure` (out-of-vocabulary value) and `ErrInvalidActionRef` (a reference that names nothing) are new and named here. They follow the sentinel pattern of `refuse-unrecognized-wire-values` (`ErrInvalidEndBehavior`, `ErrInvalidTriggerKind`).
- Build-time and decode-time refusals are kept apart. `ErrOnFailureWithoutAction` is a `Validate` rule, because the Go API can set a policy with no action. On the wire, a policy without a name is a malformed reference, refused earlier with `ErrInvalidActionRef`.

### D4. Compensation stays tied to the completion action (pending Q1)

`handleActionCompleted` records compensation whenever the node declares `compensate_action`. Without a guard, an entry success would record an entry that undoes a completion action that never ran. The recommended guard records compensation only when the marker is not `entry`. See Q1.

### D5. `on_failure: ignore` short-circuits before the chain

In `handleActionFailed`, the retry interception runs first, unchanged, so retries come first under both policies (#119). The `ignore` check sits at the terminal-exhaustion point, before `RecoveryFlow`, the boundary and the incident. It logs a WARN carrying the instance, node, command and error. Then:
- for entry, it re-enters the strategy (D2);
- for completion, it advances the token as a completion success with no output would.

The `ActionFailed` row is journaled as every trigger is. No incident is raised, because #54 reserved incidents for what the operator can fix, and an ignored failure blocks nothing.

### D6. Cross-change coupling

| Change | Coupling | Order |
|---|---|---|
| `cut-v0-release` (#48) | Maintainer ruling: post-release | Must be archived first |
| `add-user-task-updates-call-envelope` | Owns `humantask.Call[In]`, the `humantask.CallKind` vocabulary and the envelope builder. This change adds kind `entry` and its key `<task_id>:entry`, which #119 decided independently of that change's Q1 on the other kinds' keys | Lands first; lists this change under *Blocks* |
| `add-user-task-updates` (#41) | Owns the `Progressed` state machine and update actions; the `humantask` package doc's failure rule is shared | Lands first |
| `replace-casbin-with-composed-deciders` (#115, #119 amendment) | Owns `Principal()` and the one-role actor. The entry `Call` carries a zero actor; `definition/activity` and `definition/model` files are shared | Lands first; lists this change under *Blocks* |
| `persist-pending-commands` (#110) | Re-drives an unanswered entry `InvokeAction` after a crash; the entry key must be stable across that (spec scenario) | Lands first (pre-release) |
| `encode-persisted-enums-by-name` (#25) | `on_failure` and the awaited-action marker are encoded by name in the snapshot and the wire | Lands first (pre-release) |
| `refuse-unrecognized-wire-values`, `refuse-case-variant-json-keys`, `improve-wire-gate-diagnostics` | Same decode files (`node_wire.go`, `node_wire_keys.go`, `yaml.go`) | Land first |

## Risks / Trade-offs

- [The completion-action retry hypothesis proves red, so released v0 retries user-task completion actions under the wrong name.] → This change pins it post-release (task 1.1). If it is red, the orchestrator should open a separate pre-release change for the completion half alone. This change cannot be that change, because it waits on `cut-v0-release`.
- [A snapshot written by this version, with a pending id and marker, is read by an older binary after a rollback.] → Unmeasured. A reading of the current `handleActionCompleted` suggests the older binary ignores the unknown fields and advances the token past the node with no task, but that is a hypothesis. Task 6.3 measures it, and rollback guidance in *Migration Plan* states the result.
- [A slow entry action delays the deadline.] → Decided on #119 and documented. The spec pins the due date at task creation.
- [`ignore` hides a broken integration.] → The default is `stop`; `ignore` is opt-in, logs a WARN on every occurrence, and journals the failure.

## Migration Plan

Nothing to migrate: snapshot, wire and store changes are all additive (proposal, persisted formats). Rollback is safe only while no instance is parked on an entry action and no stored definition uses the object form. The release note for the release after `cut-v0-release` says so.

## Open Questions

Each needs a maintainer ruling; the specs carry the recommendation, marked PENDING.

**Q1. Compensation on entry success.** When a userTask declares `compensate_action`, does a successful entry action record a compensation entry?
- (a) No: compensation stays paired with the completion action. `ErrCompensateActionWithoutForwardAction` already defines the completion action as the forward action of a user task. **Recommended.**
- (b) Yes: the entry action is also a forward action. Then the node needs a second compensate action, or the one action must undo two things.

**Q2. `on_failure` on a ReceiveTask's completion action.** `completion_action` is honoured on ReceiveTask too (`engine/node_accessors.go:23-30`). #119 decided the vocabulary for *task* actions only.
- (a) Refuse `ignore` on receiveTask with a new `ErrOnFailureUnsupportedKind`; the object form with `stop` is accepted, so the wire shape stays uniform. Widening later is not breaking. **Recommended.**
- (b) Honour it on receiveTask as well, for one vocabulary on every awaited completion action.

**Q3. Boundary events during the entry action.** Round 1's sketch on #119 armed no boundaries before the task exists, but the *Decision* comment states only the deadline rule.
- (a) Arm at task creation, as the deadline is. A hung entry action is the domain of retry and incident. **Recommended.**
- (b) Arm at node entry, so an interrupting timer or message boundary can abandon a slow entry action. The entry action is then cancelled mid-flight, which needs a cancel path for an awaited action that the engine does not have today.
