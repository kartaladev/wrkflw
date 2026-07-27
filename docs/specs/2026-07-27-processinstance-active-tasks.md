# ProcessInstance active-task accessors

**Date:** 2026-07-27
**Status:** Approved (brainstorming) — pending ADR-0142, plan, adversarial audit.

## Problem

Consumers embedding the engine frequently need to answer two questions about a
running instance:

1. What is the **currently active human task at a given node**? (e.g. render the
   claim/complete UI for a specific step)
2. What are **all currently active human tasks** in the instance? (e.g. a
   per-instance task inbox)

Today the only path is `inst.State().Tasks` filtered by hand on
`humantask.HumanTask.IsOpen()`. That works but pushes an internal invariant
("active" == open == `Unclaimed || Claimed`) onto every consumer and is easy to
get wrong. This feature promotes it to a stable, ergonomic API on
`ProcessInstance`.

"Active task" is defined as an **open** human task: `humantask.IsOpen()` ==
`State == Unclaimed || State == Claimed`. Unclaimed = pending (no actor yet);
Claimed = waiting for the claimer to complete. This is exactly the user's
"pending or waiting-for-completion" definition. `Completed` and `Cancelled`
tasks are never returned.

## Grounded facts (source-verified 2026-07-27)

- `ProcessInstance` is an **interface** (`service/instance.go:17`): `Definition()`,
  `State()`, `json.Marshaler`. It is deliberately small so a consumer can embed it
  in their own DTO and customize the response shape.
- The **only** in-repo implementation is the unexported `processInstance` struct
  (`service/instance.go:29`); `NewProcessInstance` is the sole constructor.
- `State() engine.InstanceState` already exposes `Tasks []humantask.HumanTask`
  (`engine/state.go:159`) — the **full** task records including `State`. The
  snapshot is fully materialized; **no `TaskStore` call is needed** at method-call
  time.
- Human tasks are keyed by **`TaskToken`** (a fresh unique token per node entry,
  `engine/step_nodes.go:603`), **not** by `NodeID`. `NodeID` is a plain field, and
  records are appended to `state.Tasks` in creation order without dedup.
- The engine has **no multi-instance activity support**: `UserTask`
  (`definition/activity/activity.go:26`) carries no cardinality / loop / instances
  field, and there is no multi-instance machinery anywhere. Therefore, for every
  well-formed graph, **at most one open task exists per node**. The only way to
  produce two concurrent open tasks at one node id is a degenerate graph (a
  parallel-split gateway with two outgoing flows into the *same* `UserTask` node),
  which is a modeling smell, not a real scenario. The API contract assumes
  one-open-task-per-node but degrades deterministically if that is violated.
- `humantask` is a pure public root package (imports only stdlib + `authz`);
  returning `humantask.HumanTask` adds **no new coupling** — it is already part of
  the public surface transitively via `State()`.

## Design

Add two methods to the `ProcessInstance` interface:

```go
type ProcessInstance interface {
	Definition() *model.ProcessDefinition
	State() engine.InstanceState
	json.Marshaler

	// ActiveTask returns the open human task at the given node id and true, or
	// the zero HumanTask and false if the node has no open task. "Open" means
	// Unclaimed or Claimed (see humantask.IsOpen). A well-formed graph has at
	// most one open task per node; if a pathological definition produces more
	// than one, the first in ascending TaskToken order is returned.
	ActiveTask(nodeID string) (humantask.HumanTask, bool)

	// ActiveTasks returns every open human task (Unclaimed or Claimed; see
	// humantask.IsOpen) in the instance, sorted by TaskToken in ascending
	// lexicographic order (matching the TaskStore ordering contract). Never
	// returns nil: an instance with no open tasks yields a non-nil empty slice.
	ActiveTasks() []humantask.HumanTask
}
```

### Behavior

- Both methods read **only** from the already-materialized `p.st.Tasks` snapshot.
  No I/O, no `context`, no error return — they are pure projections.
- "Open" is `humantask.IsOpen()` (`Unclaimed || Claimed`). Note the `TaskState`
  zero value is `Unclaimed` (iota 0), so a partially-constructed `HumanTask{}`
  with no explicit `State` counts as open/active — an inherent property of the
  underlying model, not introduced here. An out-of-range `TaskState` is **not**
  open (only `Unclaimed`/`Claimed` are), so it is never returned.
- **Ordering:** results are sorted by `TaskToken` in **ascending lexicographic**
  order (Go string comparison), matching the `TaskStore` "sorted by TaskToken"
  contract (`humantask/humantask.go:109`). This is deterministic but **not**
  creation order: task tokens have the form `<InstanceID>-h<N>`
  (`engine/step_state.go:129`), so once an instance has ≥10 human tasks, `…-h10`
  sorts before `…-h2`. Consumers wanting chronological order should sort the
  result by `CreatedAt` themselves. `ActiveTask` returns the first open match in
  this order.
