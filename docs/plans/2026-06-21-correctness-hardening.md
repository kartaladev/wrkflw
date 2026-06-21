# Correctness & tests hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the nested-scope compensation MUST-FIX, add mixed split+join gateway validation, introduce a typed wrong-state error mapped to HTTP 422 / gRPC FailedPrecondition, and close two test gaps (parked-async Postgres resume e2e, inner-scope topology tests).

**Architecture:** Five independent work-streams. A (engine) hoists a closing sub-process scope's compensation records into its parent so the existing root rollback walk reaches them — no new state. B adds one `model.Validate` rule. C adds a `service.ErrConflict` sentinel classified at the service seam and mapped by both transports. D and E are test-only additions.

**Tech Stack:** Go 1.25.7, stdlib, `expr-lang/expr` (existing), pgx + testcontainers (existing), `clockwork` fake clock (existing).

## Global Constraints

- Module `github.com/zakyalvan/krtlwrkflw`. Go 1.25.7.
- **Engine purity:** `engine` and `model` import only stdlib (+ `model`/`authz`/`humantask`/`expreval`). No transport/storage/bus/time-vendor in the core. `Step` stays deterministic (IDs from `InstanceState` counters; slice-order iteration; no map iteration into command/record order) and pure (never mutates its input `InstanceState`; extend `cloneState` for any new state field — **this plan adds no new `InstanceState` field**).
- **TDD strict** (CLAUDE.md "TDD Operational Discipline"): every new symbol and behavioural change gets a failing test FIRST with a visible RED (`go test` showing build-fail or assertion-fail) before the implementation. **Bug fixes get a regression test that reproduces the bug first.** A `Write` of a test file immediately followed by the impl with no `go test` between them is forbidden.
- **Tests:** black-box (`package <pkg>_test`); table tests use the project `table-test` skill's **`assert` closure per case** (NOT `want`/`wantErr`); `t.Context()` not `context.Background()`; pair each `foo.go` with `foo_test.go`; Postgres tests use `database.RunTestDatabase` (testcontainers), never mocked.
- **Verify per task:** `go test -race ./<touched-pkg>/...` green; on completion ≥85% line coverage on touched packages, `golangci-lint run ./...` clean (v2 config).
- **Commits:** Conventional Commits scoped to the area (`fix(engine)`, `feat(model)`, `feat(service)`, `test(...)`), ending with the trailer `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`. Commit per task.
- Branch `feat/correctness-hardening` (already created; spec + ADR-0013/0014 already committed there).
- **Hard-won lesson:** the engine grew large; the plan's example code can drift. Trust the test and the then-current code over a plan listing — observe the RED state, ground every edit against the actual file.

---

### Task 1: Nested-scope compensation hoist (MUST-FIX, ADR-0013)

Hoist a closing sub-process scope's `Compensations` into its parent on normal exit, before `closeScope`, so completed-sub-process activities remain rollback-able via the existing root `CompensateRequested` walk.

