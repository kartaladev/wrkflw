# Proposal

## Why

Every action a user task invokes today receives a flat copy of the instance variables, the same shape a service task gets. The action cannot tell which task, actor or lifecycle moment called it, cannot deduplicate a re-drive, and cannot be written with `action.Typed` under `WithStrictInput`, because the variables map has no fixed key set. kartaladev/wrkflw#29 (decided) settled one programming model for task actions: *task actions receive task context; instance actions receive instance data.* This change ships that envelope for the three task actions that already exist (completion, deadline, reminder), so the update action that `add-user-task-updates` adds lands on an envelope that is already in place.

## What Changes

This is part 1 of 4 of kartaladev/wrkflw#41 ("Build the UserTask updates feature"): the `humantask.Call[In]` envelope of #41 section 2, last bullet. The design is decided by kartaladev/wrkflw#29 (decided, audit item A1 and the Decision comment) and is applied, not reopened. kartaladev/wrkflw#28 (decided, prior art) is background only.

- **New exported type `humantask.Call[In]`** with JSON names `kind`, `idempotency_key`, `task_id`, `instance_id`, `node_id`, `update` (omitted when empty), `actor`, `input`, `previous` (omitted when nil), `applied` and `variables`, and an exported kind vocabulary `humantask.CallKind` with the names `completion`, `deadline` and `reminder`. `add-user-task-updates` adds `update`; `add-user-task-entry-action` (kartaladev/wrkflw#121, under kartaladev/wrkflw#119, decided) adds `entry`.
- **BREAKING. A user task's completion action receives a `Call`** with `kind` `completion`, the completing actor, the submitted completion output as `input`, and the instance variables after the merge as `variables`. Today it receives the flat variables map (`parkOnCompletionAction`, `engine/step_triggers.go:853-871` on `b8576841`).
- **BREAKING. A user task's deadline action and reminder action receive a `Call`** with `kind` `deadline` or `reminder`, the zero actor and an empty `input`. Today both receive the flat variables map through `emitFireOnceAction` (`engine/step_cancel.go:120-127`, called from `engine/step_timers.go:81` and `:214`).
- **Unchanged, and pinned as negative controls:** service, cancel and compensation actions keep the flat map (#29 A1). So do the actions of nodes that are not user tasks, even where they share the code path today: a receive task's completion action (the same `parkOnCompletionAction`), a deadline or reminder on any other activity or catch event, and boundary and error-boundary actions (`emitFireOnceAction` from `step_boundaries.go:164` and `step_errors.go:214`). The failure handling of every action kind is unchanged: the completion action keeps today's chain (#29 A3), and deadline and reminder actions stay fire-and-forget.
- **Docs.** The `humantask` package doc states the two input conventions and their one-sentence reason, and shows `action.Typed(func(ctx, c humantask.Call[X]) (Y, error), action.WithStrictInput())`.

Re-measured on main (`b8576841`):
- **Holds:** five action input sites, all flat maps. The service task is at `engine/step_state.go:371-380` (with `_idempotencyKey` at `:380`), the completion action at `step_triggers.go:867`, the cancel action at `step_cancel.go:127` and `step_triggers.go:180,204`, compensation at `step_compensation.go:671`, and deadline and reminder through `emitFireOnceAction`.
- **Drifted line numbers** from #29's `c1420e00` figures: the completion action moved from `step_triggers.go:813` to `:867`, and the deadline call site from `step_timers.go:75` to `:81`.
- **New, not in #29:** `emitFireOnceAction` is shared by deadline, reminder, boundary and error-boundary actions, and `parkOnCompletionAction` is shared by user and receive tasks. The envelope therefore has to be chosen per call site and per node kind, not per helper. Design D1 covers this.
- **Holds:** `action.Typed` and `action.WithStrictInput` are on main (`action/typed.go:85,166`), so kartaladev/wrkflw#117's prerequisite is met.

Impact flags:
- **BREAKING (consumer code):** every consumer action registered as a user task's `completion_action`, `deadline_action` or `wait_action` must read the envelope. `cut-v0-release` carries the release-note line #29 asked for on kartaladev/wrkflw#48.
- **Persisted format:** none on main. Action inputs are not journaled. Once `persist-pending-commands` lands, a pending `InvokeAction` row carries this envelope as its opaque input map, and a row written before this change re-drives with the old flat input. That change's rows are pre-release, so no migration is owed (design, coupling).
- **Default security posture:** unchanged. The envelope carries the variables the action already received, plus the actor the engine already holds.
- **Consumer go.mod:** unchanged.

Out of scope:
- The `update` kind, `previous` and `applied` values: `add-user-task-updates` (#41 part 2). This change ships the fields; they stay empty until that change fills them.
- The `entry` kind and its key `<task_id>:entry`: `add-user-task-entry-action` (kartaladev/wrkflw#121).
- Any change to `action.Typed` itself: `harden-typed-action-input`, which owns the `typed-action-input` capability.
- Reserved engine variable keys such as `_idempotencyKey`: `reserve-engine-variable-keys`. The envelope's keys live outside the variables map, so the two cannot collide.

Decisions this change cannot be fully specified without (design, Open Questions):
- **Q1.** The `idempotency_key` of the completion, deadline and reminder kinds. #29 fixed it as `task_id:seq` with the update table's seq, which these kinds do not have. #119 set `<task_id>:entry` for the entry kind. Recommended: `<task_id>:completion`, `<task_id>:deadline`, and `<task_id>:reminder:<n>`, where n counts the reminder's firings. The key-format rows in the specs are marked *pending Q1*. The rest does not depend on it.

**Coordination**
- Size: M
- Depends on: nothing
- Blocks: add-user-task-updates, add-user-task-entry-action, cut-v0-release
- Readiness: human

## Capabilities

### New Capabilities

- `human-task-actions`: what an action a user task invokes receives, and which actions keep the flat variables map.

### Modified Capabilities

None. `process-variables` keeps its service-task requirement unchanged. Its completion-merge requirement is about what reaches the variables, not what an action receives.

## Impact

- `humantask`: new `call.go` (`Call[In]`, `CallKind`, and the kind names with `MarshalText`/`UnmarshalText` that refuse an unknown name), plus the package doc.
- `engine/step_triggers.go` (`parkOnCompletionAction`): it builds the envelope when the node is a `UserTask` and keeps the flat map otherwise.
- `engine/step_timers.go`: the deadline and reminder sites build the envelope for a user-task timer. `engine/step_cancel.go` (`emitFireOnceAction`) gains an input parameter or a sibling, so that boundary actions keep the flat map.
- Tests in `engine` and `runtime` that assert a completion, deadline or reminder action's input.
- `examples/` that register those actions.
- Docs: `humantask` package doc, and root `doc.go`'s action section if it describes action input.
