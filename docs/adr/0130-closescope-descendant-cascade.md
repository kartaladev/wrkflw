# 130. `closeScope` cascades to descendant scopes

- Status: Accepted
- Date: 2026-07-13

Task C9 of the engine-simplify Phase C backlog. Plan:
`docs/plans/2026-07-13-engine-simplify-phase-c.md` (Task C9).

## Context

`(*InstanceState).closeScope(scopeID)` (`engine/state_compensation.go`) removes
the `Scope` with the given ID from `s.Scopes`. Its doc comment admitted the gap
directly: "Child scopes (those whose ParentID equals scopeID) are NOT
automatically removed — callers are responsible for closing or reparenting
children before closing a parent. This is intentionally minimal; Plan 8
(compensation/rollback) will add the richer cascading logic." Plan 8 shipped
(ADR-0039 onward) without ever adding that logic, leaving the comment stale
and the gap real.

All three current call sites (`engine/step_nodes.go` — regular sub-process
exit and event-sub-process exit, `engine/step_errors.go` — enclosing-scope
boundary-error routing) close a scope only after confirming, via
`tokensInScope`, that no active child scope still holds tokens. In the
common/tested paths this makes single-scope `closeScope` behaviorally
equivalent to a cascade — there is nothing left to cascade into. But nothing
enforced that invariant: a child scope that had already drained to zero
tokens but was never itself explicitly closed (e.g. because its own exit path
returned early, or a future call site closes a scope without walking its
children first) would be left as a permanently orphaned entry in `s.Scopes` —
a real, unbounded leak in a long-lived process instance's persisted state.

A caller audit (`grep -n closeScope engine/*.go`, all four unexported call
sites) confirmed no caller *relies on* single-scope-only removal for
correctness:

- `step_nodes.go` (`exitEventSubprocessScope`, `exitRegularSubprocessScope`):
  each already gates the `closeScope` call on `hasActiveChildren`/similar
  checks finding zero live descendants. A cascade is a no-op there today and
  only helps the leak case above.
- `step_errors.go` (enclosing-scope boundary routing): cancels tokens whose
  `ScopeID` equals the erroring scope, then closes it. If that scope had a
  live descendant scope (e.g. a sibling parallel branch that had opened a
  nested sub-process), the descendant's tokens were never cancelled by this
  path either before or after this change — that is a pre-existing, separate
  gap (untracked-descendant-token cancellation) out of scope for C9. Before
  this change, the descendant `Scope` entry survived with a `ParentID`
  pointing at a now-removed scope — an already-inconsistent, already-broken
  state (`defForScope` on that descendant already errored with "unknown
  scope" before this ADR, since it resolves via `scopeByID` on the missing
  parent). The cascade does not make this worse; it removes the equally-stale
  descendant entry instead of leaving a dangling one.

## Decision

`closeScope(scopeID)` now removes the target scope **and** every descendant
scope reachable via the `ParentID` chain (transitively — children,
grandchildren, ...), in one pass over `s.Scopes`. It relies on the existing
invariant that `openScope` always appends a scope after its parent already
exists (a scope's `ParentID` must resolve at open time), so a single forward
scan can mark a scope "doomed" and propagate that to any scope appended later
whose `ParentID` is already doomed — no recursion or second pass needed.

It remains a no-op when `scopeID` does not exist in `s.Scopes` (covering both
"never existed" and "already closed"), preserving idempotency.

`closeScope` itself still only prunes `s.Scopes`; it does not touch tokens,
arms, or timers — that division of labor is unchanged. Callers remain
responsible for cancelling tokens/arms/timers for whatever scopes they intend
to tear down, exactly as before. This task closes the audit gap named by the
stale "Plan 8 will add cascading" comment, which has been removed.

## Consequences

- `s.Scopes` can no longer accumulate orphaned descendant entries when a
  parent scope is closed — the structural leak flagged by the stale comment
  is closed.
- No caller changes were required: all four existing call sites are either
  unaffected (no live descendants at close time) or already tolerated the
  pre-existing dangling-descendant inconsistency in `step_errors.go`, which
  the cascade improves rather than worsens.
- `closeScope` remains unexported; no public API surface changes (`go doc
  ./engine` diff against the pre-C9 snapshot is empty).
- Wire format is unchanged — this only prunes an in-memory/persisted slice
  using existing fields (`Scope.ID`, `Scope.ParentID`); no struct shape
  change.
- The known, separate gap — `step_errors.go`'s enclosing-scope boundary
  routing does not cancel tokens in descendant scopes when tearing down a
  scope that contains a live nested sub-process — is **not** addressed here.
  It predates this change and is orthogonal (token cancellation, not scope
  bookkeeping); it should be tracked as its own follow-up if a live scenario
  needing it is identified.