**Files:**
- Modify: `engine/state.go` (add `hoistCompensations` helper near `closeScope`)
- Modify: `engine/step.go` (sub-process normal-exit path: call hoist before `closeScope`)
- Modify: `engine/command.go` + `engine/trigger.go` (correct the now-inaccurate `Compensate` / `CompensateRequested` limitation godoc)
- Test: `engine/step_compensation_test.go` (add regression + ordering + nested tests, following this file's existing driving pattern)

**Interfaces:**
- Produces: `func (s *InstanceState) hoistCompensations(childID, parentID string)` (unexported; used only inside `engine`).

- [ ] **Step 1: Write the failing regression test**

Add to `engine/step_compensation_test.go`, following the existing tests' pattern in that file (they drive `Step` directly through the sub-process lifecycle, then deliver `CompensateRequested`). The new test builds a definition with a **compensable ServiceTask inside a sub-process**, runs the sub-process to completion so its scope closes, keeps the instance running (a root user task or a second root activity), then delivers `engine.NewCompensateRequested(at, "")` and asserts the inner activity's compensating `InvokeAction` (its `CompensationAction` name) is emitted.

Use the existing helper `compensableSubProcessDef()` (already in this file — a compensable task inside a sub-process) if its shape fits; otherwise add a sibling helper `compensableSubThenRootDef()` that appends a root-level parked node after the sub-process so the instance is still `StatusRunning` when `CompensateRequested` arrives. The assertion core:

```go
// After the sub-process has completed and its scope closed, a full
// CompensateRequested must still compensate the inner activity (hoisted to root).
res, err := engine.Step(def, st, engine.NewCompensateRequested(at, ""), engine.StepOptions{})
require.NoError(t, err)
// The first compensation InvokeAction must target the inner task's compensation action.
var gotActions []string
for _, c := range res.Commands {
    if ia, ok := c.(engine.InvokeAction); ok {
        gotActions = append(gotActions, ia.Name)
    }
}
require.Contains(t, gotActions, "compensate-inner",
    "inner sub-process activity must be rollback-able after the scope closed")
```

(Match `compensable*Def`'s actual `CompensationAction` string to whatever the helper sets — read the helper.)

- [ ] **Step 2: Run test to verify it fails (RED)**

Run: `go test -run '^TestCompensateRequested' ./engine/...` (include your new test's name)
Expected: FAIL — the inner compensation action is NOT emitted, because the record was dropped on `closeScope` (the bug). This is the reproduction.

- [ ] **Step 3: Add the `hoistCompensations` helper**

In `engine/state.go`, immediately above `closeScope`, add:

```go
// hoistCompensations moves childID's accumulated compensation records into its
// parent (parentID), appended in completion order, so they remain rollback-able
// after the child scope closes. parentID "" targets RootCompensations. The
// child's own slice is cleared. No-op if the child has no records or is not found.
func (s *InstanceState) hoistCompensations(childID, parentID string) {
	child := s.scopeByID(childID)
	if child == nil || len(child.Compensations) == 0 {
		return
	}
	if parentID == "" {
		s.RootCompensations = append(s.RootCompensations, child.Compensations...)
	} else if parent := s.scopeByID(parentID); parent != nil {
		parent.Compensations = append(parent.Compensations, child.Compensations...)
	}
	child.Compensations = nil
}
```

- [ ] **Step 4: Call hoist before closeScope on the sub-process exit path**

In `engine/step.go`, in the sub-process normal-exit path, find the bare `s.closeScope(currentScopeID)` call (preceded by the comment "Scope drained (and no active children): close it and resume in parent."). Insert the hoist immediately before it:

```go
	s.hoistCompensations(currentScopeID, parentScopeID)
	s.closeScope(currentScopeID)
```

`parentScopeID` is already in scope at that point (it is used a few lines later in `defForScope(def, s, parentScopeID)`). Do not change the subsequent "if the sub-process node itself carries a CompensationAction → recordCompensation(parentScopeID, …)" block — it correctly appends the sub-process node's own compensation after the hoisted child records.

- [ ] **Step 5: Run tests to verify GREEN (and no regression)**

Run: `go test -race ./engine/...`
Expected: PASS — the new regression test passes; all existing compensation/scope tests still pass.

- [ ] **Step 6: Add the ordering + nested-depth tests**

Add two more tests to `engine/step_compensation_test.go`:
1. **Ordering:** a root compensable activity, then a sub-process with a compensable inner activity, then a root parked node. After full `CompensateRequested`, assert the emitted compensation `InvokeAction`s occur in reverse completion order (sub-process node's own comp if any, then inner activity, then the earlier root activity) — assert the ordered `gotActions` slice equals the expected reverse order.
2. **Two-level nesting:** a sub-process containing a sub-process containing a compensable activity; after both scopes close and a full `CompensateRequested`, assert the grandchild activity's compensation is emitted (proves induction).

Run: `go test -race ./engine/...` → PASS.

- [ ] **Step 7: Correct the limitation godoc**

In `engine/trigger.go` (the `CompensateRequested` doc block) and `engine/command.go` (the `Compensate` doc block), replace the "root scope only / records of completed sub-process scopes are dropped / not yet rollback-able" wording with the accurate statement: completed sub-process compensation records are **hoisted into the parent on scope close** and are reachable by the root `CompensateRequested` walk in reverse order; `Compensate{ScopeID,FromNode}` remains **reserved** for future *scope-targeted* compensation (which needs a producer — a BPMN compensation boundary/throw event — not yet built). Keep it factual; do not claim scope-targeting works.

- [ ] **Step 8: Audit other closeScope callers (no behavioural change)**

Grep `engine/step.go` for every `closeScope(` call. Confirm in your report which call sites are the **normal sub-process exit** (gets the hoist) vs **error-propagation / cancel** paths (intentionally NOT changed — compensation-on-error has different semantics, out of scope per ADR-0013). Do not modify the error/cancel paths. This is an audit deliverable, not a code change.

- [ ] **Step 9: Commit**

```bash
git add engine/state.go engine/step.go engine/command.go engine/trigger.go engine/step_compensation_test.go
git commit -m "$(printf 'fix(engine): hoist sub-process compensation records into parent on close\n\nA completed sub-process scope dropped its compensation records on closeScope,\nso nested-saga rollback missed them. Hoist them into the parent (root) in\ncompletion order before closing, so the existing root CompensateRequested walk\nrolls them back. No new InstanceState field. Corrects the Compensate/\nCompensateRequested limitation godoc. ADR-0013.\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 2: Mixed split+join gateway validation (ADR-0014)

Reject a gateway node with both >1 incoming and >1 outgoing flows.

**Files:**
- Modify: `model/validate.go` (add `ErrMixedGateway` sentinel + the rule)
- Test: `model/validate_test.go` (table cases)

**Interfaces:**
- Produces: `var model.ErrMixedGateway = errors.New("model: gateway both splits and joins")`.

- [ ] **Step 1: Write the failing test**

Add cases to the existing `TestValidate` table in `model/validate_test.go` (same `map[string]struct{ def; assert }` shape):

```go
"mixed gateway both splits and joins": {
	def: &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "a", Kind: model.KindServiceTask, Action: "a"},
			{ID: "b", Kind: model.KindServiceTask, Action: "b"},
			{ID: "gw", Kind: model.KindExclusiveGateway},
			{ID: "c", Kind: model.KindServiceTask, Action: "c"},
			{ID: "d", Kind: model.KindServiceTask, Action: "d"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f0", Source: "start", Target: "a"},
			{ID: "f0b", Source: "start", Target: "b"}, // start splits to a and b
			{ID: "f1", Source: "a", Target: "gw"},
			{ID: "f2", Source: "b", Target: "gw"},      // gw has 2 incoming
			{ID: "f3", Source: "gw", Target: "c"},
			{ID: "f4", Source: "gw", Target: "d"},      // gw has 2 outgoing  → mixed
			{ID: "f5", Source: "c", Target: "end"},
			{ID: "f6", Source: "d", Target: "end"},
		},
	},
	assert: func(t *testing.T, err error) {
		require.ErrorIs(t, err, model.ErrMixedGateway)
	},
},
"pure split gateway is valid": {
	def: &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "gw", Kind: model.KindParallelGateway},
			{ID: "c", Kind: model.KindServiceTask, Action: "c"},
			{ID: "d", Kind: model.KindServiceTask, Action: "d"},
			{ID: "j", Kind: model.KindParallelGateway},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "gw"},
			{ID: "f2", Source: "gw", Target: "c"},
			{ID: "f3", Source: "gw", Target: "d"},
			{ID: "f4", Source: "c", Target: "j"},
			{ID: "f5", Source: "d", Target: "j"},
			{ID: "f6", Source: "j", Target: "end"},
		},
	},
	assert: func(t *testing.T, err error) {
		require.NoError(t, err)
	},
},
```

Also add a case nesting a mixed gateway inside a sub-process and asserting `require.ErrorIs(t, err, model.ErrMixedGateway)` (use the existing `validSubprocessDef` pattern but inject a mixed gateway into the inner def).

- [ ] **Step 2: Run test to verify it fails (RED)**

Run: `go test -run '^TestValidate$' ./model/...`
Expected: FAIL — `model.ErrMixedGateway` undefined (build error), then once defined, the "mixed" case fails because no rule rejects it yet.

- [ ] **Step 3: Add the sentinel and the rule**

In `model/validate.go`, add to the sentinel `var (...)` block:

```go
	ErrMixedGateway = errors.New("model: gateway both splits and joins")
