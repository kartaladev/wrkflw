# Proposal

## Why

An actor cannot record progress on a user task before completing it. The only human operations are claim, reassign, complete and candidate refresh, and the only audit trail is the instance journal. The required feature is an update declared in the task definition, validated against its own schema, possibly backed by a consumer action, and audited on every change. It has no implementation: `wrkflw_task_update`, `HumanProgressed` and `InputTrigger` occur nowhere in the tree on `b8576841`. The positive-control search for `wrkflw_human_task` finds three files. kartaladev/wrkflw#29 (decided) fixed the design after three rounds and an adversarial audit. kartaladev/wrkflw#28 (decided) supplied the prior art: WS-HumanTask's `setOutput` and `InProgress`, and no engine that validates an update against a schema.

## What Changes

This is part 2 of 4 of kartaladev/wrkflw#41 ("Build the UserTask updates feature"), the core: #41 sections 1 (definition), 2 (engine, except the envelope), 3 (persistence) and the engine half of 6 (docs and tests). The design is kartaladev/wrkflw#29 (decided), applied and not reopened. Part 1 is `add-user-task-updates-call-envelope` (the `humantask.Call` envelope). Part 3 is `add-user-task-updates-http` (#41 section 4). Part 4 is `add-user-task-updates-outbox` (#41 section 5). Each cites #41. The orchestrator closes #41 once all four are created.

