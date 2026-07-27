# ProcessInstance active-task accessors — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `ActiveTask(nodeID) (humantask.HumanTask, bool)` and `ActiveTasks() []humantask.HumanTask` to the `service.ProcessInstance` interface, returning the open (Unclaimed|Claimed) human tasks from the instance snapshot.

**Architecture:** Both methods are pure projections over the already-materialized `p.st.Tasks` (`engine.InstanceState.Tasks`) — filter by `humantask.IsOpen()`, sort by `TaskToken`. No `TaskStore` call, no `context`, no `error`. Methods are added to the `ProcessInstance` interface (breaking, ADR-0142) and implemented on the sole `processInstance` struct.

**Tech Stack:** Go 1.25, `stretchr/testify`, black-box `service_test` package, `slices.SortFunc` + `cmp.Compare`.

## Global Constraints

- Go 1.25; strict Go idioms.
- **TDD strict** (CLAUDE.md): every new method gets a failing test run (RED) via `go test ./service/...` BEFORE implementation. The RED is a visible Bash `go test` failure.
- Table-driven tests use the project `table-test` skill form: an `assert func(t *testing.T, ...)` closure field (NOT `want`/`wantErr`), `t.Context()` over `context.Background()`. Existing `service/instance_test.go` already uses this form — match it.
- Black-box tests: `package service_test`.
- Return type element is `humantask.HumanTask` (public root package — no new coupling).
- `ActiveTasks` returns a **non-nil** empty slice when none open.
- `ActiveTask` returns the **first** open match in **ascending lexicographic `TaskToken` order** (deterministic; NOT creation order — `<InstanceID>-h10` sorts before `…-h2`), or `(zero, false)`.
- Ordering matches the `TaskStore` "sorted by TaskToken" contract. `TaskState` zero value is `Unclaimed` (open); out-of-range `TaskState` is not open.
- Accessors return a **freshly allocated** outer slice (not aliased to `p.st.Tasks`); each element is a **shallow value copy** whose ref-fields (`Candidates`, `Eligibility.*`, `Vars`) alias the snapshot exactly as `State().Tasks[i]` does — no deep-copy.
- `MarshalJSON` / `instanceJSON` MUST remain unchanged.
- **Feature-bundle commit** (CLAUDE.md git discipline): all work folds into ONE commit on branch `feat/processinstance-active-tasks` via `git commit --amend`. Do NOT stack separate commits. The branch already has a spec commit to amend into.
- Verification floor: `service` package ≥ 85% line coverage, `go test ./...` clean, `golangci-lint run ./...` clean.

## File Structure

- Modify: `service/instance.go` — add the two methods to the `ProcessInstance` interface (`:17`) and implement them on `processInstance` (`:29`). Add imports `cmp`, `slices`, and `github.com/kartaladev/wrkflw/humantask`.
- Test: `service/instance_test.go` — extend the existing black-box suite with table-driven tests for both methods plus a testable `Example`.
- Modify: `CHANGELOG.md` — breaking-change entry (see Task 3).

---

### Task 1: `ActiveTasks()` — all open tasks

**Files:**
- Modify: `service/instance.go` (interface `:17`, struct impl after `:35`)
- Test: `service/instance_test.go`

**Interfaces:**
- Consumes: `engine.InstanceState.Tasks []humantask.HumanTask` (`engine/state.go:159`); `humantask.HumanTask` with fields `TaskToken`, `NodeID`, `State`; `humantask.HumanTask.IsOpen() bool`; `humantask.TaskState` consts `Unclaimed`, `Claimed`, `Completed`, `Cancelled`.
- Produces: `ProcessInstance.ActiveTasks() []humantask.HumanTask` (non-nil; open tasks sorted by `TaskToken`).

- [ ] **Step 1: Write the failing test**

Add to `service/instance_test.go`. Add `"github.com/kartaladev/wrkflw/humantask"` to the import block.