```

Then add a new rule loop inside `validate` (after the existing event-based-gateway loop, before the sub-process recursion). The set of gateway kinds:

```go
	gatewayKinds := map[NodeKind]bool{
		KindExclusiveGateway: true,
		KindInclusiveGateway: true,
		KindParallelGateway:  true,
		KindEventBasedGateway: true,
	}
	for _, n := range d.Nodes {
		if !gatewayKinds[n.Kind] {
			continue
		}
		if len(d.Incoming(n.ID)) > 1 && len(d.Outgoing(n.ID)) > 1 {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrMixedGateway, n.ID))
		}
	}
```

The existing sub-process recursion already revalidates nested definitions, so the nested-mixed-gateway case is covered without extra code.

- [ ] **Step 4: Run tests to verify GREEN**

Run: `go test -race ./model/...`
Expected: PASS (new cases pass; all existing `Validate` cases still pass).

- [ ] **Step 5: Commit**

```bash
git add model/validate.go model/validate_test.go
git commit -m "$(printf 'feat(model): reject mixed split+join gateways in Validate\n\nA gateway with both >1 incoming and >1 outgoing flows is structurally\nambiguous and routes silently. Validate now returns ErrMixedGateway for\nany such gateway (recursively into sub-processes). ADR-0014.\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 3: `service.ErrConflict` wrong-state classification