- **Declaration.** `activity.Update{Name, Required (default true), DependsOn, Validation, Action, Resubmit}`, `UserTask.Updates` and `WithUpdates(...)`. JSON and YAML gain an `updates` list on `userTask`. `NodeSpec` and the singular `validation` slot are untouched, and the slot keeps meaning the assembled completion output. The library golden file is regenerated once.
- **Build rules** with new `model` sentinels: `ErrManualTaskUpdates`, `ErrDuplicateUpdateName`, `ErrUpdateDependencyUnknown`, `ErrUpdateDependencyCycle` and `ErrUpdateResubmitPolicy`. `ErrUpdateActionUnknown` depends on Q3, below. `updates` on any other node kind is refused by the existing key-on-kind rule.
- **One input-validation dispatch.** Input-bearing triggers implement `engine.InputTrigger`, which names the slot each input validates against. `runtime.inputOf` (`runtime/processdriver.go:939`) is deleted. A guard test over the trigger registry fails when a trigger carries a `map[string]any` and does not implement the interface. This closes #29's constraint 2: a new trigger can no longer be accepted unvalidated in silence.
- **New trigger `engine.HumanProgressed{TaskID, Update, Input, Actor, Applied}`.** It is authorized as the new `authz.OpProgress` under the ownership rule with implicit claim (kartaladev/wrkflw#114, decided), through the driver gate that `authorize-task-triggers-in-driver` builds. The step checks, in order: the task is open; no update is in flight (`engine.ErrUpdateInFlight`); the update is declared (`engine.ErrUnknownUpdate`, a new sentinel, design D3); every dependency is applied (`engine.ErrUpdateDependencyMissing`, naming the missing updates); and the resubmit policy (`engine.ErrUpdateRefused`). An action-less update is applied in the same step. An action-backed update dispatches `InvokeAction` with a `Call` of kind `update`, parks the token on the command, and records the update as pending.
- **Update action outcomes.** Success records `applied`, with the action's output as the value. A terminal failure, after the node's retry policy, records `rejected` with the reason and restores the task. There is no `RecoveryFlow` and no incident: *a task action fails to the party that can act* (#29 A2). A task closed while an action is in flight records `abandoned` at each of the four closing sites.
- **New task state `Progressed`.** It is entered when an update is applied, and its name is encoded per kartaladev/wrkflw#25 (decided). A progressed task always carries a claim and at least one applied update. A pending update only exists on a claimed or progressed task. Reassign keeps `Progressed` and its updates. Claim, reassign, completion and a second update are refused with `ErrUpdateInFlight` while an action is pending.
- **Completion of a task that declares updates.** `humantask.Completable(task, decl)` reports the missing required updates. Completion refuses with `engine.ErrRequiredUpdatesMissing` before any state change. Next, the whole-output descriptor is checked over the assembled values. The values are then merged into variables namespaced by update name. A task that declares no updates keeps today's flat merge, byte for byte.
- **Audit table `wrkflw_task_update`** (`task_id, seq, name, status, submitted_seq, input, value, reason, actor, occurred_at`, primary key `(task_id, seq)`). It is append-only on all three dialects and in the memory and caching stores. `kernel.AppliedStep.TaskUpdates` writes it in the commit transaction, with the snapshot, journal and outbox. An action-backed update writes two rows (`submitted`, then `applied`, `rejected` or `abandoned`), and an action-less update writes one. `TaskUpdateReader.LatestApplied` and `History` read it. The runtime loads `LatestApplied` before the step for `HumanProgressed` and `HumanCompleted`: *the snapshot holds references; a trigger that needs values carries them; the runtime loads them.*
- **`TaskService.Progress`**, so a library consumer submits an update without HTTP, as it does with Claim and Complete.
- **Docs.** The `humantask` package doc states the failure rule, and the update kind joins the envelope section.

Re-measured on main (`b8576841`). #29 and #41 cite `c1420e00` and `7b8b7bbb`.
- **Drifted:** the four task-closing sites are now `engine/state.go:726`, `step_cancel.go:39`, `step_stale_commands.go:170` and `step_timers.go:95`. #41 cites `state.go:683` and `step_timers.go:89`. Reassign sets `Claimed` unconditionally at `step_triggers.go:695` (#29: `:640-641`). The validation switches are at `engine/target_node.go:15` and `runtime/processdriver.go:939`.
- **Holds:** `kernel.AppliedStep` is the snapshot, trigger, events and call-link fields (`runtime/kernel/ports.go:51-61`). The task row is written after commit by `UpdateTask`. `TaskState` has four values (`humantask/humantask.go:24-34`), and the task-row `state` column is already `TEXT` by name. `IsOpen` admits only `Unclaimed` and `Claimed` (`humantask/humantask.go:173-174`), so `Progressed` must be added to it.
- **Stale:** "`ErrUpdateActionUnknown` (the service-task catalog check, reused)". No Build-time action-name check exists for any action kind. `ProcessDefinition.CancelActions` says action-name existence "is not validated here" (`definition/model/definition.go:47-48`), and an unknown name is answered at dispatch with a non-retryable `ActionFailed` (`runtime/processdriver_action.go:375`). The sibling `add-user-task-entry-action` found the same thing. This is Q3.
- **Stale:** #41's blocker prose. `action.Typed` (#117) and implicit claim (#114) are closed. The live blockers map to the changes in Depends on.
- **Hypothesis, carried from `add-user-task-entry-action`, not reproduced here:** a node-policy retry re-invokes `mainActionName(node)` (`engine/step_timers.go:233-247`), not the action that failed. If that holds, a retried update action would invoke the wrong action. Task 4.4 writes the failing test first.

Impact flags:
- **Persisted format: YES.** A new table, `wrkflw_task_update`, in three dialects. New snapshot fields: `HumanTask.Updates`, `Pending` and a per-task update counter. A new journal trigger kind, `HumanProgressed`. `HumanCompleted` gains the loaded applied values. A new task-state name, `progressed`, in the snapshot and the task row. The table follows whichever migration-window ruling `persist-pending-commands` Q3 receives (in place in `0001_init.sql`, or `0002`). Under the in-place option, a database created earlier must be recreated.
- **BREAKING (Go API):** `humantask.TaskState` gains a value. `IsOpen` changes meaning, and a consumer switch over task states must handle `Progressed`. The `HumanCompleted` and task-store invariants widen.
- **Default security posture:** tightened, not loosened. A new write operation (`OpProgress`) is authorized at the transition. The snapshot's pending actor is withheld from unidentified callers unless actors are disclosed.
- **Consumer go.mod:** unchanged.

Out of scope:
- The envelope type and the completion, deadline and reminder kinds: `add-user-task-updates-call-envelope` (part 1).
- The three HTTP routes, their disclosure and their error classification: `add-user-task-updates-http` (part 3).
- Task-level outbox events and the `instance.completed` line: `add-user-task-updates-outbox` (part 4).
- Re-driving an unperformed update action after a crash: `persist-pending-commands` (kartaladev/wrkflw#110, #167). A `submitted` row with no closing row is what its sweep finds.
- Off-map, recorded on #29 and not filed: a request-level idempotency key on the human routes, cascade invalidation as a per-update option, and a `SchemaDescriber` drift check at Build.
- Bounding how many rows a `replace` update may accumulate, and the update input's size ceiling: `bound-untrusted-input` owns the input ceilings. Rows per task are recorded as a risk in design.

Decisions this change cannot be fully specified without (design, Open Questions). The specs mark the dependent rows *pending*.
- **Q1.** How a resubmission refused by `resubmit: refuse` is both recorded as `rejected` (#29 round 2, Q5 (ii) a) and reported as `ErrUpdateRefused` (#41). Recommended: commit the `rejected` row, then return `ErrUpdateRefused` carrying the seq.
- **Q2.** What a completion may carry on a task that declares updates: new update submissions ("submittable … all at once to complete", #29 round 1 (i)) and a flat `Output`. Recommended: no submissions. Updates are submitted one per request, and a non-empty `Output` is refused with a new sentinel.
- **Q3.** `ErrUpdateActionUnknown`, whose premise is stale. Recommended: no Build rule, matching `add-user-task-entry-action`. An unknown name is a `rejected` row at dispatch.
- **Q4.** The pending update's actor. #41 puts it on `Pending`, while the #29 Decision says "no actor in the snapshot". Recommended: keep it on `Pending` while in flight, projected only under actors.

**Coordination**
- Size: XL
- Depends on: add-user-task-updates-call-envelope, authorize-task-triggers-in-driver, replace-casbin-with-composed-deciders, persist-pending-commands, encode-persisted-enums-by-name, split-persistence-modules
- Blocks: add-user-task-updates-http, add-user-task-updates-outbox, add-user-task-entry-action, cut-v0-release
- Readiness: human

## Capabilities

### New Capabilities

- `human-task-updates`: declared, ordered, named updates on a user task. Covers submission and its gates, action-backed outcomes, the update envelope, the `progressed` state, the in-flight guard, the audit rows and how they are read, and completion with updates.

### Modified Capabilities

- `human-task-lifecycle`: the closed-task and terminal-instance refusals cover `HumanProgressed`. Reassignment keeps a progressed task progressed. Task stores accept and validate `progressed`.
- `definition-validation`: ADDED, Build refuses malformed update declarations.
- `definition-wire-format`: ADDED, `updates` round-trips on `userTask` in JSON and YAML, and is refused elsewhere and on unknown inner keys.
- `human-task-authorization`: ADDED, submitting an update is authorized as progress, with implicit claim.
- `instance-store`: ADDED, update rows commit atomically with their step on every store and are read back in order.
- `instance-disclosure`: ADDED, the update references and the pending actor in the instance projection.

## Impact

- `definition/activity` (`Update`, `UserTask.Updates`, `WithUpdates`), `definition/model` (wire, YAML, Build rules, golden file).
- `engine`: `trigger.go` (`HumanProgressed`, `InputTrigger`), `target_node.go`, the human handlers in `step_triggers.go`, `handleActionCompleted` / `handleActionFailed`, the four closing sites, `taskCall` (kind `update`), and the sentinels.
- `humantask`: `TaskState` `Progressed`, `IsOpen`, `validate.go`, `HumanTask.Updates` / `Pending`, `Completable`, and the `TaskUpdate` / `TaskUpdateReader` types.
- `authz`: `OpProgress`. The ownership decider's arm is owned here per `replace-casbin-with-composed-deciders` design.
- `runtime`: `processdriver.go` (`inputOf` removed, `LatestApplied` loaded before the step), `runtime/kernel/ports.go` (`AppliedStep.TaskUpdates`), the memory store, `runtime/task/service.go` (`Progress`).
- `persistence` (after `split-persistence-modules`): migrations in three dialects, the store's `Create`/`Commit` write, the reader, the caching store, the trigger codec, and `TestMigrationParity_*` with the at-rest census.
- `transport/http/httpcore` projection, for the new snapshot fields only.
- Docs: `humantask` package doc, `runtime/README.md`.