```go
// TestProcessInstanceActiveTasks verifies ActiveTasks returns only open
// (Unclaimed|Claimed) tasks, sorted by TaskToken, as a non-nil slice.
func TestProcessInstanceActiveTasks(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		tasks  []humantask.HumanTask
		assert func(t *testing.T, got []humantask.HumanTask)
	}

	cases := []testCase{
		{
			name:  "no tasks yields non-nil empty slice",
			tasks: nil,
			assert: func(t *testing.T, got []humantask.HumanTask) {
				assert.NotNil(t, got)
				assert.Empty(t, got)
			},
		},
		{
			name: "only resolved tasks yields non-nil empty slice",
			tasks: []humantask.HumanTask{
				{TaskToken: "t-1", NodeID: "n-1", State: humantask.Completed},
				{TaskToken: "t-2", NodeID: "n-2", State: humantask.Cancelled},
			},
			assert: func(t *testing.T, got []humantask.HumanTask) {
				assert.NotNil(t, got)
				assert.Empty(t, got)
			},
		},
		{
			name: "returns open tasks sorted by task token",
			tasks: []humantask.HumanTask{
				{TaskToken: "t-3", NodeID: "n-3", State: humantask.Claimed, ClaimedBy: "u-b"},
				{TaskToken: "t-1", NodeID: "n-1", State: humantask.Unclaimed},
				{TaskToken: "t-2", NodeID: "n-2", State: humantask.Completed},
			},
			assert: func(t *testing.T, got []humantask.HumanTask) {
				require.Len(t, got, 2)
				assert.Equal(t, "t-1", got[0].TaskToken)
				assert.Equal(t, humantask.Unclaimed, got[0].State)
				assert.Equal(t, "t-3", got[1].TaskToken)
				assert.Equal(t, humantask.Claimed, got[1].State)
			},
		},
		{
			// Locks the ORDER contract as lexicographic (NOT numeric/creation):
			// with realistic <InstanceID>-hN tokens, "i-1-h10" sorts BEFORE
			// "i-1-h2". A future switch to numeric/creation order would break this.
			name: "ordering is lexicographic by task token (h10 before h2)",
			tasks: []humantask.HumanTask{
				{TaskToken: "i-1-h2", NodeID: "n-a", State: humantask.Unclaimed},
				{TaskToken: "i-1-h10", NodeID: "n-b", State: humantask.Claimed},
			},
			assert: func(t *testing.T, got []humantask.HumanTask) {
				require.Len(t, got, 2)
				assert.Equal(t, "i-1-h10", got[0].TaskToken)
				assert.Equal(t, "i-1-h2", got[1].TaskToken)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := engine.InstanceState{InstanceID: "i-1", Status: engine.StatusRunning, Tasks: tc.tasks}
			pi := service.NewProcessInstance(nil, st)
			tc.assert(t, pi.ActiveTasks())
		})
	}
}

// TestProcessInstanceActiveTasksConsistentWithState verifies ActiveTasks returns
// exactly the open subset of State().Tasks, in the same TaskToken order — the
// consistency contract from the spec.
func TestProcessInstanceActiveTasksConsistentWithState(t *testing.T) {
	t.Parallel()

	st := engine.InstanceState{
		InstanceID: "i-1",
		Status:     engine.StatusRunning,
		Tasks: []humantask.HumanTask{
			{TaskToken: "i-1-h3", NodeID: "n-c", State: humantask.Completed},
			{TaskToken: "i-1-h1", NodeID: "n-a", State: humantask.Unclaimed},
			{TaskToken: "i-1-h2", NodeID: "n-b", State: humantask.Claimed},
		},
	}
	pi := service.NewProcessInstance(nil, st)

	// Independently derive the expected open subset from State().Tasks.
	var want []humantask.HumanTask
	for _, task := range pi.State().Tasks {
		if task.IsOpen() {
			want = append(want, task)
		}
	}
	slices.SortFunc(want, func(a, b humantask.HumanTask) int {
		return cmp.Compare(a.TaskToken, b.TaskToken)
	})

	assert.Equal(t, want, pi.ActiveTasks())
}
```