Classify wrong-state task and instance operations at the service seam.

**Files:**
- Create: `service/errors.go` (`ErrConflict` sentinel + `isTerminal` helper)
- Modify: `service/service.go` (`deliverTaskTrigger`, `DeliverSignal`)
- Test: `service/errors_test.go` (or extend the existing service test file)

**Interfaces:**
- Produces: `var service.ErrConflict = errors.New("service: conflicting state")`.
- Consumes (Task 4): `errors.Is(err, service.ErrConflict)`.

- [ ] **Step 1: Write the failing test**

Create `service/errors_test.go` (black-box `package service_test`). Build an `Engine` over `runtime.NewMemStore()` + a `humantask.NewMemTaskStore()` (follow the existing service test's construction — read `service/*_test.go` for the exact wiring helper). Two cases:

```go
// Claiming a task that is already Completed → ErrConflict.
// (Seed the task store with a Completed task whose InstanceID exists.)
_, err := svc.ClaimTask(t.Context(), service.ClaimTaskRequest{TaskToken: tok, Actor: actor})
require.ErrorIs(t, err, service.ErrConflict)

// Delivering a signal to a terminal (Completed) instance → ErrConflict.
// (Seed the store with a Completed instance state.)
_, err = svc.DeliverSignal(t.Context(), service.DeliverSignalRequest{InstanceID: id, Signal: "go"})
require.ErrorIs(t, err, service.ErrConflict)
```

If wiring a full `Engine` in the test is heavy, instead unit-test the two pure helpers and the classification by constructing the minimal `Engine` the existing service tests already use. Read the existing service test setup first and reuse it.

- [ ] **Step 2: Run test to verify it fails (RED)**

Run: `go test ./service/...`
Expected: FAIL — `service.ErrConflict` undefined (build error), then once defined, the operations do not yet return it.

- [ ] **Step 3: Add the sentinel and terminal helper**

Create `service/errors.go`:

```go
package service

import (
	"errors"

	"github.com/zakyalvan/krtlwrkflw/engine"
)

// ErrConflict classifies a wrong-state operation — one targeting an instance or
// task that is not in a state where the operation is valid (e.g. claiming a
// completed task, delivering a signal to a finished instance). Transports map it
// to HTTP 422 / gRPC FailedPrecondition. The cause is wrapped, so
// errors.Is(err, ErrConflict) holds while the cause stays inspectable.
var ErrConflict = errors.New("service: conflicting state")

// isTerminal reports whether an instance status rejects further triggers.
func isTerminal(s engine.Status) bool {
	return s == engine.StatusCompleted || s == engine.StatusFailed || s == engine.StatusTerminated
}
```

- [ ] **Step 4: Classify in `deliverTaskTrigger` and `DeliverSignal`**

In `service/service.go`, update `deliverTaskTrigger` to reject a closed task and a terminal instance (it already loads both):

```go
func (e *Engine) deliverTaskTrigger(ctx context.Context, taskToken string, trg engine.Trigger) (engine.InstanceState, error) {
	task, err := e.taskStore.Get(ctx, taskToken)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("service: deliver task trigger: get task: %w", err)
	}
	if !task.IsOpen() {
		return engine.InstanceState{}, fmt.Errorf("%w: task %q is not open", ErrConflict, taskToken)
	}
	def, st, err := e.resolveDefinition(ctx, task.InstanceID)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("service: deliver task trigger: resolve definition: %w", err)
	}
	if isTerminal(st.Status) {
		return engine.InstanceState{}, fmt.Errorf("%w: instance %q is in a terminal state", ErrConflict, task.InstanceID)
	}
	newSt, err := e.runner.Deliver(ctx, def, task.InstanceID, trg)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("service: deliver task trigger: deliver: %w", err)
	}
	return newSt, nil
}
```

(Note: `resolveDefinition` already returns `st`; capture it instead of discarding with `_`.)

In `DeliverSignal`, add the terminal check after `resolveDefinition`:

```go
	def, st, err := e.resolveDefinition(ctx, req.InstanceID)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("service: deliver signal: %w", err)
	}
	if isTerminal(st.Status) {
		return engine.InstanceState{}, fmt.Errorf("%w: instance %q is in a terminal state", ErrConflict, req.InstanceID)
	}
	trg := engine.NewSignalReceived(e.clk.Now(), req.Signal, req.Payload)
	// ... unchanged
```

- [ ] **Step 5: Run tests to verify GREEN**

Run: `go test -race ./service/...`
Expected: PASS (the two wrong-state cases now return `ErrConflict`; existing service tests still pass — a normal claim of an Open task and a signal to a Running instance are unaffected).

- [ ] **Step 6: Commit**

```bash
git add service/errors.go service/service.go service/errors_test.go
git commit -m "$(printf 'feat(service): classify wrong-state operations as ErrConflict\n\nClaiming/completing/reassigning a closed task, or delivering a signal to a\nterminal instance, now returns service.ErrConflict (wrapping the cause)\ninstead of a generic 500-mapped error. Classified at the service seam; the\nengine stays pure.\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 4: Map `ErrConflict` to 422 / FailedPrecondition

**Files:**
- Modify: `transport/rest/errors.go` (`classifyError`)
- Modify: `transport/grpc/errors.go` (`mapToGRPCStatus`)
- Test: `transport/rest/*_test.go`, `transport/grpc/*_test.go` (mapping cases)

**Interfaces:**
- Consumes: `service.ErrConflict` (Task 3).

- [ ] **Step 1: Write the failing tests**

In the REST error test (find the existing test covering `classifyError`/`WriteHTTPError`), add a case asserting `service.ErrConflict` → HTTP 422. If the existing test calls `WriteHTTPError` against a `httptest.ResponseRecorder`:

```go
"conflict maps to 422": {
	err:  fmt.Errorf("wrapped: %w", service.ErrConflict),
	code: http.StatusUnprocessableEntity,
},
```

In the gRPC error test, add a case asserting `mapToGRPCStatus(fmt.Errorf("x: %w", service.ErrConflict))` has `status.Code(...) == codes.FailedPrecondition`.

- [ ] **Step 2: Run tests to verify they fail (RED)**

Run: `go test ./transport/rest/... ./transport/grpc/...`
Expected: FAIL — `ErrConflict` currently falls through to the default (500 / `codes.Internal`).

- [ ] **Step 3: Add the REST mapping**

In `transport/rest/errors.go` `classifyError`, add a case (import `service` if not already imported):

```go
	case errors.Is(err, service.ErrConflict):
		return http.StatusUnprocessableEntity, "conflict_state"
```

Place it before the `default`. Keep the existing `runtime.ErrConcurrentUpdate` → 409 `"conflict"` case distinct (optimistic-concurrency is a different conflict).

- [ ] **Step 4: Add the gRPC mapping**

In `transport/grpc/errors.go` `mapToGRPCStatus`, add a case (import `service` if not already imported):

```go
	case errors.Is(err, service.ErrConflict):
		return status.Error(codes.FailedPrecondition, err.Error())
```

- [ ] **Step 5: Run tests to verify GREEN**

Run: `go test -race ./transport/rest/... ./transport/grpc/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add transport/rest/errors.go transport/grpc/errors.go transport/rest transport/grpc
git commit -m "$(printf 'feat(transport): map service.ErrConflict to 422 / FailedPrecondition\n\nWrong-state operations now surface as HTTP 422 (REST) and\ncodes.FailedPrecondition (gRPC) instead of 500 / Internal.\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 5: Parked-async Postgres resume e2e (highest-value missing test)

Prove a parked instance's snapshot survives a real DB reload through a fresh `Store` and resumes.

**Files:**
- Test: `internal/persistence/postgres/resume_test.go` (new, black-box `package postgres_test`)

**Interfaces:**
- Consumes: `database.RunTestDatabase`, `pg.Migrate`, `pg.NewStore`, `runtime.NewRunner`, `runtime.NewMemScheduler`, `clockwork.NewFakeClockAt`.

- [ ] **Step 1: Write the failing/exercising test**

Create `internal/persistence/postgres/resume_test.go`. It parks on an intermediate timer, persists to Postgres, builds a **fresh** `Store` over the same pool (simulating a restart), advances the fake clock, fires the timer through a runner on the fresh store, and asserts completion. Use a timer-intermediate definition (copy the shape of `timerIntermediateDef()` from `runtime/timer_example_test.go` — a start → intermediate-timer (`TimerDuration` e.g. `"PT1H"`) → service-task → end; define it inline in this test file since test helpers don't cross packages).

```go
func TestPostgresParkedTimerResumesAfterReload(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	startAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	ran := false
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"finish": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			ran = true
			return map[string]any{"done": true}, nil
		}),
	})

	def := timerResumeDef() // start → wait(PT1H) → finish(service) → end  (define inline)
	const id = "pg-resume-1"

	// Runner #1 over the Postgres store: start → park at the timer.
	store1 := pg.NewStore(pool)
	sched1 := runtime.NewMemScheduler(fc)
	r1 := runtime.NewRunner(cat, fc, store1, runtime.WithScheduler(sched1))
	parked, err := r1.Run(t.Context(), def, id, nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status)
	require.False(t, ran)

	// Simulate a process restart: a brand-new Store over the same pool. The only
	// surviving state is in Postgres.
	store2 := pg.NewStore(pool)
	reloaded, _, err := store2.Load(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, reloaded.Status)
	require.Len(t, reloaded.Timers, 1, "the pending timer must survive the JSON round-trip")

	// Advance the clock and deliver the timer-fired trigger via a runner on store2.
	fc.Advance(1*time.Hour + time.Second)
	sched2 := runtime.NewMemScheduler(fc)
	r2 := runtime.NewRunner(cat, fc, store2, runtime.WithScheduler(sched2))
	// Re-arm + fire: deliver TimerFired for the reloaded timer directly.
	timerID := reloaded.Timers[0].ID // confirm the field name when reading timerRecord
	final, err := r2.Deliver(t.Context(), def, id, engine.NewTimerFired(fc.Now(), timerID))
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, final.Status)
	require.True(t, ran)
	require.Empty(t, final.Tokens)
}
```

Confirm the exact `timerRecord` ID field name and `engine.NewTimerFired` signature against the code when writing (read `engine/state.go` `timerRecord` and `engine/trigger.go`). If `NewTimerFired` takes a different identifier (e.g. the node id), use what the engine expects — **trust the compiler and the timer example test over this listing**.

- [ ] **Step 2: Run test to verify it passes (GREEN) — or fails revealing a real round-trip bug**

Run: `go test -race -run '^TestPostgresParkedTimerResumesAfterReload$' ./internal/persistence/postgres/...`
Expected: PASS. If it FAILS because a parked field does not survive the JSON round-trip, that is a real persistence bug — STOP and report it (status DONE_WITH_CONCERNS / BLOCKED with the diff between `parked` and `reloaded`); do not paper over it.

- [ ] **Step 3: Add the boundary/armed-event variant**

Add `TestPostgresParkedBoundaryResumesAfterReload`: a service task with a **boundary timer** (or an armed signal/message catch) that parks; reload via a fresh store; assert `reloaded.Boundaries` (or `reloaded.ArmedEvents`) survived the round-trip and the instance resumes to completion when the boundary/event fires. Define the definition inline; assert the relevant bookkeeping slice is non-empty after reload.

Run: `go test -race ./internal/persistence/postgres/...` → PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/persistence/postgres/resume_test.go
git commit -m "$(printf 'test(persistence): parked-instance resume after a fresh-Store reload\n\nProves the snapshot of a timer/boundary-parked instance survives a real\nPostgres reload through a brand-new Store and resumes to completion when the\nclock advances. Closes the highest-value missing persistence test.\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 6: Inner-scope topology tests

Confidence tests for constructs nested inside a sub-process.

**Files:**
- Test: `engine/step_subprocess_test.go` (add tests + inline def helpers, following `parallelSubProcessDef()`'s pattern)

**Interfaces:**
- Consumes: existing engine test helpers and the `Step` driving pattern in `engine/step_subprocess_test.go`.

- [ ] **Step 1: Write the tests (one per construct)**

Following the `parallelSubProcessDef()` helper and the existing sub-process test driving pattern in `engine/step_subprocess_test.go`, add one focused test each, with an inline def helper that nests the construct inside a sub-process:

1. **Boundary timer inside a sub-process** — a service task in the inner def with a boundary timer; assert the boundary arms within the child scope and (on fire) routes to the boundary target, then the sub-process exits to the parent.
2. **Event-based gateway inside a sub-process** — inner def has an event-based gateway with two catch events; deliver one event; assert first-event-wins inside the child scope and the sub-process completes.
3. **Inclusive (OR) gateway inside a sub-process** — inner def has an OR-fork + OR-join; assert correct multi-branch activation and join inside the child scope.
4. **SLA timer on a user task inside a sub-process** — inner def has a user task with an SLA timer + escalation path; advance the clock; assert the escalation routes inside the child scope and the sub-process exits.

Each test asserts (a) the inner construct behaves correctly within the child scope and (b) the sub-process still exits cleanly to the parent (`outer-end` reached / `StatusCompleted`). Use the existing assertion style in the file.

- [ ] **Step 2: Run the tests**

Run: `go test -race ./engine/...`
Expected: PASS. **If any test reveals a real bug** in inner-scope propagation, STOP: write the failing test as a regression (it is already failing — that IS the RED), report it (DONE_WITH_CONCERNS), and fix the engine code test-first rather than adjusting the test to match buggy behaviour.

- [ ] **Step 3: Commit**

```bash
git add engine/step_subprocess_test.go
git commit -m "$(printf 'test(engine): inner-scope topology coverage inside sub-processes\n\nBoundary timer, event-based gateway, inclusive gateway, and SLA-timer\nconstructs nested inside a sub-process now have dedicated scope-propagation\ntests.\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 7: Verification gate + HANDOVER update

**Files:**
- Modify: `docs/plans/HANDOVER.md` (mark the addressed follow-ups done)

- [ ] **Step 1: Full race suite**

Run: `go test -race ./...`
Expected: all green (needs Docker for the Postgres tests). Fix any failure before proceeding.

- [ ] **Step 2: Coverage on touched packages**

Run: `go test -coverprofile=cover.out ./engine/... ./model/... ./service/... ./transport/... ./internal/persistence/postgres/... && go tool cover -func=cover.out | tail -1`
Expected: ≥85% per the gate (the touched packages already sit well above 85%; the additions are mostly tests and small rules, so coverage should rise or hold). If any touched package dipped below 85%, add the missing case.

- [ ] **Step 3: Lint**

Run: `golangci-lint run ./...`
Expected: 0 issues. Fix any (e.g. an unused import after capturing `st` in `resolveDefinition` callers).

- [ ] **Step 4: Engine-purity guard**

Run: `grep -REl "transport|/persistence/|watermill|gocron|casbin|pgx" --include='*.go' engine model | grep -v _test.go || echo "CLEAN: engine/model stay pure"`
Expected: prints `CLEAN`. (No transport/storage/vendor import was added to `engine`/`model`.)

- [ ] **Step 5: Update HANDOVER.md**

In `docs/plans/HANDOVER.md`, update the engine-core "Tracked follow-ups" list: mark #1 (nested-scope compensation) **DONE — ADR-0013**, note #2 (`Compensate` command) is now accurately documented as reserved-for-scope-targeted, mark #4 (typed gateway validation) **partially DONE — mixed-gateway rule, ADR-0014**, mark #5 (inner-scope topology tests) **DONE**. In the Persistence deferred list mark #6 (parked-async resume e2e) **DONE**. In the Transports deferred list mark #1 (422/FailedPrecondition wrong-state sentinel) **DONE — `service.ErrConflict`**. Keep the remaining deferred items (scope-targeted compensation, reachability pairing, engine-level wrong-state sentinel) listed.

- [ ] **Step 6: Commit**

```bash
git add docs/plans/HANDOVER.md
git commit -m "$(printf 'docs(correctness): record correctness-hardening follow-ups as addressed\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

## Self-review notes (author)

- **Spec coverage:** §2 → Task 1; §3 → Task 2; §4 → Tasks 3+4; §5 → Task 5; §6 → Task 6; §7 gate → Task 7. All five work-streams plus the gate map to tasks.
- **Type consistency:** `hoistCompensations(childID, parentID string)` (Task 1) matches ADR-0013; `model.ErrMixedGateway` (Task 2) used identically in test + impl; `service.ErrConflict` + `isTerminal` (Task 3) consumed verbatim in Task 4's transport cases; `engine.Status` terminal set = `StatusCompleted|StatusFailed|StatusTerminated` (confirmed against `engine/state.go`).
- **Determinism:** Task 1's hoist is slice-order appends only; no new `InstanceState` field, so `cloneState`/snapshot are untouched.
- **Grounding caveats flagged inline:** Task 1 (compensation action name + multi-`Step` driving pattern), Task 5 (`timerRecord` ID field + `NewTimerFired` signature), Task 3 (existing service-test wiring) all instruct the implementer to trust the then-current code and compiler over the listing — the engine is large and these listings are starting points.
