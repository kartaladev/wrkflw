# 142. `ProcessInstance` active-task accessors

Status: Accepted — 2026-07-27. Spec:
[docs/specs/2026-07-27-processinstance-active-tasks.md](../specs/2026-07-27-processinstance-active-tasks.md).
Extends the `ProcessInstance` interface introduced by
[ADR-0098](0098-service-coherent-graph-refactor.md).

## Context

`service.ProcessInstance` (`service/instance.go:17`) is the read-only, fused view
of a running instance — `Definition()`, `State()`, and `json.Marshaler`. It is an
**interface** deliberately kept small so a consumer can embed it in their own DTO
and customize the response shape (CLAUDE.md "API response customization").

Consumers repeatedly need two answers about the human tasks of a running instance:

1. the **active task at a specific node** (to render its claim/complete UI), and
2. **all active tasks** in the instance (a per-instance task inbox).

Today the only path is `inst.State().Tasks` filtered by hand on
`humantask.HumanTask.IsOpen()`. That leaks an internal invariant — "active" ==
open == `Unclaimed || Claimed` (`humantask/humantask.go:92`) — onto every consumer
and is easy to get subtly wrong (e.g. forgetting to exclude `Cancelled`).

Source-verified facts shaping the decision:

- `State() engine.InstanceState` already exposes `Tasks []humantask.HumanTask`
  (`engine/state.go:159`) — the full records including `State`. The snapshot is
  fully materialized, so the accessors need **no `TaskStore` call**, no `context`,
  and no `error`.
- Human tasks are keyed by **`TaskToken`** — a fresh unique token per node entry
  (call site `engine/step_nodes.go:603`; generator
  `engine/step_state.go:129`, `<InstanceID>-h<TaskSeq>`) — **not** by `NodeID`;
  records are appended in creation order without dedup.
- The engine has **no multi-instance activity support**: `UserTask`
  (`definition/activity/activity.go:26`) has no cardinality/loop/instances field,
  and no multi-instance machinery exists. Therefore **every well-formed graph has
  at most one open task per node**. The only way to produce two concurrent open
  tasks at one node id is a degenerate graph (a parallel-split gateway with two
  outgoing flows into the *same* `UserTask` node), which is a modeling smell.
- `State()` returns `p.st` **directly** (`service/instance.go:35`) — it does *not*
  call `InstanceState.Clone()`. So `State().Tasks` already aliases the snapshot's
  backing arrays.
- The **only** in-repo implementation of the interface is the unexported
  `processInstance` struct (`service/instance.go:29`).

### Options

1. **Add the two methods to the `ProcessInstance` interface** (chosen) —
   `ActiveTask(nodeID string) (humantask.HumanTask, bool)` and
   `ActiveTasks() []humantask.HumanTask`. Most discoverable; matches the user's
   explicit request for methods on `ProcessInstance`. Breaking for hand-rolled
   implementers (see Consequences); acceptable pre-v0.1.0 with this ADR + a
   CHANGELOG entry.
2. **Methods on the concrete `processInstance` struct only** (not the interface).
   Rejected: not reachable through the interface consumers actually hold; poor
   discoverability; forces type-assertion.
3. **A separate capability interface** (e.g. `ActiveTasker`) consumers assert.
   Rejected: non-breaking but introduces a second concept for what is core
   instance state; the user wants the methods on `ProcessInstance` itself.
4. **Free functions** (`service.ActiveTasks(inst)`). Rejected: non-breaking but the
   user explicitly wants methods on the type; also less ergonomic at call sites.

### `ActiveTask` return shape

`ActiveTask` returns **`(humantask.HumanTask, bool)`**, not a slice. Because the
engine has no multi-instance support, one-open-task-per-node is the real contract,
so a single value + found-bool is the ergonomic fit and never lossy in practice. A
slice or `(task, error)` would force every caller to handle a multiplicity /
error case that a well-formed graph cannot produce. To stay deterministic even on
a pathological (degenerate-graph) state, `ActiveTask` returns the **first** open
task matching the node in **ascending `TaskToken` order** rather than an arbitrary
one. It reuses `ActiveTasks` (filter + sort) so this tie-break is defined once;
that is O(n log n) + one allocation per call, acceptable at the expected scale (a
handful of open tasks per instance).