This test's `import` block additions: `"cmp"` and `"slices"` (stdlib) — add them
to `service/instance_test.go` alongside `humantask`. (Task 2 adds the matching
`ActiveTask`-vs-`State()` consistency assertion.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./service/... -run '^TestProcessInstanceActiveTasks'`
Expected: build FAILS — `pi.ActiveTasks undefined (type service.ProcessInstance has no field or method ActiveTasks)`.

- [ ] **Step 3: Write minimal implementation**

In `service/instance.go`, add imports `cmp` and `slices` to the stdlib group and `"github.com/kartaladev/wrkflw/humantask"` to the module group. Add to the `ProcessInstance` interface (after the `json.Marshaler` line):

```go
	// ActiveTasks returns every open human task (Unclaimed or Claimed;
	// humantask.IsOpen) in the instance, sorted by TaskToken in ascending
	// lexicographic order. Never nil: an instance with no open tasks yields a
	// non-nil empty slice.
	ActiveTasks() []humantask.HumanTask
```

Add the method on `processInstance` (after `State()` at `:35`):

```go
func (p processInstance) ActiveTasks() []humantask.HumanTask {
	out := make([]humantask.HumanTask, 0, len(p.st.Tasks))
	for _, t := range p.st.Tasks {
		if t.IsOpen() {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b humantask.HumanTask) int {
		return cmp.Compare(a.TaskToken, b.TaskToken)
	})
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./service/... -run '^TestProcessInstanceActiveTasks'`
Expected: PASS (both the table test and `...ConsistentWithState`).

- [ ] **Step 5: Fold into the feature bundle**

```bash
git add service/instance.go service/instance_test.go
git commit --amend --no-edit
```

---

### Task 2: `ActiveTask(nodeID)` — open task at a node

**Files:**
- Modify: `service/instance.go` (interface `:17`, struct impl)
- Test: `service/instance_test.go`

**Interfaces:**
- Consumes: same as Task 1, plus `ProcessInstance.ActiveTasks()` (reuse it so ordering/filter logic is defined once).
- Produces: `ProcessInstance.ActiveTask(nodeID string) (humantask.HumanTask, bool)` — first open task at `nodeID` in `TaskToken` order, or `(zero, false)`.

- [ ] **Step 1: Write the failing test**

Add to `service/instance_test.go`:

```go
// TestProcessInstanceActiveTask verifies ActiveTask returns the open task at a
// node (Unclaimed or Claimed), false for resolved/unknown nodes, and the first
// in task-token order when a pathological state has more than one open task at
// the same node.
func TestProcessInstanceActiveTask(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		nodeID string
		tasks  []humantask.HumanTask
		assert func(t *testing.T, got humantask.HumanTask, ok bool)
	}

	cases := []testCase{
		{
			name:   "unclaimed task at node",
			nodeID: "approve",
			tasks:  []humantask.HumanTask{{TaskToken: "t-1", NodeID: "approve", State: humantask.Unclaimed}},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.True(t, ok)
				assert.Equal(t, "t-1", got.TaskToken)
				assert.Equal(t, humantask.Unclaimed, got.State)
			},
		},
		{
			name:   "claimed task at node",
			nodeID: "approve",
			tasks:  []humantask.HumanTask{{TaskToken: "t-1", NodeID: "approve", State: humantask.Claimed, ClaimedBy: "u-jane"}},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.True(t, ok)
				assert.Equal(t, "u-jane", got.ClaimedBy)
			},
		},
		{
			name:   "completed task at node is not active",
			nodeID: "approve",
			tasks:  []humantask.HumanTask{{TaskToken: "t-1", NodeID: "approve", State: humantask.Completed}},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.False(t, ok)
				assert.Zero(t, got)
			},
		},
		{
			name:   "cancelled task at node is not active",
			nodeID: "approve",
			tasks:  []humantask.HumanTask{{TaskToken: "t-1", NodeID: "approve", State: humantask.Cancelled}},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.False(t, ok)
			},
		},
		{
			// Locks the "only Unclaimed|Claimed are active" contract: an
			// out-of-range TaskState is not open, so it is never returned.
			name:   "out-of-range task state is not active",
			nodeID: "approve",
			tasks:  []humantask.HumanTask{{TaskToken: "t-1", NodeID: "approve", State: humantask.TaskState(999)}},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.False(t, ok)
			},
		},
		{
			name:   "unknown node id",
			nodeID: "missing",
			tasks:  []humantask.HumanTask{{TaskToken: "t-1", NodeID: "approve", State: humantask.Unclaimed}},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.False(t, ok)
			},
		},
		{
			name:   "two open tasks at same node returns first in token order",
			nodeID: "approve",
			tasks: []humantask.HumanTask{
				{TaskToken: "t-2", NodeID: "approve", State: humantask.Claimed},
				{TaskToken: "t-1", NodeID: "approve", State: humantask.Unclaimed},
			},
			assert: func(t *testing.T, got humantask.HumanTask, ok bool) {
				assert.True(t, ok)
				assert.Equal(t, "t-1", got.TaskToken)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := engine.InstanceState{InstanceID: "i-1", Status: engine.StatusRunning, Tasks: tc.tasks}
			pi := service.NewProcessInstance(nil, st)
			got, ok := pi.ActiveTask(tc.nodeID)
			tc.assert(t, got, ok)
		})
	}
}

