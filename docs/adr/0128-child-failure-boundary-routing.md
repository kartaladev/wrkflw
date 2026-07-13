# 128. `SubInstanceFailed` participates in call-activity boundary-error routing

- Status: Accepted
- Date: 2026-07-13

Task C8 of the engine-simplify Phase C backlog. Plan:
`docs/plans/2026-07-13-engine-simplify-phase-c.md` (Task C8).

## Context

A `CallActivity` node starts a child process instance (`StartSubInstance`) and
parks the parent token until the child terminates. On success,
`SubInstanceCompleted` resumes the parked token and drives it past the
call-activity node. On failure, `SubInstanceFailed` reported the child's error
message but `handleSubInstanceFailed` **unconditionally** transitioned the
parent to `StatusFailed` via `FailInstance` — even when the call-activity node
carried an attached boundary error event that would, for an ordinary activity's
`ActionFailed`, catch the error and let the parent recover.

BPMN2 models a call activity as an activity like any other: it can carry
attached boundary events, including an error boundary. A child instance's
failure is semantically indistinguishable from any other activity throwing an
error at that node — it should be catchable the same way. The engine already
has this machinery for ordinary activities: `propagateError`'s direct-attachment
check uses `findDirectBoundary` (scan the host definition for a
`KindBoundaryEvent` error boundary attached to the failing node, matching via
the three-tier `boundaryErrorMatches` precedence) and `routeToBoundary` (fire
the boundary's action, consume the failing token, resolve the outgoing flow,
place a recovery token, drive). `SubInstanceFailed` never invoked either.

`SubInstanceFailed.Err` is the only field carrying failure information — a
free-form message set by the runtime (`err.Error()` from the child's failure,
or a fixed string for `terminate`/timeout/reject outcomes; see
`runtime/processdriver_action.go` and `runtime/calllink/notifier.go`). This
mirrors `ActionFailed.Err`, which `propagateError` already uses directly as the
boundary-matching error code (there is no separate "error code" field on
`ActionFailed` either) — so no new field is needed on `SubInstanceFailed` to
make it participate in boundary matching.

## Decision

Treat `SubInstanceFailed` as an error thrown **at the call-activity node** that
spawned the child, and route it through the same direct-boundary machinery
`propagateError` uses for `ActionFailed`, before falling back to the existing
`FailInstance` behavior.

In `handleSubInstanceFailed` (`engine/step_triggers.go`):

1. Locate the parent's parked call-activity token via
   `s.tokenAwaiting(t.CommandID)` (existing lookup, unchanged) — its `NodeID` is
   the call-activity node, its `ScopeID` is the host scope.
2. Resolve the host definition via `defForScope(def, s, tok.ScopeID)` — the same
   definition `propagateError`'s direct-attachment check resolves for an
   ordinary activity in the same scope.
3. Run `t.Err` as the error code through `findDirectBoundary(hostDef,
   tok.NodeID, t.Err, s.Variables, cause, eval)`, with `cause :=
   errors.New(t.Err)` synthesized exactly as `propagateError` does for
   `cause == nil` (bare-code sources — `SubInstanceFailed` carries no live Go
   error, only a message).
4. On a match: `routeToBoundary(def, s, hostDef, boundary, ..., tok.ScopeID, ...,
   consume)` where `consume` consumes the call-activity token **by ID**
   (`s.consumeToken(tok, ...)`), mirroring `propagateError`'s
   consume-by-ID pattern for the direct-boundary case (correctness under
   parallel/loop topologies where more than one token could occupy the same
   node). The route stays in the **same scope** — only the call-activity token
   is consumed, matching the direct-attachment (non-scope-closing) case.
5. On no match: fall back to the current behavior unchanged — `StatusFailed`,
   `FailInstance{Err: t.Err}`, and the existing timer/task/arm cleanup.

`handleSubInstanceFailed`'s signature grows `def *model.ProcessDefinition` and
`opt StepOptions` (needed to resolve the host definition, the condition
evaluator, and the step mode for `drive`) — an unexported function signature
change, not a public API break; `Step`'s dispatch in `engine/step.go` is
updated to match.

No new exported symbol or sentinel error is introduced: `findDirectBoundary`
and `routeToBoundary` are reused as-is (added by the companion C2 task), and
`SubInstanceFailed.Err` already served as the error code by the same convention
as `ActionFailed.Err`.

Only the enclosing-scope walk (the second phase of `propagateError`,
`findEnclosingBoundary`) is intentionally **not** wired in here — a call
activity's own attached boundary is the direct-attachment case; if it goes
uncaught, the existing scope/root-level fallback (`FailInstance`) still
applies, matching the plan's scope for this task. Extending an enclosing-scope
walk for the call-activity's parent scope is not requested by this task.

The companion completeness gap — `closeScope` not cascading to descendant
scopes — is Task C9 and is out of scope for this ADR; see the Phase C plan for
its own decision record.

## Consequences

- A parent process can now recover from a child instance's failure via a
  boundary error event on the call-activity node, instead of always failing.
  This closes a real BPMN2-alignment gap (call activities were the only
  activity kind whose failure could not be caught by an attached error
  boundary).
- Existing behavior is preserved byte-for-byte when no boundary matches:
  `FailInstance` plus the full timer/task/arm cleanup, unchanged.
- `handleSubInstanceFailed` now depends on `def`/`opt`, matching the shape of
  every other trigger handler that needs to resolve a scope definition or an
  evaluator (`handleSubInstanceCompleted`, `handleActionFailed`, etc.) — this
  removes an inconsistency rather than introducing one.
- `SubInstanceFailed.Err` is now doing double duty (human-readable message +
  boundary-matching error code), exactly like `ActionFailed.Err` already does.
  This is accepted as consistent with the established convention rather than a
  new design point; a future task could split these if callers need a stable
  machine-readable code distinct from the display message, but that is not
  required by any current caller (`runtime/calllink`,
  `runtime/processdriver_action.go`).
- Wire format is unchanged: `SubInstanceFailed`'s fields (`CommandID`, `Err`)
  are untouched; only in-engine routing behavior changes.
