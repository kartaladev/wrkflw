# 0116. Target reverse restores the target node's start-of-visit variables

- Status: Accepted
- Date: 2026-07-10

## Context

ADR-0109 introduced `ProcessDriver.ReverseInstance` and its two modes,
`WithFullReverse()` and `WithTargetNode(nodeID)`. Point 7 of that ADR
documented the two modes' variable semantics as deliberately asymmetric:

- Full reverse resets `Variables` to `InstanceState.StartVariables` (the
  start-of-instance snapshot).
- Target reverse **kept the instance's current live `Variables` as-is** — it
  did not restore variables to what they were when the target node was first
  entered, even though the data to do so already existed:
  `CompensationRecord.Input` (`engine/state.go`) persists a per-visit
  snapshot of `InstanceState.Variables` taken the moment each activity was
  invoked, before that activity ran or its output was merged.

ADR-0109's Consequences flagged this as a known gap: "Restore node-start
variables on target reverse" was explicitly deferred to its own ADR — this
one — because adopting it changes the documented target-reverse variable
contract and was judged too large a change to bundle into the original
facade work (`docs/adr/0109-reverse-instance.md`, "Deferred" bullet and the
"Still deferred" note in its Hardening section, which reserved ADR number
0116 for this decision).

The follow-up plan (`docs/plans/2026-07-10-reverse-instance-followups.md`,
"FU#1") picked this up: since `CompensationRecord.Input` for the target
node's own most-recent visit already holds exactly the "variables as they
stood the moment execution first arrived at that node" snapshot, restoring
it is a bounded, well-understood change rather than new machinery.

A second constraint shaped the design: `WithTargetNode` is not the only
caller of the target-reverse engine path. Admin/partial-rollback tooling and
the cancel-preempt flow construct `engine.CompensateRequested{ToNode: "X"}`
directly (via `engine.NewCompensateRequested`), bypassing the facade
entirely, and must keep their existing "resume with current variables"
behavior — an unconditional restore at the engine level would silently
change their contract too.

## Decision

`WithTargetNode(nodeID)` now, by default, restores `Variables` to `nodeID`'s
own start-of-visit snapshot instead of keeping the current live variables.
This is a **breaking change** to the target-reverse variable contract
documented in ADR-0109 point 7.

**1. New opt-in signal, not a behavior change to the existing trigger.**
`engine.CompensateRequested` gains a new field,
`RestoreTargetVars bool` (`engine/trigger.go`), alongside the pre-existing
`ToNode`/`ReverseNode`/`ResetVars`. A new constructor,

```go
func NewReverseToNode(at time.Time, toNode string) CompensateRequested
```

sets `ToNode: toNode, RestoreTargetVars: true`. The pre-existing
`NewCompensateRequested(at, toNode)` is **unchanged** — it leaves
`RestoreTargetVars` at its zero value (`false`) — so every existing direct
caller (admin partial rollback, cancel-preempt) keeps the current-variables
behavior with no code change on their part. `ReverseInstance` +
`WithTargetNode` is the only caller of `NewReverseToNode`
(`runtime/processdriver_reverse.go`).

The three reverse variable-semantics that now exist:

| Trigger | Variable outcome |
|---|---|
| `WithFullReverse()` (→ `NewReverseToStart`) | reset to `StartVariables` |
| `ReverseInstance` + `WithTargetNode(X)` (→ `NewReverseToNode`) | restored to X's start-of-visit snapshot |
| raw `engine.NewCompensateRequested(at, X)` (admin / cancel-preempt) | unchanged — current variables kept |

**2. Shape guard.** `RestoreTargetVars` is meaningless without a target node
to look the snapshot up on, mirroring the pre-existing `ResetVars`-without-
`ReverseNode` guard. `stepCompensateRequested`
(`engine/step_compensation.go`) rejects `RestoreTargetVars && ToNode == ""`
with a `workflow-engine:` error before any state change, ahead of the
state-dependent guards — the same defense-in-depth posture as the
`ResetVars` guard added in ADR-0109's hardening pass. This also closes the
same category of gap: `CompensateRequested` is a public,
directly-constructible struct, so a caller could hand-build
`CompensateRequested{ToNode: "X", RestoreTargetVars: true}` without going
through `NewReverseToNode`; the guard makes the combination well-formed
regardless of construction path.

