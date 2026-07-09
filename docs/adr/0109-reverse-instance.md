# 0109. `ProcessDriver.ReverseInstance` — reverse/rollback facade

- Status: Accepted
- Date: 2026-07-09

## Context

Rolling back a running instance previously required a consumer to hand-build an
engine-internal trigger directly — `driver.ApplyTrigger(ctx, def, id,
engine.NewCompensateRequested(clk.Now(), toNode))`, as done in
`examples/scenarios/compensation_saga/main.go`. That leaked
`engine.NewCompensateRequested` into consumer code, exposed the raw `toNode
string` (with a magic `""` meaning "compensate everything"), and offered no
ergonomic, safe surface, unlike the sibling facades `DeliverMessage`,
`BroadcastSignal`, `CancelInstance`, and `ResolveIncident`.

Two independently useful rollback modes exist and needed a facade:

- **Partial rollback** (`CompensateRequested{ToNode: "X"}`): compensates the
  records completed after node X (exclusive, reverse order), drops a token at
  X, and resumes `StatusRunning`. This machinery already existed.
- **Full rollback** (`CompensateRequested{ToNode: ""}`): compensates every
  record and, historically, always terminated the instance
  (`StatusTerminated`) — this path is shared with the cancel/error flow, so a
  full compensation had no way to resume the instance instead of ending it.

Compensation records are appended per execution (`recordCompensation`,
`engine/state.go`), never keyed by node — a node visited N times in a loop
yields N records, and a reverse walk replays all N **LIFO** (newest-visit
first, walked from the end of the slice backward). This shape did not need to
change for this feature; it was already correct for cyclic processes, just
untested.

Since the design doc (`docs/specs/2026-07-08-reverse-instance-design.md`) was
first written, the option-consolidation and completion-action work (ADR-0114)
landed, changing how UserTask/ReceiveTask compensation is recorded: a
UserTask/ReceiveTask now records a compensation entry only when its
**completion action** runs (`parkOnCompletionAction` parks the token;
`handleActionCompleted` records `compensateActionOf(node)` against the token
still at that node). A Build guard,
`definition.ErrCompensateActionWithoutForwardAction`
(`definition/model/validate.go`), rejects any UserTask/ReceiveTask that
carries a compensate action but no completion action — "if you didn't DO it,
you can't UNDO it." This constrains how a reversible UserTask/ReceiveTask must
be authored, and is exercised by this feature's tests and its example.

The full analysis, semantics table, and file-by-file plan are recorded in
`docs/specs/2026-07-09-reverse-instance-design.md`; this ADR records the
decisions.

## Decision

We added `ProcessDriver.ReverseInstance` (`runtime/processdriver_reverse.go`)
as a facade that rolls a Running or Compensating instance **backward without
terminating it** — termination remains `CancelInstance`'s job:

```go
func (driver *ProcessDriver) ReverseInstance(ctx context.Context, def *model.ProcessDefinition,
	instanceID string, opts ...ReverseOption) (engine.InstanceState, error)

type ReverseOption func(*reverseConfig)
func WithFullReverse() ReverseOption
func WithTargetNode(nodeID string) ReverseOption
```

**1. Functional options, introduced at the facade layer for the first time.**
`ReverseInstance` is the only facade using `opts ...ReverseOption`; the four
siblings (`CancelInstance`, `DeliverMessage`, `BroadcastSignal`,
`ResolveIncident`) are all positional. Options fit here because
`ReverseInstance` has two mutually exclusive modes plus a sensible default —
a positional signature would force every caller to pass a mode flag and a
node ID (mostly unused), where options let the common case
(`ReverseInstance(ctx, def, id)`) read cleanly while the less common
target-node case stays explicit and self-documenting
(`WithTargetNode("review")`). This is a one-off shape decision, not a new
project-wide facade convention — the siblings keep their positional
signatures unchanged.

**2. Default (no option) = full reverse.** `ReverseInstance(ctx, def, id)`
with zero options behaves exactly as `WithFullReverse()`: `reverseConfig`'s
zero value has `full == false`, but the facade resolves "not targeted" to the
full-reverse dispatch path regardless of `cfg.full`, so omitting both options
and passing `WithFullReverse()` explicitly are equivalent.

**3. Mutual exclusion and the empty-target guard.** `WithFullReverse()` +
`WithTargetNode(...)` together return a `workflow-runtime:`-prefixed error
before any state change. Separately, `WithTargetNode("")` (empty node ID) is
also rejected with a `workflow-runtime:` error — this was caught in review as
a defect: an empty target node is exactly the sentinel
`CompensateRequested.ToNode == ""` already uses to mean "compensate
everything and terminate" (the admin full-compensation trigger). Without the
guard, `WithTargetNode("")` would silently collapse onto that trigger and
terminate the instance instead of reversing it — the opposite of what
`ReverseInstance` promises never to do.

