# Design: cancellation propagation parent→child (async call activities)

**Date:** 2026-06-22
**Status:** Approved (autonomous run)
**Track:** Consolidated-backlog top pick (Production-hardening). Follow-up to ADR-0028 (CancelInstance) and ADR-0024/0025 (async call activity).
**ADR:** 0032.

## 1. Problem & scope

`CancelInstance` (ADR-0028) terminates exactly one instance: the engine's `CancelRequested` clears
all tokens, sets `StatusTerminated`, runs process-level `CancelActions` (best-effort), and emits
`FailInstance{Err:"cancelled"}`. It does **not** touch the instance's **child call-activity
instances**. When a parent is parked at a call activity (async path, `WithCallLinks`) and is
cancelled, its parked token is cleared but the **child instance keeps running** — orphaned. The
child's eventual outcome flips its call link to terminal; the notifier then tries to resume the
(now-terminated) parent, gets `engine.ErrTokenNotFound`, and marks it notified — *correct but the
child's work ran to completion needlessly*, and any grandchildren too.

This track makes cancellation **propagate down the call tree**: cancelling a parent also cancels its
still-running children, recursively, so the whole subtree stops.

**In scope:** recursive, best-effort child cancellation in the runtime layer, gated on the async
call-activity feature being wired (`WithCallLinks` + `WithDefinitions`); a new
`CallLinkStore.ListRunningChildren(parentID)` read method (Mem + Postgres) with a supporting index.

**Out of scope (deferred, documented in ADR §Consequences):**
- **Per-active-node cancel handlers** (a `Node`-scoped cancel action vs the process-level
  `CancelActions`) — a model+engine change; separate track.
- **Synchronous** call activities need no propagation: a sync child runs to completion *inside*
  `perform(StartSubInstance)`, so it is never "running" when the parent is later cancelled.
- Orphaned-child cleanup for the *already-terminal parent* case is already handled by the notifier's
  `ErrTokenNotFound`→`MarkNotified` path (no change needed).

**Engine/model untouched** (zero diff): propagation is a pure runtime concern — the engine has no
knowledge of call links (they live in the runtime/persistence layer, not `InstanceState`).

## 2. Design

### 2.1 Enumerate running children — `ListRunningChildren`

`wrkflw_call_links` is keyed by `child_instance_id` and stores `parent_instance_id` + `status`
(`running`/`completed`/`failed`/`notified`). There is no way today to find a parent's children. Add:

```go
// runtime.CallLinkStore gains:
//   ListRunningChildren(ctx context.Context, parentInstanceID string) ([]CallLink, error)
//   — links whose ParentInstanceID == parentInstanceID and status == 'running'.
```

- **Postgres:** `SELECT child_instance_id, parent_instance_id, parent_command_id, parent_def_id,
  parent_def_version, depth FROM wrkflw_call_links WHERE parent_instance_id = $1 AND status =
  'running' ORDER BY child_instance_id`. Migration `0007_call_link_parent_idx.sql` adds
  `CREATE INDEX wrkflw_call_links_parent_running_idx ON wrkflw_call_links (parent_instance_id) WHERE status = 'running';`
- **Mem:** iterate the link map, returning non-terminal (`!terminal`) links whose parent matches.

This is an additive port method (breaking only to external `CallLinkStore` implementers; we own Mem
+ Postgres). Implementers that don't support it can return `nil, nil` (no propagation).

### 2.2 Recursive cancel in `runner.CancelInstance`

`runner.CancelInstance(ctx, def, instanceID)` becomes:

```
st := Deliver(ctx, def, instanceID, CancelRequested)      // terminate THIS instance first
if err != nil { return st, err }                          // (unchanged behaviour)
if r.callLinks != nil && r.defsReg != nil {
    propagateCancel(ctx, instanceID, visited{instanceID})  // best-effort
}
return st, nil
```

`propagateCancel(parentID)`:
1. `children, err := r.callLinks.ListRunningChildren(ctx, parentID)` — on error, log + return
   (best-effort; never fail the parent cancel, per ADR-0028).
