# 13. Hoist sub-process compensation records into the parent scope on close

- Status: Accepted
- Date: 2026-06-21

## Context

The engine records a `CompensationRecord` for each completed compensable activity
into the execution scope the activity ran in: root-level activities accumulate in
`InstanceState.RootCompensations`, and activities inside a sub-process accumulate
in that sub-process's `Scope.Compensations` (Plan 8). The only compensation entry
point today is the admin/debug `CompensateRequested` trigger, which walks
`RootCompensations` in reverse completion order, emitting one compensating
`InvokeAction` per record.

On normal sub-process exit, `engine/step.go` calls `s.closeScope(currentScopeID)`,
which removes the `Scope` from `s.Scopes` — **and silently drops its
`Compensations`**. So once a sub-process completes, the compensable activities that
ran inside it can no longer be rolled back: `CompensateRequested` reaches only
`RootCompensations`, and `compensationRecordsForScope` returns `nil` for a closed
scope. This is a correctness bug for nested sagas, labelled MUST-FIX in
`docs/plans/HANDOVER.md`. The reserved `Compensate{ScopeID,FromNode}` command was
intended as the future scope-targeted vehicle but is inert (no producer).

Two ways to make completed-sub-process records rollback-able:

1. **Hoist into the parent at close time** — append the closing scope's
   `Compensations` to its parent scope (or `RootCompensations` if the parent is
   root) in completion order, then close. The existing root walk reaches them.
2. **Archive keyed by closed scope id** — keep a new `InstanceState` map/slice of
   closed-scope records and make `CompensateRequested`/`Compensate` select a
   scope to compensate.

## Decision

**Hoist the closing scope's `Compensations` into its parent on the normal
sub-process-exit path**, before `closeScope`, preserving completion order. Add a
helper `(*InstanceState).hoistCompensations(childID, parentID string)`:
`parentID == ""` appends to `RootCompensations`; otherwise appends to the parent
`Scope.Compensations`; the child's slice is cleared. The call is inserted
immediately before `s.closeScope(currentScopeID)` in the sub-process-exit path,
ahead of the existing "sub-process node's own `CompensationAction`" recording, so
the parent list ends up `[…parent-records, …hoisted-child-records, spNode-own]`.

Consequences for the rest of the machinery:

- **No new `InstanceState` field.** `cloneState`, the snapshot JSONB shape, and the
  persistence round-trip are untouched — the records simply live in a different
  existing slice. Determinism is preserved (slice-order appends, no new IDs, no
  clock).
- **Nested sub-processes work by induction**: each scope close hoists one level
  up, so a grandchild's records reach the root after its ancestors close.
- **`CompensateRequested` is unchanged** — it still walks `RootCompensations`,
  which now transitively contains every completed activity's record in reverse
  saga order.
- **`Compensate{ScopeID,FromNode}` stays reserved and inert.** Its godoc and
  `CompensateRequested`'s limitation note are corrected: nested records are now
  reachable via the root walk; `Compensate` remains the future vehicle for
  *scope-targeted* compensation, which requires a producer (a BPMN compensation
  boundary/throw event) that is not built here.
- **Only the normal-exit path changes.** The error-propagation and cancel paths
  also call `closeScope`; compensation-on-error/cancel has different semantics and
  is out of scope. Those call sites are audited (not modified) so the change is
  deliberate, not accidental.

This matches BPMN saga semantics: compensation of a completed embedded sub-process
is performed as part of the enclosing scope's compensation, in reverse order.

## Consequences

**Easier:** the MUST-FIX is resolved with a minimal, deterministic change that
touches one call site plus a small helper — no new state, no persistence-format
change, no change to the compensation walk, the cursor, or `cloneState`. Nested
sagas now roll back correctly through the existing admin trigger, proven by a
regression test that fails before the change. The reserved `Compensate` command's
documentation becomes truthful.

**Harder / trade-offs:** hoisting **erases per-scope identity** — once records are
in the parent/root list there is no boundary marking which sub-process they came
from, so "compensate just this completed sub-process" is not expressible. That is
acceptable because the only entry point is the root-scope admin trigger; true
scope-targeted compensation (archive-by-scope + a scope selector + a compensation
boundary/throw producer) is a deliberate deferred follow-up. A subtle ordering
question — whether the sub-process node's own `CompensationAction` should
compensate before or after its children — is resolved as "before" (it completed
last, so it is most-recent in the reverse walk); this is a defensible BPMN reading
and is locked by an ordering test.