## Decision

Add to the `service.ProcessInstance` interface, and implement on `processInstance`:

```go
// ActiveTask returns the open human task at nodeID and true, or the zero
// HumanTask and false if the node has no open task. "Open" means Unclaimed or
// Claimed (humantask.IsOpen). A well-formed graph has at most one open task per
// node; if a pathological definition produces more than one, the first in
// ascending TaskToken order is returned.
ActiveTask(nodeID string) (humantask.HumanTask, bool)

// ActiveTasks returns every open human task (Unclaimed or Claimed;
// humantask.IsOpen) in the instance, sorted by TaskToken in ascending
// lexicographic order. Never nil: an instance with no open tasks yields a
// non-nil empty slice.
ActiveTasks() []humantask.HumanTask
```

Behavior:

- Pure projections over the already-materialized `p.st.Tasks` snapshot: filter by
  `humantask.IsOpen()`, sort by `TaskToken` ascending. No I/O, no `context`, no
  `error`.
- `ActiveTasks` returns a non-nil empty slice when none are open.
- `ActiveTask` returns the first open match in `TaskToken` order, or `(zero, false)`.
- **Ordering is lexicographic, not creation order.** Tokens have the form
  `<InstanceID>-h<N>` (`engine/step_state.go:129`), so ascending string order puts
  `…-h10` before `…-h2` once an instance has ≥10 human tasks. This is deterministic
  and matches the existing `TaskStore` "sorted by TaskToken" contract
  (`humantask/humantask.go:109`); it is intentionally *not* chronological
  (consumers can re-sort by `CreatedAt`). Note `TaskState`'s zero value is
  `Unclaimed`, so a zero-value `HumanTask` counts as open; an out-of-range
  `TaskState` does not.
- **Aliasing.** The returned **slice is freshly allocated** (`make`+`append`) and
  is **not** aliased to the snapshot's `Tasks` array — a strictly safer
  outer-slice guarantee than `State().Tasks` (which returns the snapshot's own
  array). Each **element is a shallow value copy**; its reference-typed fields
  (`Candidates`, `Eligibility.*`, `Vars`, and the `DueAt *time.Time` pointer)
  still alias the snapshot — the same
  shallow sharing `State().Tasks[i]` exposes (`State()` returns `p.st` directly,
  no `Clone()`). We deliberately do **not** deep-copy those fields.
- **Concurrency.** No guarantee beyond `State()`: safe for concurrent reads of an
  immutable snapshot; a consumer must not read while the source instance is being
  mutated (same hazard `State().Tasks` carries).
- `MarshalJSON` / `instanceJSON` are **unchanged** — these are accessors only;
  open tasks remain visible via the existing `tasks[].state` field.

### Out of scope (explicit)

- No change to the JSON wire format.
- No new sorting/ordering guarantee on `State().Tasks` itself; the accessors sort
  their own results.
- The pre-existing README doc-rot (root + `service/README.md`, ADR-0141 follow-up)
  is **not** touched here.

## Consequences

- **Breaking public-API change** (pre-v0.1.0, no stability promise): adding methods
  to an interface breaks any consumer that provides their **own** implementation of
  `ProcessInstance`. Consumers who obtain it from the engine and **embed** it get
  the methods promoted automatically — no code change, but they **must recompile**
  against the new version. Internally only `processInstance` implements the
  interface, so the change is a single-struct update plus tests. Recorded in
  CHANGELOG with the migration note: a hand-rolled implementer adds the two methods,
  filtering `State().Tasks` by `humantask.IsOpen()`, returning a **non-nil** slice
  **sorted ascending by `TaskToken`** (`ActiveTasks`) and the **first** such match
  (`ActiveTask`).
- Consumers get a stable, discoverable, correctly-defined "active task" accessor and
  no longer re-implement the open-state filter.
- No behavior change to execution, persistence, or serialization; the safety net is
  the new unit tests plus the unchanged existing suite.
- The one-open-task-per-node contract is documented; if the engine later gains
  multi-instance activities, `ActiveTask`'s contract must be revisited (a future ADR
  would change the return shape) — noted so the assumption is not silently carried.
