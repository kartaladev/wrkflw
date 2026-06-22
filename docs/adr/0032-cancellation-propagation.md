# 32. Cancellation propagates down the async call tree

- Status: Accepted
- Date: 2026-06-22

## Context

`CancelInstance` (ADR-0028) terminates exactly one instance via the engine's `CancelRequested`
(clear tokens, `StatusTerminated`, process-level `CancelActions`, `FailInstance{cancelled}`). It does
not touch the instance's **child call-activity instances** (ADR-0024/0025, async path). A parent
parked at a call activity that is cancelled leaves its child **running and orphaned**: the child runs
to completion, flips its call link terminal, and the notifier then resolves the (terminated) parent
via `engine.ErrTokenNotFound` — correct, but the child's (and any grandchildren's) work ran for
nothing. ADR-0024/0025 explicitly listed cancellation propagation as out of scope.

The engine is pure and has no knowledge of call links (they live in the runtime/persistence layer,
not `InstanceState`), so propagation must be a **runtime** concern. The call-link table is keyed by
`child_instance_id` and offers no way to enumerate a parent's children.

## Decision

Make `runner.CancelInstance` propagate cancellation down the call tree, best-effort, with **zero
engine/model change**:

1. Add `CallLinkStore.ListRunningChildren(ctx, parentInstanceID) ([]CallLink, error)` (Mem +
   Postgres). Postgres migration `0007` adds a partial index
   `(parent_instance_id) WHERE status = 'running'`.
2. `runner.CancelInstance(ctx, def, instanceID)` first delivers `CancelRequested` (terminating the
   instance, unchanged), then — when `WithCallLinks` and `WithDefinitions` are both configured —
   recursively cancels each running child: resolve the child def via `store.Load(childID)` →
   `registry.Lookup("defID:version")`, then `CancelInstance(childDef, childID)`. Recursion reaches
   grandchildren; a `visited` set guards pathological cycles (the tree is already depth-bounded at
   `maxCallDepth=64`).
3. **Parent-first ordering:** terminate the parent before cancelling children, so a child-cancel that
   flips the child link terminal cannot race a concurrent notifier into resuming the still-parked
   parent (the parent is already terminal → clean `ErrTokenNotFound` no-op).
4. **Best-effort:** every propagation error (list/load/lookup/child-cancel) is logged and swallowed —
   never failing the parent cancel, consistent with ADR-0028's cancel-action contract.

## Consequences

**Positive**

- Cancelling a parent now stops its entire running subtree instead of leaving children to run
  needlessly; cancelled children run their own `CancelActions` (cleanup propagates too).
- Engine/model untouched — determinism/purity intact; propagation is isolated to the runtime.
- Cancelled children flip their links terminal, so they aren't re-listed and resolve cleanly via the
  existing notifier `ErrTokenNotFound` path — no new orphan handling.
- Automatic when the async call-activity feature is wired; no new opt-in flag, no transport/service
  change.

**Negative / trade-offs**

- A new additive `CallLinkStore` port method (`ListRunningChildren`) — breaking for external store
  implementers (we own Mem + Postgres); an implementer that can't support it returns `nil, nil`
  (propagation simply doesn't happen).
- Cancelling a large subtree issues one `CancelInstance` (a `Deliver` + store round-trip) per running
  descendant — acceptable for an admin cancel (not a hot path), bounded by the tree size.
- Best-effort means a child whose definition the registry cannot resolve is *not* cancelled (logged,
  left running); it will still resolve cleanly later via the notifier no-op, but its work runs on.
  Documented; the cure is ensuring child defs are registered.

**Deferred**

- **Per-active-node cancel handlers** (a `Node`-scoped cancel action distinct from the process-level
  `CancelActions`) — a model+engine change, separate track.
- **Synchronous** call activities need nothing: a sync child completes inside `perform`, so it is
  never running at parent-cancel time.
