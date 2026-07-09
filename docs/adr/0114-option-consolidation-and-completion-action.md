# 0114. Activity/event option-naming consolidation, deadline/wait split, and completion-action

- Status: Accepted
- Date: 2026-07-09

## Context

The activity (`definition/activity`) and event (`definition/event`) option surfaces grew
organically and, by the time Phases 1-2 of the prior activity-options program shipped
(type-safe per-kind options, ADR-0113; input validation, ADR-0110-0112), exhibited three
inconsistencies this decision resolves together rather than in separate passes:

1. **No `WithXxxAction` family.** Pure, single-argument, "name an action" options are named
   inconsistently — `WithCompensation`, `WithCancelHandler`, `WithActionName` — and there is no
   option at all for running an action on a UserTask/ReceiveTask's *completion*, even though the
   engine's completion handlers (`handleHumanCompleted`, `handleMessageReceived` in
   `engine/step_triggers.go`) already merge vars and advance a token synchronously, and the
   existing service-task action round-trip (`InvokeAction`/`ActionCompleted`, park on
   `TokenWaitingCommand`) is directly reusable for a post-completion hook.
2. **Compound waiter options bundle a mandatory waiter with an optional action.**
   `WithDeadline(t, flow, action)` forces callers to supply an action even when only a
   breach-flow is wanted. The in-wait recurring action option is named `WithWaitReminder` even
   though a prior change (in-wait reminders generalization) already broadened it beyond literal
   reminders to arbitrary in-wait actions — the name no longer matches the behaviour.
3. **Event-side waiter/message/signal options carry a redundant `Catch` prefix**
   (`WithCatchDeadline`, `WithCatchWaitReminder`, `WithCatchMessage`, `WithCatchSignal`) and are
   split per event kind (`WithStartMessage`/`WithCatchMessage`/`WithBoundaryMessage`,
   `WithStartSignal`/`WithCatchSignal`/`WithBoundarySignal`), diverging from the activity side and
   from the existing multi-kind precedent `event.WithName`.

This module is pre-1.0 (see ADR-0113, ADR-0083/0084, and the "constructor conventions" and
"scoped action catalog" precedents), and the established convention on breaking renames is:
hard-rename, no deprecated aliases, accept the wire/YAML break, document a migration note. A
read-only architecture assessment against `main` (`db9e08b`) confirmed every attach point this
decision relies on is present, and that the recent input-validation redesign (validation is now
a runtime pre-`Step` gate) leaves the completion-action mechanics untouched: a completion action
runs after validation for free.

The full specification, workstream-by-workstream rationale, and phased execution plan are
recorded in `docs/specs/2026-07-09-option-consolidation-and-completion-action.md`; this ADR
records the decisions, not the plan. ADR-0109 remains reserved for the separate, unrelated
`ReverseInstance` track.

## Decision

We will consolidate the activity/event option surface and add a completion-action hook, as one
coordinated program (six workstreams, executed as Phases A-F on one branch; this ADR is written
during Phase A and amended if a later phase surfaces a new decision):

**1. Establish a `WithXxxAction` family for pure, single-argument, name-an-action options.**
Every option whose only job is to attach a catalog action name follows this shape:

| Current | New | Kinds | Backing field |
|---|---|---|---|
| `WithCompensation(a)` | `WithCompensateAction(a)` | all activity | `CompensateAction` (renamed from `CompensationAction`) |
| `WithCancelHandler(a)` | `WithCancelAction(a)` | all activity | `CancelAction` (renamed from `CancelHandler`) |
| `WithActionName(a)` | `WithTaskAction(a)` | ServiceTask + BusinessRule | `TaskAction.Action` (unchanged) |
| — (new) | `WithCompletionAction(a)` | UserTask + ReceiveTask | `CompletionAction` (new) |
| — (new, see #2) | `WithDeadlineAction(a)` | all activity + catch | `DeadlineAction` (unchanged) |

`WithCompensateAction`/`WithCancelAction` carry field and wire/YAML key renames
(`compensationAction`→`compensateAction`, `cancelHandler`→`cancelAction`); all others keep their
existing field/wire names.

**2. Split the compound deadline option into a mandatory waiter and an optional action.**
`activity.WithDeadline(t, flow, action)` and `event.WithCatchDeadline(t, flow, action)` are
replaced by `WithWaitDeadline(t schedule.TriggerSpec, flow string)` (sets `DeadlineTimer` +
`DeadlineFlow`) plus the optional `WithDeadlineAction(action string)` from #1 (sets
`DeadlineAction`). `WithWaitDeadline` enforces **fire-once** at `Build`: a trigger for which
`t.Recurring()` is true is rejected with a new sentinel error, `ErrDeadlineTriggerRecurring`
(prefix `workflow-<pkg>:`, per the project's error-sentinel convention). Field and wire keys
(`DeadlineTimer`/`DeadlineFlow`/`DeadlineAction`, wire `deadlineTrigger`/`deadlineFlow`/
`deadlineAction`) are unchanged — only the option shape and the Build-time enforcement change.

**3. Generalize the wait-action option and rename its backing field to match.**
`WithWaitReminder`/`WithCatchWaitReminder` are replaced by a single `WithWaitAction(t
schedule.TriggerSpec, action string)` on both activity (dual-kind
`interface { UserTaskOption; ReceiveTaskOption }`) and event (`CatchOption`), accepting either a
one-shot or recurring trigger — no cadence restriction. The backing fields rename:
`WaitFields.ReminderEvery`→`WaitEvery`, `ReminderAction`→`WaitAction`; the carrier method
`reminder()`→`waitAction()` and accessor `ReminderOf`→`WaitActionOf` rename with their call sites
(`engine`'s `armWaitReminder` and siblings). Wire keys rename `reminderTrigger`→`waitTrigger`,
`reminderAction`→`waitAction`, `reminderEvery`→`waitEvery`. The deferred "wait-schedule exceeds
deadline" advisory warning remains out of scope (no advisory-warning channel exists post
ADR-0113; deferred to a future purpose-built mechanism).

**4. Consolidate event message/signal options into multi-kind options.**
`WithStartMessage`/`WithCatchMessage`/`WithBoundaryMessage` become one
`WithMessageCorrelator(msg, key string) interface { StartOption; CatchOption; BoundaryOption }`
(signature unchanged; each kind sets its own `MessageName`+`CorrelationKey`).
`WithStartSignal`/`WithCatchSignal`/`WithBoundarySignal` (listen side) become one
`WithSignalName(name string) interface { StartOption; CatchOption; BoundaryOption }`. The emit-side
`WithThrowSignal(name)` renames to `WithThrowSignalName(name)` but stays a distinct `ThrowOption` —
it sets the *emitted* signal on `IntermediateThrowEvent`, a different mechanism from the listen
side, so it is not folded into `WithSignalName`. Primary-trigger timer options
(`WithCatchTimer`/`WithStartTimer`/`WithBoundaryTimer`) are unchanged.

**5. Remove the inline-action path entirely.**
`activity.WithAction`/`activity.WithActionFunc`, the `inlineActionOpt`/`actionFunc` machinery,
`model.TaskAction.Inline`, `InvokeAction.Inline`, the inline-vs-name precedence in
`runtime/resolve_action.go` and `engine/main_action.go`, and the inline-vs-name conflict
validation in `definition/model/builder.go`/`definition/activity/activity.go` are all deleted.
Every action resolves by catalog name only, via `WithTaskAction` (and the other
`WithXxxAction` options). Consumers using node-local closures (`processtest/harness.go`,
affected tests/examples) migrate to registering the action in a catalog and referencing it by
name. **Rationale**: definitions become fully serializable and portable — a serialized/
deserialized definition can never carry a non-serializable closure — at the cost of consumers
losing the inline-closure shortcut.

**6. Add the completion-action, reusing the existing action round-trip — no new token state.**
`activity.WithCompletionAction(name string) interface { UserTaskOption; ReceiveTaskOption }` sets
`model.ActivityFields.CompletionAction` (wire `NodeWire.CompletionAction`
`json:"completionAction"`, YAML `nodeYAML.CompletionAction` `yaml:"completionAction"`, decode-only
per existing YAML convention), read via a new `engine.completionActionOf(model.Node) string`
accessor mirroring `compensationActionOf`. Valid only on UserTask and ReceiveTask — the two node
kinds with an external completion trigger; ServiceTask/BusinessRule already complete via their
own action, and other kinds have no completion trigger.

Mechanically, `handleHumanCompleted` and `handleMessageReceived` (`engine/step_triggers.go`), in
each case *after* `mergeVars(output)` and *after* the existing timer/boundary-arm cancellation:

- if `completionActionOf(node) == ""`, behave exactly as today (advance the single outgoing flow,
  `drive`);
- otherwise, emit `InvokeAction{CommandID: cmdID, Name: completionAction, Input: copyVars(merged
  vars)}`, park the token (`tok.State = TokenWaitingCommand; tok.AwaitCommand = cmdID`), and
  return **without** advancing. The existing `handleActionCompleted` handler — unchanged — finds
  the token by `CommandID` on the resulting `ActionCompleted`, merges the action's return vars,
  advances, and drives.

**Failure semantics are not new**: because the parked token is indistinguishable in kind from a
service-task action invocation, the host node's `RetryPolicy` governs completion-action retries,
and a terminal failure raises an admin-resumable incident, or — if an error boundary is attached
to the host node — routes there instead. A node with no retry policy and no error boundary simply
fails the instance on a terminal completion-action failure, exactly as any other unhandled action
failure does today. As with compensation/deadline/wait actions, a completion action resolves
against the root definition's scoped catalog plus the global catalog, not against nested scoped
catalogs. `WithCompletionAction` is distinct from the already-shipped
`WithCompletionValidation` (an input-validation gate evaluated *before* completion is accepted);
the two are documented side by side in godoc to avoid confusion — one gates the completion input,
the other runs an action after it is accepted.

All renames are **hard renames** — no deprecated aliases are kept, matching the project's prior
rename convention (e.g. ADR-0064, ADR-0066, ADR-0087). Renames land across Phases B-F of the
execution plan in `docs/specs/2026-07-09-option-consolidation-and-completion-action.md`; Phase A
(this ADR, plus the completion-action itself) is the only phase introducing new behaviour —
Phases B-F are mechanical renames/removals gated by the compiler and existing tests.

## Consequences

- **Positive.** One coherent naming rule (`WithXxxAction`) for every pure action-attachment
  option, extended uniformly to the new completion-action.
- **Positive.** The deadline waiter is no longer forced to carry an action it doesn't need;
  `WithWaitDeadline` alone is a valid, minimal breach-flow waiter.
- **Positive.** Activity- and event-side waiter naming becomes symmetric (`WithWaitAction` on
  both), and the field name (`WaitAction`/`WaitEvery`) finally matches the generalized behaviour
  instead of the stale "reminder" name.
- **Positive.** Event message/signal options collapse from nine per-kind setters to three
  multi-kind ones (`WithMessageCorrelator`, `WithSignalName`, `WithThrowSignalName`), mirroring
  the existing `event.WithName` precedent.
- **Positive.** Definitions are fully serializable: no node can carry a non-serializable inline
  closure, which matters for the library's core promise of portable, storable process
  definitions.
- **Positive.** The completion-action adds no new token state and no new engine machinery beyond
  two call sites in `step_triggers.go` — it reuses the same `InvokeAction`/`ActionCompleted`
  round-trip, `TokenWaitingCommand` park state, and retry/incident/error-boundary handling that
  service-task actions already exercise, keeping the engine's action-failure model singular.
- **Breaking (accepted, pre-1.0).** Public option renames and removals:
  `WithCompensation`→`WithCompensateAction`, `WithCancelHandler`→`WithCancelAction`,
  `WithActionName`→`WithTaskAction`, `WithDeadline`/`WithCatchDeadline` split into
  `WithWaitDeadline`+`WithDeadlineAction`, `WithWaitReminder`/`WithCatchWaitReminder`→
  `WithWaitAction`, `WithStartMessage`/`WithCatchMessage`/`WithBoundaryMessage`→
  `WithMessageCorrelator`, `WithStartSignal`/`WithCatchSignal`/`WithBoundarySignal`→
  `WithSignalName`, `WithThrowSignal`→`WithThrowSignalName`; and outright removal of
  `WithAction`/`WithActionFunc` (no replacement — catalog registration is the only path).
- **Breaking (accepted, pre-1.0).** Wire/YAML key renames: `compensationAction`→
  `compensateAction`, `cancelHandler`→`cancelAction`, `reminderAction`/`reminderTrigger`/
  `reminderEvery`→`waitAction`/`waitTrigger`/`waitEvery`; plus the new `completionAction` key.
  Persisted definitions serialized with the old keys will not decode after this change — this is
  acceptable pre-1.0 per established project convention, but requires a CHANGELOG entry and a
  migration note (re-serialize/re-author affected definitions with the new keys).
  Deadline keys (`deadlineTrigger`/`deadlineFlow`/`deadlineAction`), `signalName`, `messageName`,
  and `correlationKey` are **not** wire-renamed — only the Go option surface changes there.
- **Neutral.** Consumers relying on inline node-local action closures (most visibly
  `processtest/harness.go`) must migrate to named catalog registrations; this is a one-time
  migration cost in exchange for fully serializable definitions.
- **Neutral.** The "wait schedule may exceed the deadline" advisory warning stays unimplemented;
  a future purpose-built advisory mechanism (foreshadowed by ADR-0113's removal of
  `definition.Lint`) is needed before it can return, and must not resurrect the deleted Lint
  machinery for this one rule.
- **Neutral.** ADR-0109 remains reserved for the unrelated `ReverseInstance` track and is
  unaffected by this decision.