**4. Terminal-instance guard — deliberately deviates from `CancelInstance`.**
`ReverseInstance` rejects an instance whose status is `StatusCompleted`,
`StatusFailed`, or `StatusTerminated` with a `workflow-runtime:` error before
touching any state; only `StatusRunning` and `StatusCompensating` are
reversible. This is a deliberate deviation from `CancelInstance`, which
treats re-cancelling an already-terminal instance as an idempotent no-op.
Reversing a terminal instance is judged a caller mistake that should fail
loudly rather than silently no-op, because — unlike cancel — a successful
reverse call mutates the instance back into `StatusRunning`; a silent no-op
here would leave the caller believing the instance was rewound when it was
not touched. (The `reverse_rollback` example deliberately stops one decision
short of the definition's `end` node and parks the instance mid-flow, because
a completed instance cannot be reversed.)

**5. The engine enhancement — gated purely on new cursor fields.** The
partial-rollback mode (`WithTargetNode`) needed no engine change: it dispatches
`engine.NewCompensateRequested(at, targetNode)`, reusing the existing
resume-at-X path. The full-reverse mode needed new engine behavior, because a
full compensation walk previously always terminated. Three additions, all in
`engine/`:

- `InstanceState.StartVariables map[string]any` (`engine/state.go`) — an
  immutable-by-convention deep copy of the instance's variables, captured
  once in `handleStartInstance` (`engine/step_triggers.go`) right after the
  start trigger's vars are merged. It participates in serialization
  (persisted with the instance) and is deep-copied independently in
  `cloneState`/`InstanceState.Clone`, so a clone's `StartVariables` never
  aliases the original's map.
- `CompensateRequested.ReverseNode string` / `ResetVars bool`
  (`engine/trigger.go`), plus a new constructor,
  `NewReverseToStart(at time.Time, startNode string) CompensateRequested`,
  which sets `ReverseNode: startNode, ResetVars: true` (`ToNode` stays `""`).
  `NewCompensateRequested(at, toNode)` is unchanged and leaves the new fields
  at their zero values. These fields propagate onto the runtime
  `compensationCursor` (`ReverseNode`/`ReverseResetVars`,
  `engine/state.go`) through `beginCompensation` and
  `stepCompensationAdvance`, mirroring — but kept strictly distinct from —
  the pre-existing throw-walk cursor fields `ResumeNode`/`ResumeScope`
  (ADR-0039), so the throw-resume branch in `stepCompensationFinish` is never
  mis-triggered by a reverse walk.
- A new finish branch in `stepCompensationFinish`
  (`engine/step_compensation.go`): when the walk is a full walk (`toNode ==
  ""` and `resumeNode == ""`, i.e. no throw-walk in progress) and
  `reverseNode != ""`, it clears the scope's compensation records (as a
  terminal full rollback already does), sets `Status = StatusRunning`, clears
  `EndedAt` (restoring the Running invariant — the primary use case reverses
  an already-completed-looking instance whose `EndedAt` was stamped, so it
  must be cleared, not left stale), resets `Variables = copyVars(s.StartVariables)`
  when `reverseResetVars` is true, places a token at `reverseNode`, and
  drives forward — instead of terminating. `History` is deliberately retained
  (not reset): re-execution from `reverseNode` appends fresh visits on top of
  it, so the full run history, including the reversed segment, stays intact.
  This branch is reached **only** when `reverseNode != ""`; every existing
  caller of `beginCompensation` (cancel, unhandled-error, and the admin
  `CompensateRequested{ToNode: ""}` path) passes `reverseNode = "", 
  reverseResetVars = false`, so the pre-existing terminate branch immediately
  below it is unchanged in behavior for every caller except the new
  full-reverse one.