// TestProcessInstanceActiveTaskConsistentWithState verifies a matched ActiveTask
// equals the corresponding open State().Tasks entry — the spec consistency
// contract for the by-node accessor.
func TestProcessInstanceActiveTaskConsistentWithState(t *testing.T) {
	t.Parallel()

	st := engine.InstanceState{
		InstanceID: "i-1",
		Status:     engine.StatusRunning,
		Tasks: []humantask.HumanTask{
			{TaskToken: "i-1-h1", NodeID: "approve", State: humantask.Claimed, ClaimedBy: "u-jane"},
		},
	}
	pi := service.NewProcessInstance(nil, st)

	got, ok := pi.ActiveTask("approve")
	require.True(t, ok)
	assert.Equal(t, pi.State().Tasks[0], got)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./service/... -run '^TestProcessInstanceActiveTask'`
Expected: build FAILS — `pi.ActiveTask undefined`.

- [ ] **Step 3: Write minimal implementation**

Add to the `ProcessInstance` interface (after `ActiveTasks`):

```go
	// ActiveTask returns the open human task at nodeID and true, or the zero
	// HumanTask and false if the node has no open task. "Open" means Unclaimed or
	// Claimed (humantask.IsOpen). A well-formed graph has at most one open task
	// per node; if a pathological definition produces more than one, the first in
	// ascending TaskToken order is returned.
	ActiveTask(nodeID string) (humantask.HumanTask, bool)
```

Add the method on `processInstance`:

```go
func (p processInstance) ActiveTask(nodeID string) (humantask.HumanTask, bool) {
	for _, t := range p.ActiveTasks() { // already open-filtered and token-sorted
		if t.NodeID == nodeID {
			return t, true
		}
	}
	return humantask.HumanTask{}, false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./service/... -run '^TestProcessInstanceActiveTask'`
Expected: PASS (both the table test and `...ConsistentWithState`).

- [ ] **Step 5: Fold into the feature bundle**

```bash
git add service/instance.go service/instance_test.go
git commit --amend --no-edit
```

---

### Task 3: Testable example, CHANGELOG, and Delivery Gate

**Files:**
- Test: `service/instance_test.go` (add `Example`)
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: `service.NewProcessInstance`, `ProcessInstance.ActiveTask`, `ProcessInstance.ActiveTasks`, `engine.InstanceState`, `humantask.HumanTask`.
- Produces: nothing new (docs + gate).

- [ ] **Step 1: Write the testable example**

This is a documentation example authored *after* the methods exist (Tasks 1-2),
so it is **not** a RED step — per CLAUDE.md "What Counts as New Behaviour," a pure
test/doc addition needs no failing state. It documents the already-implemented
behavior and is verified by `go test` via its `// Output:` block.

Add to `service/instance_test.go`:

```go
// ExampleProcessInstance_activeTasks shows how a consumer reads the open human
// tasks of a running instance.
func ExampleProcessInstance_activeTasks() {
	st := engine.InstanceState{
		InstanceID: "inst-1",
		Status:     engine.StatusRunning,
		Tasks: []humantask.HumanTask{
			{TaskToken: "t-1", NodeID: "manager-approval", State: humantask.Claimed, ClaimedBy: "u-jane"},
			{TaskToken: "t-0", NodeID: "validate", State: humantask.Completed},
		},
	}
	inst := service.NewProcessInstance(nil, st)

	task, ok := inst.ActiveTask("manager-approval")
	fmt.Println(ok, task.ClaimedBy)
	fmt.Println(len(inst.ActiveTasks()))
	// Output:
	// true u-jane
	// 1
}
```

Add `"fmt"` to the import block if not present.

- [ ] **Step 2: Run to verify it passes**

Run: `go test ./service/... -run '^ExampleProcessInstance_activeTasks$'`
Expected: PASS — the methods exist from Tasks 1-2 and the example's `// Output:` block matches.

- [ ] **Step 3: Add the CHANGELOG entry**

In `CHANGELOG.md`, append a bullet to the existing
`### Breaking changes (pre-v0.1.0 — no stability promise)` subsection under
`## [Unreleased]` (match the existing bullet style — bold lead-in):

```markdown
- **`service.ProcessInstance` gains two methods** — `ActiveTask(nodeID string) (humantask.HumanTask, bool)`
  and `ActiveTasks() []humantask.HumanTask` — returning the open (Unclaimed|Claimed)
  human tasks of an instance, sorted ascending by `TaskToken` (ADR-0142). Consumers
  who **embed** a ProcessInstance obtained from the engine need no code change but
  must **recompile**; consumers with a **hand-rolled** implementation must add the
  two methods, filtering `State().Tasks` by `humantask.IsOpen()`, returning a
  **non-nil** slice **sorted by `TaskToken`** (`ActiveTasks`) and the **first** such
  match (`ActiveTask`).
```

- [ ] **Step 4: Full verification**

```bash
go test -race -coverprofile=cover.out ./service/... && go tool cover -func=cover.out | tail -1
go test ./...
golangci-lint run ./...
```
Expected: `service` tests pass, coverage ≥ 85%; full suite green; lint clean.

- [ ] **Step 5: Fold docs + reword the bundle commit**

Stage the ADR, spec (already committed — re-add if edited), plan, CHANGELOG, and code, then reword the amended bundle commit to a proper feat message:

```bash
git add service/instance.go service/instance_test.go CHANGELOG.md \
  docs/adr/0142-processinstance-active-task-accessors.md \
  docs/specs/2026-07-27-processinstance-active-tasks.md \
  docs/plans/2026-07-27-processinstance-active-tasks.md
git commit --amend -m "$(cat <<'EOF'
feat(service)!: ProcessInstance active-task accessors (ADR-0142)

Add ActiveTask(nodeID)(humantask.HumanTask,bool) and ActiveTasks()
[]humantask.HumanTask to the ProcessInstance interface, returning the
open (Unclaimed|Claimed) human tasks from the instance snapshot. Pure
projections over State().Tasks; JSON wire format unchanged.

BREAKING: hand-rolled ProcessInstance implementations must add the two
methods (pre-v0.1.0, no stability promise).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_016FzuYkwCwdXJ9VWchXAcB4
EOF
)"
```

- [ ] **Step 6: Delivery Gate**

Run `/code-review` then `/security-review` on the pending change; fold any accepted findings via `git commit --amend`. Then merge to `main`:

```bash
git checkout main && git merge --no-ff feat/processinstance-active-tasks
```

## Verification checklist

- [ ] `ActiveTasks()` returns only open tasks, sorted ascending by `TaskToken`, non-nil when empty.
- [ ] `ActiveTask(nodeID)` returns first open task at node in `TaskToken` order; `(zero, false)` for resolved/unknown/out-of-range-state/unknown-node.
- [ ] Ordering is **lexicographic** — guard test asserts `inst-1-h10` sorts before `inst-1-h2`.
- [ ] Out-of-range `TaskState` is not returned (contract-lock test present).
- [ ] Consistency tests: `ActiveTasks()` == open subset of `State().Tasks`; matched `ActiveTask` == corresponding `State().Tasks` entry.
- [ ] Both methods are on the `ProcessInstance` interface AND implemented on `processInstance`.
- [ ] `MarshalJSON` output byte-identical to before (no `instanceJSON` change).
- [ ] Testable `Example` present and passing.
- [ ] Every new method had a visible RED `go test` failure before implementation (the `Example` is a post-impl doc addition, not a RED step).
- [ ] `service` coverage ≥ 85%; `go test ./...` clean; `golangci-lint run ./...` clean.
- [ ] CHANGELOG breaking-change entry added.
- [ ] One feature-bundle commit (impl + tests + ADR-0142 + spec + plan + CHANGELOG); `/code-review` + `/security-review` clean.