2. For each child link:
   - Skip if already visited (defensive cycle guard; the depth limit already bounds the tree).
   - Resolve the child def: `childSt, _, err := r.store.Load(ctx, child.ChildInstanceID)`;
     `childDef, err := r.defsReg.Lookup(fmt.Sprintf("%s:%d", childSt.DefID, childSt.DefVersion))`.
     On any error or unresolved def → log + continue (best-effort).
   - `r.CancelInstance(ctx, childDef, child.ChildInstanceID)` — recursion cancels the child (running
     its own `CancelActions`) and, in turn, its grandchildren. Log on error; continue.

**Ordering (parent-first) is deliberate:** terminating the parent before cancelling children means a
child-cancel that flips the child's link to terminal cannot race a concurrent notifier into
*resuming* the still-parked parent — the parent is already terminal, so any such delivery is a clean
`ErrTokenNotFound` no-op.

**Why this flips links cleanly:** a cancelled child reaches `StatusTerminated`; the runtime's
terminal-step handling (`runner.go` `isTerminal(st.Status)` branch) already derives a
`CallOutcome{Completed:false, Err:"cancelled"}` and the Store flips the child's link to terminal
atomically. So a cancelled child is *not* re-listed by `ListRunningChildren` (status≠running), and
its link is later resolved by the notifier against the terminated parent (`ErrTokenNotFound` →
`MarkNotified`). No orphan, no double-cancel.

### 2.3 Gating & compatibility

Propagation runs only when both `WithCallLinks` and `WithDefinitions` are configured (async call
activities can only exist then). With neither (e.g. MemStore-only, or sync-only call activities),
`CancelInstance` behaves exactly as today. The `service.CancelInstance` and transports are unchanged
(they call `runner.CancelInstance`, which now propagates internally).

## 3. Correctness & safety

- **Best-effort, never fails the parent cancel:** every propagation error (list, load, lookup,
  child-cancel) is logged via the injected `slog` logger and swallowed — consistent with ADR-0028's
  cancel-action contract.
- **Termination guaranteed finite:** the call tree is finite and depth-bounded (`maxCallDepth=64`,
  enforced at child creation); a `visited` set guards against pathological cyclic data.
- **Idempotent:** cancelling an already-terminated child is a no-op (its link is already terminal so
  it isn't listed; a direct re-cancel sets Terminated again harmlessly).
- **No engine/model change** → determinism/purity untouched.

## 4. Testing strategy

- **runtime (`runtime_test`, MemCallLinkStore + MemStore):**
  - Start a parent that parks at a call activity with an async child that parks at a human task
    (mirror `TestAsyncCallActivityParentParks`). Cancel the parent → assert parent
    `StatusTerminated` **and** child loaded from the store is `StatusTerminated` (propagated).
  - Two-level: parent → child → grandchild all parked; cancel parent → all three terminated
    (recursion).
  - Best-effort: a child whose def the registry cannot resolve → parent cancel still succeeds
    (logged, child left as-is); assert no error from `CancelInstance`.
  - A child that already completed before the parent cancel is not re-cancelled (not listed).
  - `ListRunningChildren` mem unit test: returns only running children of the given parent.
- **internal/persistence/postgres** (testcontainers): `ListRunningChildren` returns the running
  children for a parent and excludes terminal/other-parent rows; migration `0007` index applied by
  `Migrate`. A propagation e2e over the Postgres store (parent+child parked → cancel parent → both
  terminated, child link flipped) if tractable; otherwise the runtime mem e2e plus the postgres
  `ListRunningChildren` unit test cover the seam.

**Gate:** `go test -race -p 1 ./...` green; ≥85% on `runtime`, `internal/persistence/postgres`,
`persistence`; `golangci-lint` clean; **engine/model production diff ZERO**; `workflow-` prefixes.

## 5. ADR

| ADR | Decision |
|---|---|
| **0032** | Cancellation propagates down the async call tree: `runner.CancelInstance` terminates the instance, then best-effort recursively cancels its running children (enumerated via a new `CallLinkStore.ListRunningChildren` + a `parent_instance_id` partial index, migration 0007), resolving each child def via `store.Load`→registry. Parent-first ordering avoids a notifier resume race; cancelled children flip their links terminal so they aren't re-listed and resolve cleanly via `ErrTokenNotFound`. Engine/model untouched. Per-active-node cancel handlers deferred. |