- `ActiveTasks` returns a **non-nil** slice (empty when none open).
- **Aliasing.** The returned **slice is freshly allocated** (`make`+`append`), so
  it is *not* aliased to the snapshot's `Tasks` backing array — assigning to or
  appending to the result does not write back to the instance. This is a strictly
  *safer* outer-slice guarantee than `State().Tasks`, which returns the snapshot's
  own array. Each **element is a shallow value copy** of a `HumanTask`; its
  reference-typed fields (`Candidates`, `Eligibility.Roles/Privileges`, `Vars`,
  and the `DueAt *time.Time` pointer) still alias the snapshot — the same shallow
  sharing `State().Tasks[i]` already
  exposes (recall `State()` returns `p.st` directly, `service/instance.go:35`; it
  does **not** `Clone()`). We deliberately do **not** deep-copy those fields —
  matching surrounding behavior and avoiding a per-call clone. Callers that mutate
  a shared map/slice field of a returned task mutate the snapshot too, exactly as
  with `State().Tasks[i]` today.
- **Concurrency.** These accessors add no concurrency guarantee beyond `State()`:
  they are safe for concurrent *reads* of an immutable snapshot, but because the
  snapshot aliases the source instance state (no clone), a consumer must not read
  while the source instance is being mutated — the same hazard `State().Tasks`
  already carries.
- **Cost.** `ActiveTask` reuses `ActiveTasks` (filter + sort) so its "first in
  TaskToken order" tie-break is defined once; this is O(n log n) + one allocation
  per call. Acceptable at the expected scale (a handful of open tasks per
  instance); not optimized further.

### JSON output — unchanged

These are accessor methods only. `MarshalJSON` / `instanceJSON` are untouched;
open tasks remain visible via the existing `tasks[].state` field. No wire-format
change.

## Interface-break impact

Adding methods to the `ProcessInstance` **interface** is a **breaking change** for
any consumer that provides their **own** implementation of the interface
(hand-rolled, not embedded). Consumers who obtain a `ProcessInstance` from the
engine and **embed** it get the new methods promoted automatically — no break.

- Internally, only `processInstance` implements the interface, so the change is a
  single-struct update plus tests.
- Consumers who **embed** need no code change but **must recompile** against the
  new version (a `nil` embedded interface would panic on call — a pre-existing
  property of embedding).
- This is pre-1.0; breaking changes are permitted with an **ADR (0142)** and a
  **CHANGELOG** entry documenting the migration. A hand-rolled implementer must add
  the two methods, filtering `State().Tasks` by `humantask.IsOpen()`, returning a
  **non-nil** slice **sorted ascending by `TaskToken`** for `ActiveTasks`, and the
  **first** such match in that order for `ActiveTask`.

Rejected alternatives (from brainstorming):
- *Concrete-struct-only methods* — not reachable through the interface; poor
  discoverability.
- *Separate capability interface* (`ActiveTasker`) — non-breaking but adds a second
  concept; the user explicitly wants the methods on `ProcessInstance`.
- *Free functions* — non-breaking but the user explicitly wants methods on the type.
- *`ActiveTask` returning a slice or `(task, error)`* — rejected: the engine has no
  multi-instance support, so one-open-task-per-node is the real contract;
  `(HumanTask, bool)` is the ergonomic fit, with deterministic first-match as the
  defensive fallback.

## Testing (TDD, hot-path-first)

Black-box tests in `service_test` (prefer `service_test` package). Table-driven
per the project `table-test` skill (`assert` closure form). Cases:

- **`ActiveTask`**
  - node with one Unclaimed task → returns it, `true`.
  - node with one Claimed task → returns it, `true`.
  - node whose task is Completed → `false` (zero value).
  - node whose task is Cancelled → `false`.
  - node whose task has an **out-of-range** `TaskState` (e.g. `TaskState(999)`) →
    `false` (locks "only Unclaimed/Claimed are active").
  - unknown node id → `false`.
  - node with **two** open tasks (degenerate/pathological state constructed
    directly) → returns the first in task-token order, `true` (documents the
    deterministic fallback).
- **`ActiveTasks`**
  - mix of Unclaimed/Claimed/Completed/Cancelled across several nodes → returns
    only the open ones, sorted by task-token.
  - instance with no tasks → non-nil empty slice (`len == 0`, `!= nil`).
  - instance with only resolved (Completed/Cancelled) tasks → non-nil empty slice.
  - **lexicographic-ordering guard:** open tasks with realistic tokens crossing
    the 9→10 boundary (`inst-1-h2`, `inst-1-h10`) → asserts `inst-1-h10` sorts
    **before** `inst-1-h2`, locking the lexicographic (not numeric/creation)
    contract so a future refactor can't silently change it.
- **Consistency with `State()`:** the tasks returned by `ActiveTasks` equal the
  open subset of `State().Tasks` (same records, same task-token order); a matching
  `ActiveTask(nodeID)` result equals the corresponding `State().Tasks` entry. (We
  do **not** assert non-aliasing — the accessors intentionally alias the snapshot
  exactly as `State()` does; see the Aliasing note above.)
- A **testable example** (`Example...`) demonstrating both methods for the
  library-facing docs (Go rule #6).

Coverage: ≥ 85% floor on `service`; the projection logic and all `IsOpen` state
branches (the hot path) covered first.

## Deliverable bundle (one commit)

- Implementation: `ProcessInstance` interface + `processInstance` methods.
- Tests + example.
- **ADR-0142** (`docs/adr/0142-processinstance-active-task-accessors.md`, Nygard).
- This spec.
- Plan (`docs/plans/`).
- CHANGELOG entry for the breaking interface addition.

Delivery Gate: Verification (tests + ≥85% + clean lint, no cross-repo regressions)
→ `/code-review` → `/security-review` (fold via `--amend`) → merge `--no-ff`.
