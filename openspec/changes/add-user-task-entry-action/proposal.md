# Proposal: an entry action on user tasks, and one failure policy for awaited task actions

## Why

A definition author who needs data loaded before a human sees a task has to model a separate `serviceTask` in front of the `userTask` (kartaladev/worktrack#24). kartaladev/wrkflw#119 (decided) settled the shape: an **entry action** is an awaited task action that runs when the user task is activated, before the task exists, so the task's variables snapshot, eligibility predicate and candidates see the action's output. The same decision gave the two awaited task actions, entry and completion, one failure vocabulary, `on_failure: stop | ignore`. This change builds that half of #119. The actor-model half is `replace-casbin-with-composed-deciders`.

**This is a post-release change.** The maintainer ruled that kartaladev/wrkflw#121 is planned in full now but is not applied before the first v0 tag. That is why `cut-v0-release` is in *Depends on*: nothing here lands until the release is cut.

Re-measured on `main` (`eebe55ba`), branch base `b8576841`:

| Premise (#119 / #121) | Re-measurement |
|---|---|
| A user task is created, offered and parked in one step (`step_nodes.go:778-829`) | **Holds, drifted.** `userTaskStrategy.enter` is `engine/step_nodes.go:740-834`. |
| Task ids are per visit, minted by `nextTaskID` (`step_state.go:156`) | **Holds, drifted.** `engine/step_state.go:167`; minted at `step_nodes.go:743`. |
| `ManualImmediate` completes the task on entry and advances in the same step | **Holds.** `step_nodes.go:770-787`. |
| Completion actions park the token (`parkOnCompletionAction`) and failure runs retry → `RecoveryFlow` → boundary → incident | **Holds.** `engine/step_triggers.go:853`; the chain is `handleActionFailed`, `:357`. |
| `completion_action` is honoured on UserTask **and** ReceiveTask | **Holds**, and #119 did not address ReceiveTask (Open Question Q2). |
| "the catalog-name check reused for `entry_action`" (#121 §1, #119 A7) | **Stale.** No Build-time catalog check exists for any action name. An unregistered name is answered at dispatch with a non-retryable `ActionFailed` ("unknown action: …", `runtime/processdriver_action.go:375-390`). This change keeps that behaviour for the entry action and specifies it; no Build rule is added. |
| A retried action is re-invoked by the same action | **Hypothesis, not reproduced.** `handleRetryFired` and the `ResolveIncident` retry both call `reinvokeServiceAction`, which re-emits `mainActionName(node)`: the service-task action, else the node id (`engine/step_timers.go:233-247`, `engine/main_action.go:7-12`). Read this way, a retried user-task completion action invokes an action named after the node. No test covers it (`engine/completion_action_test.go` pins only the non-retryable path). Task 1.1 writes the failing test first. |

## What Changes

Decided and applied, not reopened: kartaladev/wrkflw#119 (decided), the *Decision* comment's entry-action half: awaited before creation; the task id minted at entry and carried on the token; `Call{Kind: entry, TaskID: <pending id>, IdempotencyKey: "<id>:entry", Actor: zero, Input: {}, Applied: {}, Variables}`; `on_failure: stop | ignore` on entry and completion, default `stop`; `ignore` journals the failure, logs a WARN, raises no incident and proceeds with variables unchanged; the deadline starts at task creation; `ErrEntryActionOnImmediate` and `ErrOnFailureWithoutAction` at Build. kartaladev/wrkflw#25 (decided) applies: `on_failure` is encoded by name.

- **New: `entry_action` on `userTask`.** `activity.UserTask` gains `EntryAction` and `WithEntryAction(name)`. When the token enters the node, the engine mints the task id, keeps it on the token as the pending task id, dispatches the entry action with the `humantask.Call` envelope and parks. On success the action's output merges into variables, then the task is created with the pending id, so its variables snapshot, eligibility predicate and candidate resolution all see the output. Each visit (a loop) runs the action once with a fresh id and key. While the action is in flight no task exists: no task record, no `AwaitHuman`, no deadline or reminder timer.
- **New: `on_failure: stop | ignore` on the entry and completion actions of a `userTask`.** Retries by the node's retry policy come first in both modes. `stop` (the default) runs today's chain: `RecoveryFlow`, else error boundary, else incident. On the entry action no task is created. `ignore` journals the `ActionFailed`, logs a WARN, raises no incident and skips `RecoveryFlow` and the boundary. On entry it creates and offers the task with variables unchanged. On completion it advances the token with the human's completion output kept and no action output. `UserTask` gains `EntryFailure` and `CompletionFailure` (an `OnFailurePolicy`), set with `WithOnFailure`.
- **Wire and YAML: `completion_action` gains an object form.** `entry_action` and `completion_action` accept a string (the catalog name, meaning `stop`) or `{name, on_failure}`. The encoder writes the bare string when the policy is `stop`, so every definition stored today re-encodes byte for byte (design D3). An `on_failure` value outside `stop` / `ignore`, compared exactly, is refused with a new `model.ErrInvalidOnFailure`. An object with a blank or non-string `name`, or a value that is neither string nor object, is refused with a new `model.ErrInvalidActionRef`, on the pattern of `ErrInvalidRule`.
- **Build rules.** `ErrEntryActionOnImmediate` refuses an entry action on an immediate manual task. `ErrOnFailureWithoutAction` refuses an `ignore` policy on an absent action. `entry_action` on any other kind is refused at decode by the existing `ErrKeyNotOnKind`.
- **Retries and incident resolution re-invoke the action that failed** (entry or completion), under the same idempotency key. This pins the hypothesis above for both actions.
- **Docs.** The `humantask` package doc states: *an awaited task action stops the instance on terminal failure unless the author says to ignore it; an update action fails to its actor.* It also says the deadline starts at task creation, after the entry action. Release-note lines go to the release after `cut-v0-release`; `completion_action` gaining an object form and `ignore` on completion are the two consumer-visible lines.
- **BREAKING (narrow): a blank `completion_action` string is refused.** A whitespace-only string decodes today and dispatches an action named by that whitespace. It is now refused with `ErrInvalidActionRef`. An empty string is still treated as absent. Every other existing definition decodes, validates and runs as today. The new `Call` kind `entry` widens the `humantask.CallKind` vocabulary that `add-user-task-updates-call-envelope` introduces.
- **Persisted formats (flagged), all additive, no migration:**
  - The engine snapshot gains the token's pending task id. It is empty in every pre-change snapshot, which decodes as "no entry action in flight".
  - Stored definitions may carry the object form. A definition using it cannot be read by a library version older than this change: a downgrade hazard, not an upgrade one.
  - `on_failure` is stored by name.
  - Journal, outbox and task-store rows are unchanged: the entry action's `ActionCompleted` and `ActionFailed` are ordinary rows.
- **Default security posture:** unchanged. `ignore` is opt-in, and the default `stop` is today's behaviour. **Consumer go.mod:** no change.

Absorbed issues:
- kartaladev/wrkflw#121: absorbed whole, except its catalog-name check, which is stale (see the table). This change closes #121.
- kartaladev/wrkflw#119: decision issue, already closed; cited as decided.

Decisions #119 left open (design, Open Questions; specs carry the recommended option, marked pending):
- **Q1** Does a successful entry action record a compensation entry when the node declares `compensate_action`? Recommended: no.
- **Q2** Is `on_failure` accepted on a ReceiveTask's `completion_action`? Recommended: no, refused with `ErrOnFailureUnsupportedKind`.
- **Q3** Are the node's boundary events armed while the entry action runs, or only at task creation? Recommended: at task creation.

Out of scope:
- The `humantask.Call` envelope and the `humantask.CallKind` vocabulary this change adds `entry` to: owned by `add-user-task-updates-call-envelope`, which lands first and lists this change under *Blocks*.
- The `Progressed` state machine and update actions: kartaladev/wrkflw#41, owned by `add-user-task-updates`, which lands first.
- `Principal()`, the one-role actor and the deciders: kartaladev/wrkflw#115 with the #119 amendment, owned by `replace-casbin-with-composed-deciders`, which lands first.
- Re-driving a dispatched-but-unanswered `InvokeAction` after a crash: kartaladev/wrkflw#110, owned by `persist-pending-commands`. This change only states that the re-driven entry action carries the same key.
- The service-task `instance:node` idempotency key that repeats across loop visits (recorded on kartaladev/wrkflw#117): not owned by any change. The entry action does not inherit it.
- Name encoding of persisted enums in general: `encode-persisted-enums-by-name`.
- A pre-release fix of the completion-action retry hypothesis, should it prove red. This change pins it post-release; an earlier fix needs its own change (design, Risks).

**Coordination**
- Size: L
- Depends on: cut-v0-release, add-user-task-updates-call-envelope, add-user-task-updates, replace-casbin-with-composed-deciders
- Blocks: nothing
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `human-task-lifecycle`:
  - ADDED: the entry action runs before the task exists, and the task sees its output.
  - ADDED: one entry action per visit, under the pending task id.
  - ADDED: no task exists while the entry action is in flight.
  - ADDED: a node left during the entry action creates no task.
  - ADDED: `on_failure` on the entry action.
  - ADDED: `on_failure` on the completion action.
  - ADDED: the deadline starts at task creation.
  - ADDED: an entry action's success records no compensation (pending Q1).
  - ADDED: boundaries are armed at task creation (pending Q3).
- `definition-wire-format`:
  - ADDED: `entry_action` and `completion_action` accept a name or `{name, on_failure}`.
  - ADDED: `on_failure` is refused outside its vocabulary.
  - ADDED: malformed action references are refused.
- `definition-validation`:
  - ADDED: an entry action on an immediate manual task is refused.
  - ADDED: a failure policy without an action is refused.
  - ADDED: `on_failure` on a ReceiveTask is refused (pending Q2).
- `retry-policy`:
  - ADDED: a retried or resolved task action re-invokes the same action under the same key.

## Impact

- **Definition:** `definition/activity` (the `UserTask` fields and options) and `definition/model` (`node_wire.go`, `node_wire_keys.go`, `yaml.go`, `validate.go`, the action-reference decode). These files are shared with `refuse-unrecognized-wire-values`, `refuse-case-variant-json-keys`, `improve-wire-gate-diagnostics` and `replace-casbin-with-composed-deciders`, all of which land first.
- **Engine:** `engine/step_nodes.go` (`userTaskStrategy.enter`), `engine/step_triggers.go` (`handleActionCompleted`, `handleActionFailed`, the completion park), `engine/step_timers.go` (`reinvokeServiceAction`), `engine/state.go` (the token's pending id), `engine/step_cancel.go` and the other paths that remove a token, and the snapshot codec.
- **humantask:** the `Call` kind `entry` (shared with `add-user-task-updates-call-envelope`) and the package doc (shared with `add-user-task-updates`).
- **Golden files:** the definition wire golden file gains the object form.
- **Stores, HTTP adapters, runtime:** no code change expected. Parity is asserted by scenarios: no task row exists while the entry action runs, on all three dialects and all three adapters.