**3. Engine mechanics.** `RestoreTargetVars` is carried through the walk as
a scalar on `compensationCursor` (`ReverseNode`/`ReverseResetVars`'s
sibling field, `engine/state.go`), propagated by `beginCompensation` and
`stepCompensationAdvance` exactly like the pre-existing cursor fields. The
resolved snapshot itself — a `map[string]any`, not a scalar — is carried
only on the transient `finishPlan.restoreVars`
(`engine/step_compensation.go`), computed in `stepCompensationFinish`'s
partial-rollback branch: when `restoreTargetVars` is true, it looks up the
target node's most-recently-completed compensation record via
`lastCompensationRecordByNode(s.RootCompensations, toNode)` and takes that
record's `Input`. `applyFinish`'s resume block then does
`s.Variables = copyVars(plan.restoreVars)` when `plan.restoreVars != nil`
(after the pre-existing `plan.resetVars` branch, so full-reverse and
target-reverse restores stay mutually exclusive) — `copyVars` isolates the
retained compensation record's `Input` map from later mutation by the
resumed instance, since records are deliberately **retained** (not cleared)
on a partial-rollback finish.

Keeping the snapshot map off `compensationCursor` and on the transient
`finishPlan` instead preserves the cursor's existing all-scalar,
value-copied invariant (`state.go`, `cloneState`) — this addition has no
`cloneState` impact.

**4. Edge case: empty snapshot.** If the target node's most-recent
compensation record has a nil/empty `Input` — only possible when the
instance started with no variables at all and the target node was the first
node recorded — the current variables are left untouched rather than being
wiped to an empty map (`plan.restoreVars != nil` guards the assignment).
This is documented on `WithTargetNode`'s godoc.

**5. Wire and facade surfaces updated in lockstep.** The `CompensateRequested`
journal codec (`internal/persistence/store/trigger_codec.go`) round-trips
the new field as `restore_target_vars` alongside the pre-existing
`reverse_node`/`reset_vars`, so a persisted target-reverse trigger replays
with full fidelity. `WithTargetNode`'s godoc
(`runtime/processdriver_reverse.go`) and the `reverse_rollback` example were
updated to describe the restore and the breaking change from ADR-0109.

## Consequences

- **Breaking.** Any consumer relying on `ReverseInstance` +
  `WithTargetNode` keeping the instance's current variables must switch to
  constructing `engine.NewCompensateRequested(at, toNode)` directly (via
  `driver.ApplyTrigger`) to keep that behavior — the facade no longer offers
  it. This is judged acceptable pre-1.0: `wrkflw` is not yet tagged
  (v0.1.0 untagged), so the module has no released API-compatibility
  obligation yet.
- **Positive.** Closes the gap ADR-0109 explicitly deferred: a target
  reverse is now closer to "replay to that point in time" than a
  checkpoint-and-continue with stale-relative-to-target data, using data
  (`CompensationRecord.Input`) that already existed for another purpose
  (feeding each compensate action its original input).
- **Positive.** The admin/raw `engine.NewCompensateRequested` path and the
  cancel-preempt flow are untouched — they do not opt into
  `RestoreTargetVars` and keep exactly their pre-existing current-variables
  behavior, verified by the guard rejecting the combination only when
  `ToNode == ""`, which never applies to their calls.
- **Neutral.** `compensationCursor` gains one more scalar field
  (`RestoreTargetVars`), following the same pattern as `ReverseNode`/
  `ReverseResetVars`; the resolved snapshot map itself lives only on the
  transient `finishPlan`, so the cursor's all-scalar/value-copied invariant
  used by `cloneState` is preserved.
- **Supersedes.** This ADR supersedes the target-reverse variable-semantics
  clause of ADR-0109 point 7 and its "Deferred" Consequences bullet — those
  remain in ADR-0109 as the historical record of the original decision and
  now cross-reference this one; ADR-0109 itself is not marked Superseded, as
  its other decisions (the facade, the full-reverse mode, the operation
  trio) are unaffected.

## References

- ADR-0109 (`docs/adr/0109-reverse-instance.md`) — the facade and full-reverse
  decision this ADR refines.
- Plan: `docs/plans/2026-07-10-reverse-instance-followups.md` ("FU#1").
- `engine/trigger.go` (`CompensateRequested.RestoreTargetVars`,
  `NewReverseToNode`), `engine/state.go`
  (`compensationCursor.RestoreTargetVars`), `engine/step_compensation.go`
  (`stepCompensateRequested` guard, `stepCompensationFinish`/`finishPlan`/
  `applyFinish`), `internal/persistence/store/trigger_codec.go`,
  `runtime/processdriver_reverse.go`,
  `examples/scenarios/reverse_rollback/main.go`.
