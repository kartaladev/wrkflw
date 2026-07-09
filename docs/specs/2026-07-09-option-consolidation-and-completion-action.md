# Option-naming consolidation, deadline/wait split & completion-action

Date: 2026-07-09
Status: Design — approved for planning
Scope: one coordinated program across `definition/activity` + `definition/event` (+ `model`
wire/YAML, `engine`, `runtime`, `processtest`, examples). **ADR-0114.**

This supersedes the narrower Phase 3 of `docs/specs/2026-07-08-activity-options-program.md`
(completion-action only). Phases 1 (type-safe options, ADR-0113) and 2 (validation, ADR-0110/…) of
that program already shipped; this spec is that program's final phase, **widened** by the user into a
full option-naming/structure consolidation.

## Why

The activity/event option surface has grown organically and now mixes three naming/structure
inconsistencies that this pass resolves together (born-consistent, not refactored twice):

1. **No `WithXxxAction` family.** Pure single-action options are named inconsistently
   (`WithCompensation`, `WithCancelHandler`, `WithActionName`) and there is no completion hook at all.
2. **Compound waiter options bundle mandatory + optional concerns.** `WithDeadline(t, flow, action)`
   forces an action even when only a breach-flow is wanted; the in-wait recurring action is misnamed
   `WithWaitReminder` though it was generalized beyond reminders (see the in-wait-reminders
   generalization work).
3. **Event-side waiter options carry a redundant `Catch` prefix** (`WithCatchDeadline`,
   `WithCatchWaitReminder`, `WithCatchMessage`, `WithCatchSignal`) that diverges from the activity
   side and from the message/signal setters on other event kinds.

A verified read-only assessment against `main` (`db9e08b`) confirmed every structural attach point
this design relies on is present and that the completion-action mechanics are unchanged by the recent
input-validation redesign (validation is now a runtime pre-`Step` gate, so a completion action runs
after validation for free).

## Workstreams

Six coordinated workstreams. All renames are **hard renames** (pre-1.0; matches prior rename
convention). Wire/YAML key renames are **breaking for the serialized format** — accepted pre-1.0.

### 1. Completion-action (the original Item 4)

New optional action that runs when a node's **completion** is triggered (human completion or message
receive), receiving the completion input and merging its returned vars — for updating external
business/domain data and/or contributing computed variables.

- **Option:** `activity.WithCompletionAction(name string) interface { UserTaskOption; ReceiveTaskOption }`
  — mirrors the `WithWaitAction` dual-kind shape (`options.go` `reminderOpt` pattern). Valid only on
  UserTask + ReceiveTask (ServiceTask/BusinessRule complete via their own action; other kinds have no
  external completion trigger).
- **Field:** `model.ActivityFields.CompletionAction string`, beside `CompensateAction`/`CancelAction`.
- **Wire/YAML:** `NodeWire.CompletionAction` (`json:"completionAction"`), threaded through
  `PutActivity`/`Activity`; `nodeYAML.CompletionAction` (`yaml:"completionAction"`) + one copy line in
  `fromNodeYAML` (YAML is decode-only).
- **Accessor:** `engine.completionActionOf(model.Node) string`, mirroring `compensationActionOf`
  (`engine/node_accessors.go`).
- **Naming caveat (document in godoc):** distinct from the shipped `WithCompletionValidation`
  (validation gate) — one gates the completion input, the other runs after it.

**Engine mechanics (no new token state — reuses the action round-trip):**

Completion is synchronous today: `handleHumanCompleted` (`engine/step_triggers.go` ~L437) and
`handleMessageReceived` (~L654) do `mergeVars(output)` → advance along the single outgoing flow →
`drive`, and already cancel the node's task timers and boundary arms. The change, in each handler,
**after `mergeVars` and after the existing timer/boundary-arm cancellation**:

1. If `completionActionOf(node) == ""` → behave exactly as today (advance + drive).
2. Else emit `InvokeAction{CommandID: cmdID, Name: completionAction, Input: copyVars(merged vars)}`,
   park the token (`tok.State = TokenWaitingCommand; tok.AwaitCommand = cmdID`), and **return without
   advancing**.
3. On the resulting `ActionCompleted`, the **existing** `handleActionCompleted` (~L42) finds the
   token by `CommandID`, merges the action's return vars, advances, and drives. No change there.

**Failure semantics — reuse existing machinery (no new semantics):** the parked token behaves like a
service-task action, so the node's `RetryPolicy` governs retries; a terminal failure raises an
admin-resumable incident **or** routes to an attached error boundary. `WithRetryPolicy` /
error-boundary on the host node therefore govern the completion action for free.

**Catalog-scope limitation (documented, same as compensation/deadline/wait):** a completion action
resolves against the root definition's scoped catalog + global, not nested scoped catalogs.

### 2. The `WithXxxAction` pure-action family

Establish one naming rule for single-arg, name-an-action options:

| Current | New | Kinds | Field |
|---|---|---|---|
| `WithCompensation(a)` | `WithCompensateAction(a)` | all activity | `CompensateAction` (renamed from `CompensationAction`) |
| `WithCancelHandler(a)` | `WithCancelAction(a)` | all activity | `CancelAction` (renamed from `CancelHandler`) |
| `WithActionName(a)` | `WithTaskAction(a)` | ServiceTask + BusinessRule | `TaskAction.Action` (unchanged) |
| — (new) | `WithCompletionAction(a)` | UserTask + ReceiveTask | `CompletionAction` (new) |
| — (new, WS3) | `WithDeadlineAction(a)` | all activity + catch | `DeadlineAction` (unchanged) |

Field/wire/YAML renames included for `CompensateAction` and `CancelAction` (breaking wire):
`compensationAction`→`compensateAction`, `cancelHandler`→`cancelAction`.

### 3. Deadline split (activity + event)

Split the compound deadline option into a mandatory waiter + an optional action:

- `WithWaitDeadline(t schedule.TriggerSpec, flow string)` — sets `DeadlineTimer` + `DeadlineFlow`.
- `WithDeadlineAction(action string)` — optional; sets `DeadlineAction` (part of WS2's family).
- **Fire-once enforcement:** `WithWaitDeadline`'s trigger MUST be one-shot. Build error
  (`ErrDeadlineTriggerRecurring`, sentinel prefix `workflow-<pkg>:`) if `t.Recurring()` is true
  (`schedule.TriggerSpec.Recurring()`, `trigger.go:97`).
- Replaces `activity.WithDeadline(t, flow, action)` and `event.WithCatchDeadline(t, flow, action)`.
- Fields `DeadlineTimer`/`DeadlineFlow`/`DeadlineAction` keep their (already-good) names; wire keys
  `deadlineTrigger`/`deadlineFlow`/`deadlineAction` unchanged.

### 4. Wait-action rename (activity + event) + field rename

- `WithWaitReminder(t, action)` and `WithCatchWaitReminder(t, action)` → **`WithWaitAction(t
  schedule.TriggerSpec, action string)`** on both packages (activity: dual-kind
  `interface { UserTaskOption; ReceiveTaskOption }`; event: `CatchOption`). Accepts **either** cadence
  (one-shot or recurring) — no hard enforcement.
- **Backing-field rename (breaking wire):** `WaitFields.ReminderEvery`→`WaitEvery`,
  `ReminderAction`→`WaitAction`; carrier method `reminder()`→`waitAction()` and accessors
  `ReminderOf`→`WaitActionOf` (audit all call sites in `engine`/`runtime`, e.g. `armWaitReminder`).
  Wire keys `reminderTrigger`→`waitTrigger`, `reminderAction`→`waitAction`; legacy flat
  `reminderEvery`→`waitEvery` (or drop the legacy flat form — decide in plan).
- **Convenience warning — DROPPED (deferred).** Warning when a wait schedule elapses past the deadline
  is not implemented: the advisory/`Lint` mechanism was removed in ADR-0113 (`Build` has no warning
  channel/logger), and the comparison is only statically decidable for fixed-duration triggers
  (`AfterExpr`/`Cron`/`EveryRandom` resolve at runtime). Deferred to the future purpose-built advisory
  mechanism ADR-0113 foreshadowed.

### 5. Message-correlator + signal-name consolidation (event)

Fold the per-kind message and signal setters into unified multi-kind options (mirrors the existing
multi-kind `event.WithName`):

- **Message:** `WithStartMessage` / `WithCatchMessage` / `WithBoundaryMessage` →
  **`WithMessageCorrelator(msg, key string) interface { StartOption; CatchOption; BoundaryOption }`**
  (each kind sets its own `MessageName`+`CorrelationKey`). Signature `(msg, key)` unchanged.
- **Signal (listen side):** `WithStartSignal` / `WithCatchSignal` / `WithBoundarySignal` →
  **`WithSignalName(name string) interface { StartOption; CatchOption; BoundaryOption }`**.
- **Signal (emit side):** `WithThrowSignal(name)` → `WithThrowSignalName(name)` (kept distinct — it
  is the *emitted* signal on `IntermediateThrowEvent`, a different `ThrowOption` mechanism/semantic).
- `WithCatchTimer`/`WithStartTimer`/`WithBoundaryTimer` (primary triggers, not waiter add-ons) —
  **unchanged**.

### 6. Remove inline actions

Delete the node-local inline-action path entirely; all actions resolve by catalog name via
`WithTaskAction`.

- Remove `activity.WithAction` + `activity.WithActionFunc` + `inlineActionOpt` + `actionFunc`.
- Remove `model.TaskAction.Inline` (and simplify `taskAction()` to return just the name);
  `InvokeAction.Inline` (`engine/command.go`); inline precedence in `runtime/resolve_action.go` and
  the note in `engine/main_action.go`; the inline-vs-name conflict validation in
  `definition/model/builder.go` (+ `definition/activity/activity.go`).
- **Migrate consumers:** `processtest/harness.go` and affected tests/examples switch from inline
  closures to catalog registration (register the action by name, reference via `WithTaskAction`).
- **Trade-off (accepted):** consumers lose the closure shortcut; in return, definitions are fully
  serializable/portable with no non-serializable node-local closures.

## Wire / YAML key changes (breaking, pre-1.0)

| Concern | Old key | New key |
|---|---|---|
| Compensate | `compensationAction` | `compensateAction` |
| Cancel | `cancelHandler` | `cancelAction` |
| Completion (new) | — | `completionAction` |
| Wait action | `reminderAction` / `reminderTrigger` / `reminderEvery` | `waitAction` / `waitTrigger` / `waitEvery` |

Deadline keys (`deadlineTrigger`/`deadlineFlow`/`deadlineAction`), `signalName`, `messageName`,
`correlationKey` are unchanged at the wire level — the renames in WS3/WS5 are option/field-name
changes, not wire-key changes.

## Consequences

- **Positive:** one coherent `WithXxxAction` family; optional-not-mandatory deadline action;
  symmetric activity/event waiter naming; fewer, multi-kind event options; fully serializable
  definitions (no inline closures).
- **Breaking:** public option renames + removals; wire/YAML key renames for compensate/cancel/wait.
  Persisted definitions using the old keys will not decode — acceptable pre-1.0; call out in the
  CHANGELOG + a migration note.
- **Blast radius:** ~10 source files + wire/YAML + engine handlers + `processtest` + examples + tests.
  Every renamed/removed option has call sites in examples/tests that must move in lockstep.

## Execution phases (each an independently-reviewable commit series; TDD strict)

Ordered to keep `main`-gate green at each phase boundary. One program branch.

- **Phase A — completion-action (WS1).** New field/option/wire/YAML/accessor + engine handler
  branches + tests (UserTask success, ReceiveTask success, failure→retry→incident/error-boundary,
  wire/YAML round-trip, boundary-arm cancellation on invoke) + `examples/scenarios/completion_action/`.
  Purely additive — safest first.
- **Phase B — `WithXxxAction` family renames (WS2).** `WithCompensation`→`WithCompensateAction`,
  `WithCancelHandler`→`WithCancelAction`, `WithActionName`→`WithTaskAction` + field/wire/YAML renames
  + all call-site migrations.
- **Phase C — deadline split + fire-once (WS3).** Split option, `WithDeadlineAction`, Build
  enforcement + tests (fire-once rejected; deadline-action optional).
- **Phase D — wait-action rename + field rename (WS4).** Option + `WaitEvery`/`WaitAction` field/wire
  renames + engine `armWaitReminder`/accessor audit + tests.
- **Phase E — event message/signal consolidation (WS5).** Multi-kind `WithMessageCorrelator` +
  `WithSignalName`, `WithThrowSignalName`, call-site migrations.
- **Phase F — remove inline (WS6).** Delete inline path + migrate `processtest`/tests/examples.

Phases B–F are mechanical renames/removals gated by the compiler + existing tests; Phase A is the only
new behaviour. ADR-0114 written in Phase A, amended if later phases surface decisions.

## Verification checklist

- [ ] Each new symbol (`WithCompletionAction`, `WithWaitDeadline`, `WithDeadlineAction`,
      `WithWaitAction`, `WithMessageCorrelator`, `WithSignalName`, `completionActionOf`,
      `ErrDeadlineTriggerRecurring`) has an observable red state before implementation.
- [ ] Completion-action: success (UserTask + ReceiveTask), failure→retry→incident/error-boundary,
      wire+YAML round-trip, boundary-arm cancellation on invoke.
- [ ] `WithWaitDeadline` rejects a `Recurring()` trigger at Build (`ErrDeadlineTriggerRecurring`).
- [ ] `WithDeadlineAction` optional: deadline with flow only (no action) builds and breaches correctly.
- [ ] Wire/YAML round-trip for every renamed key; a definition serialized then deserialized is stable.
- [ ] No remaining references to removed symbols (`WithAction`, `WithActionFunc`, `WithCompensation`,
      `WithCancelHandler`, `WithActionName`, `WithDeadline`, `WithWaitReminder`, `WithCatch*`) —
      `grep` clean across the module including examples + `processtest`.
- [ ] `go build ./...`, `go test ./...`, `golangci-lint run ./...` clean; touched packages ≥ 85% cover.
- [ ] CHANGELOG entry + migration note for the breaking wire/option changes.

## ADR

**ADR-0114** — "Activity/event option-naming consolidation, deadline/wait split, and completion-action."
Records: the `WithXxxAction` family rule; deadline split with fire-once enforcement; wait-action
generalization + field rename; event message/signal multi-kind consolidation; inline-action removal
(full serializability rationale); completion-action mechanics (reuse of the action round-trip, failure
via existing retry/incident/boundary). (0109 remains reserved for the separate ReverseInstance track.)