- Start-node discovery happens in the facade, not the engine: it resolves
  `def.StartNodes()`; zero or more than one start node returns a
  `workflow-runtime:` error before dispatching (mirroring
  `handleStartInstance`'s own `len(starts) != 1` guard).

The full-reverse resume-from-start semantic means the instance re-runs the
process from the top with a fresh slate: a token dropped at a start event has
no wait semantics of its own, so `drive` carries it forward through the
definition exactly as `handleStartInstance` would for a brand-new instance,
just under the instance's existing ID and retained history.

**6. The operation trio is now clean and non-overlapping:**

| Operation | Compensates? | End state |
|---|---|---|
| `CancelInstance` | yes (all) | `StatusTerminated` |
| `ReverseInstance` + `WithFullReverse()` (default) | yes (all), LIFO | `StatusRunning` — fresh at start, vars reset |
| `ReverseInstance` + `WithTargetNode(X)` | yes, back to X (exclusive), LIFO | `StatusRunning` — at X, vars kept |

**7. Variable semantics — asymmetric between the two reverse modes, by
design.**

- **Full reverse resets** `Variables` to `StartVariables` (the
  start-of-instance snapshot). This matches "fresh slate at start": a
  full reverse is meant to look like the instance never ran.
- **Target reverse keeps the current live `Variables` as-is.** It does
  **not** restore variables to what they were at the target node's own
  start. The instance resumes at node X carrying whatever the last-running
  branch had accumulated (e.g. a rejection flag set by the most recent loop
  iteration survives the reverse).
- Each `CompensationRecord.Input` (`engine/state.go`) does persist a
  per-visit variable snapshot — "the instance variables at the moment the
  activity was invoked" — but that snapshot's only consumer is the
  compensation `InvokeAction.Input` for that specific compensate action call
  (`copyVars(rec.Input)` in `beginCompensation` /
  `stepCompensationAdvance`). It is never written back to
  `InstanceState.Variables`. A target reverse therefore does not use these
  per-node snapshots to restore instance state — only to feed each
  compensate action its original input.

**8. Item-4 interaction.** UserTask/ReceiveTask compensation flows exclusively
through the completion-action round-trip introduced by ADR-0114:
`parkOnCompletionAction` parks the token when the node's completion action is
invoked, and `handleActionCompleted` records the node's compensate action
against that still-parked token once the completion action completes. The
Build guard `ErrCompensateActionWithoutForwardAction` forces any reversible
UserTask/ReceiveTask to carry **both** a completion action and a compensate
action — a UserTask that only parks, without its completion action ever
being driven, produces no compensation record and therefore has nothing to
reverse. The `reverse_rollback` example and the engine's Item-4 interaction
test both pair every reversible UserTask with a completion action and a
compensate action to satisfy this guard.

**9. Cycles.** Per-visit compensation records already replay LIFO
(newest-first) regardless of mode — no new engine logic was needed for
cyclic processes. `WithFullReverse()` unwinds every recorded iteration back
to start; `WithTargetNode("X")` targets X's **most-recent** visit, because
`beginCompensation` locates `toNode` by scanning records for the
highest-indexed match on `NodeID`. For a node visited N times in a loop, this
is ambiguous by design — there is no way to address a specific earlier visit
by node ID alone — and is documented as such in `WithTargetNode`'s godoc.

## Consequences

- **Positive.** Consumers no longer need to know about
  `engine.CompensateRequested`, `engine.NewCompensateRequested`, or the
  `toNode == ""` termination sentinel to roll back a running instance; the
  facade is safe and self-documenting (`WithFullReverse()` /
  `WithTargetNode(id)`).
- **Positive.** The operation trio (`CancelInstance` / `ReverseInstance` +
  `WithFullReverse` / `ReverseInstance` + `WithTargetNode`) gives every
  rollback need — terminate, restart fresh, or rewind to a checkpoint — a
  single, discoverable, non-overlapping entry point.
- **Positive.** The engine change is minimal and safely gated: a single new
  finish branch keyed entirely on `reverseNode != ""`, with every existing
  caller of `beginCompensation` passing the zero value for the new
  parameters. The cancel/error full-compensation terminate path is
  byte-for-byte unchanged for those callers
  (`TestFullCompensation_WithoutReverse_StillTerminates` and the existing
  cancel/compensation test suites cover this regression).
- **Positive.** Cyclic (loop/reject/re-escalate) processes reverse correctly
  under both modes with no additional engine logic — this was already true
  of the underlying LIFO replay, and is now covered by a regression test
  (3× loop → 3 LIFO compensations) that previously did not exist.
- **Neutral / by-design asymmetry.** Full reverse resets variables to the
  start-of-instance snapshot; target reverse keeps the current live
  variables and does not restore the target node's own start-of-visit
  state, even though `CompensationRecord.Input` holds enough data per visit
  to make that possible. Callers must understand this asymmetry: a target
  reverse is a checkpoint-and-continue with current data, not a full replay
  to that point in time.
- **Deferred (user-approved, 2026-07-09).** "Restore node-start variables on
  target reverse" is a known, deliberately deferred enhancement — the data
  (`CompensationRecord.Input`) already exists to support it, but adopting it
  changes the documented target-reverse variable semantics in point 7 above
  and therefore needs its own future ADR (next free number: 0116), not a
  silent behavior change bundled into this one.
- **Neutral.** `ReverseInstance` is the only facade using functional options;
  this is scoped to its two-mode-plus-default shape and is not adopted as a
  new blanket convention for the facade layer — `CancelInstance`,
  `DeliverMessage`, `BroadcastSignal`, and `ResolveIncident` keep their
  positional signatures.
- **Neutral.** `WithTargetNode`'s "most-recent visit" resolution for a node
  visited multiple times in a loop is documented as ambiguous-by-design in
  its godoc rather than solved with a visit-index parameter; addressing it
  (if ever needed) is left to a future enhancement.

## References

- Spec: `docs/specs/2026-07-09-reverse-instance-design.md`
- `runtime/processdriver_reverse.go`, `engine/trigger.go`, `engine/state.go`,
  `engine/step_compensation.go`, `engine/step_triggers.go`
- `examples/scenarios/reverse_rollback/main.go`
- ADR-0039 (compensation throw-walk resume), ADR-0114 (completion-action /
  `ErrCompensateActionWithoutForwardAction`)
