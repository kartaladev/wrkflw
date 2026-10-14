# Design

## Context

See proposal.md for motivation. The shape of `humantask.Call[In]` and the rule that splits task actions from instance actions are decided by kartaladev/wrkflw#29 (audit A1 and the Decision comment). This change implements that envelope for the three task actions that exist on `main`. The update action, which is the reason the envelope exists, arrives with `add-user-task-updates`.

Measured on `b8576841`:
- `parkOnCompletionAction` (`engine/step_triggers.go:853-871`) builds `InvokeAction{Input: copyVars(s.Variables)}`. It is called by both `handleHumanCompleted` and `handleMessageReceived`, so the user task and the receive task share it.
- `emitFireOnceAction` (`engine/step_cancel.go:120-127`) builds the fire-and-forget input for deadline (`step_timers.go:81`), reminder (`:214`), timer-boundary (`step_boundaries.go:164`) and error-boundary (`step_errors.go:214`) actions.
- `model.DeadlineOf` and `model.WaitActionOf` (`definition/model/accessors.go:28,41`) apply to every activity and to intermediate catch events, not only to user tasks.
- The completion merge (`mergeVars(s, t.Output)`) runs at `step_triggers.go:986`, before the completion action is parked at `:1012`. So the variables the action sees today already include the output.

## Goals / Non-Goals

**Goals:**
- One envelope type that every task-lifecycle action of a user task receives, so that `action.Typed` with `WithStrictInput` works on all of them.
- No change to any instance action's input, and no change to any action's failure handling.

**Non-Goals:**
- Filling `update`, `previous` and `applied`. They are part of `add-user-task-updates`, which adds the update kind. Here they are always empty.
- Making the receive task's completion action an envelope. It is not a task action under #29's rule, because no `HumanTask` exists.

## Decisions

### D1. Choose the envelope by node kind at the call site, not by helper

`parkOnCompletionAction` and `emitFireOnceAction` are shared with non-task callers. Each call site that acts for a user task passes the input it built: an envelope when the resolved node is an `activity.UserTask` with a task record, and the flat map otherwise. The helpers take the input as a parameter, so that no helper guesses.

- Rejected: switching the helpers wholesale to the envelope. That would wrap receive-task, boundary and error-boundary actions, which #29 A1 assigns to the flat-map convention (instance data).
- Rejected: deciding by "the node has a task record" alone. A deadline on a receive task has no task record, but the explicit kind check states the rule #29 wrote ("every action a user task invokes").

Evidence: the five call sites and two shared helpers above, measured on `b8576841`.

### D2. Contents of `input` for each kind

- `completion`: the completion's submitted output (`HumanCompleted.Output`), copied. An absent output is `{}`. #29 says `input` is "the assembled output". Until `add-user-task-updates` assembles update values, the assembled output is the submitted output.
- `deadline` and `reminder`: `{}`. No submission exists. This follows #119's decided entry envelope (`Input: {}` when there is no submission), so every kind without a submission has the same shape.
- `variables`: a copy of the instance variables at the moment the command is emitted. For completion this is after the merge, which is what the action sees today.
- `applied`: `{}` (not `null`), so a strict typed decoder sees a stable type. `previous` and `update` are omitted.

Rejected: putting the variables into `input` for the timer kinds. That would make `input` mean two different things across kinds.

### D3. Kinds are names, with a refusing decoder

`CallKind` encodes as text (`completion`, `deadline`, `reminder`). `UnmarshalText` refuses any other value with `humantask.ErrUnknownCallKind`. This follows the project rule that an unknown value never decays to a zero value, and the #25 name-encoding rule (kartaladev/wrkflw#25, decided). The new exported sentinel is `humantask.ErrUnknownCallKind`.

### D4. The envelope is built once, by one helper

`taskCall(s, kind, task, actor, input)` in `engine` builds every envelope. #119 already writes its entry action against this helper (`taskCall(c.s, humantask.CallEntry, ...)`), and `add-user-task-updates` extends it with `update`, `previous` and `applied`. The `variables` copy is deep, as `copyVars` is today. The envelope is converted to `map[string]any` before it is put on `InvokeAction.Input`, because the command's input type stays a map (the `typed-action-input` capability's purpose: the engine and persistence only ever see the map).

### Cross-change coupling

- **`add-user-task-updates`** depends on this change. It adds `CallKind` `update` and fills `update`, `previous` and `applied`. It also replaces the completion `input` with the assembled update values when the task declares updates.
- **`add-user-task-entry-action`** (kartaladev/wrkflw#121) depends on this change for `Call`, `taskCall` and `CallKind`, and adds `entry`.
- **`persist-pending-commands`** persists `InvokeAction` inputs as pending-command rows. A row written by an earlier build re-drives with the flat map. Both changes are pre-release, so this is a note for `cut-v0-release`, not a migration. The idempotency requirement here depends on that change's "same input on re-drive" property.
- **`harden-typed-action-input`** owns `typed-action-input` and edits `action/typed.go`. This change adds no requirement there. It only relies on `WithStrictInput` refusing unknown top-level keys.
- **`replace-casbin-with-composed-deciders`** (#119) changes `authz.Actor` (one `Role`, `OnBehalf`). The envelope's `actor` field is an `authz.Actor`, so it carries whatever shape is current. Landing order between the two does not matter.
- **`cut-v0-release`** carries the release-note line for the breaking input change.

## Risks / Trade-offs

- [Consumers' completion, deadline and reminder actions break silently if they decode leniently, because missing keys become zero values] → The release note states the change. The `humantask` doc shows the strict form. The spec pins that a strict flat-struct action fails loudly with `ErrDecodeInput` naming `kind`.
- [Two conventions (task vs instance) is one more thing to learn] → The rule is one sentence in the docs, and #29 decided it explicitly over the six-shape status quo.
- [Envelope size: `variables` is the whole map, as today, plus a few IDs] → No growth beyond today's input.

## Migration Plan

Consumer code: change each user-task completion, deadline and reminder action to read `humantask.Call[X]` (or the map keys `input` and `variables`). No data migration is needed. Rollback is reverting the commit, because nothing persisted depends on the shape.

## Open Questions

- **Q1. `idempotency_key` for the completion, deadline and reminder kinds.** #29 wrote `task_id:seq`, where seq is the task-update table's sequence, and these kinds have no such row. #119 decided `<task_id>:entry` for its once-per-task kind. A reminder fires repeatedly, so it needs a per-firing discriminator. The key must also be identical when the same command is re-driven (`persist-pending-commands`).
  - (a) `<task_id>:completion` and `<task_id>:deadline`, plus `<task_id>:reminder:<n>`, where n is a firing counter kept on the reminder's timer record in the snapshot. Deterministic, readable, and in the style of #119. The cost is one integer on a timer record (a snapshot field, which is additive).
  - (b) As (a), but the reminder uses the firing's scheduled time (`<task_id>:reminder:<RFC3339Nano>`). No new state. The key depends on the scheduler delivering the same scheduled time on redelivery, which has not been measured.
  - (c) The engine command ID (`<instance_id>:<command_id>`) for every non-update kind. Unique, with no new state. It does not name the kind, and the key format then differs from the update and entry kinds.

  **Recommendation: (a).** The specs' key-format rows are marked *pending Q1*. The stability and distinctness rows hold under every option.
