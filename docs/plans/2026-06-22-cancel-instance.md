# CancelInstance + Definition-Level Cancel Actions — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface instance cancellation through engine → runtime → service → REST + gRPC, and add optional definition-level cancel actions the engine runs best-effort (fire-and-forget) when an instance is cancelled.

**Architecture:** The engine already implements `CancelRequested`. This track adds a `model.ProcessDefinition.CancelActions []string`, a new fire-and-forget `engine.InvokeCancelAction` command emitted by the `CancelRequested` handler, a best-effort `perform(InvokeCancelAction)` (logs failures, never feeds results back, never fails the cancel), and the surfacing layers (`Runner.CancelInstance` → `service.CancelInstance` → admin REST `POST /admin/instances/{id}/cancel` + gRPC `rpc CancelInstance`).

**Tech Stack:** Go 1.25, protoc/protobuf (gRPC regen), stdlib `slog`, testify, bufconn, project `table-test` skill.

## Global Constraints

- **TDD strict:** every new symbol/behavior gets a failing test with a visible RED (`go test ./<pkg>/...`) before the impl.
- **Engine/model DO change this track** (the chosen definition-level cancel actions): the new `InvokeCancelAction` command + `CancelActions` field + the `CancelRequested` emission. But `engine.Step` MUST stay **deterministic** (commands a pure function of `(def, state)`) and **pure** (no clock — use `t.OccurredAt()`/`copyVars`; no transport/storage/bus/time-vendor imports added to engine/model).
- **Cancel actions are fire-and-forget + best-effort:** `perform(InvokeCancelAction)` runs the action, logs failures via `slog`, and ALWAYS returns `(nil, nil)` — no follow-up trigger, never an error. Cancelling never fails because a cancel action failed.
- **Already-terminal cancel → `service.ErrConflict`** (→ REST 422 `conflict_state` / gRPC `codes.FailedPrecondition`). Mappings already exist; do not change them.
- **Error sentinel prefix:** new error messages prefix the package segment with `workflow-` (e.g. `workflow-model:`, `workflow-service:`); assert sentinels with `errors.Is`.
- **`workflowpb` regenerated + committed:** build must not require `protoc`; only regeneration does (`go generate ./transport/grpc/...`).
- **Tests:** black-box (`package <pkg>_test`); table-driven with the **`assert` closure per case** (project `table-test` skill, not want/wantErr); `t.Context()`; pair each `foo.go` with `foo_test.go`. Postgres-touching tests run `-p 1` (none expected here).
- **Lint:** `golangci-lint` v2. **Verify on completion:** `go test -race -p 1 ./...` green; touched pkgs ≥85%; lint clean.
- **Commits:** Conventional Commits scoped to the area; end with `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
- **Branch:** `feat/cancel-instance` (never implement on `main`).

**Verified anchors (current code):**
- `model.ProcessDefinition` is `{ID, Version, Nodes, Flows}` in `model/definition.go`. `Validate(d)` → `validate(d, seen)` in `model/validate.go`; sentinels are package-level `var (... = errors.New("workflow-model: ..."))`; `validate` accumulates `var errs []error` and returns `errors.Join(errs...)` (confirm the join call at the end).
- `engine/command.go`: `InvokeAction{CommandID, Name, Input}`; the `isCommand()` marker block lists all variants ending with `func (Compensate) isCommand() {}`.
- `engine/step.go` `case CancelRequested:` (≈ lines 118-145) sets `StatusTerminated`, clears tokens, emits `[]Command{FailInstance{Err:"cancelled"}}` + `cancelAllTimers()` + `cancelAllArmsAndBoundaries()`. `copyVars` and `s.Variables` are available in that scope; `def` is the `*model.ProcessDefinition` arg.
- `runtime/runner.go` `perform` switch (≈ line 569) has `case engine.InvokeAction:`; uses `r.cat` (action.Catalog, may be nil), `r.cat.Resolve(name) (action.ServiceAction, bool)`, `a.Do(ctx, input) (map[string]any, error)`, and `r.obs.tel.Logger` (a `*slog.Logger`) for logging. `ResolveIncident` (≈ line 539) is the runner-method precedent; `engine.NewCancelRequested(at)` exists.
- `service/service.go`: `Engine` struct + `Service` interface; `ResolveIncident` (≈ line 219) + `resolveDefinition` (≈ line 238) + `isTerminal` (`service/errors.go`) + `ErrConflict`. `ResolveIncidentRequest` in `service/request.go`.
- `transport/rest/handler.go` registers `POST /admin/instances/{id}/incidents/{incidentID}/resolve` behind `cfg.adminMiddleware`; `transport/rest/admin.go` `handleResolveIncident` uses `r.PathValue("id")`, `h.svc.<op>`, `WriteHTTPError`, `h.renderInstance(w, r, http.StatusOK, st)`.
- `transport/grpc/proto/workflow.proto` service `WorkflowService`; `server.go` `StartInstance` is the unary precedent (`s.svc.<op>` → `instanceToProto` → `mapToGRPCStatus`); `//go:generate` directive in `transport/grpc/errors.go`.

---

### Task 1: `model.ProcessDefinition.CancelActions` + validation

**Files:**
- Modify: `model/definition.go` (add `CancelActions []string` field)
- Modify: `model/validate.go` (reject empty entries; new `ErrEmptyCancelAction`)
- Modify/Create: `model/validate_test.go` (or the existing validate test file)

**Interfaces:**
- Produces: `model.ProcessDefinition.CancelActions []string`; `model.ErrEmptyCancelAction`.

- [ ] **Step 1: Write the failing test** — add to the existing validate test file (black-box `package model_test`)

```go
func TestValidateCancelActions(t *testing.T) {
	base := func(cancel []string) *model.ProcessDefinition {
		return &model.ProcessDefinition{
			ID: "d", Version: 1,
			Nodes: []model.Node{
				{ID: "start", Kind: model.KindStartEvent},
				{ID: "end", Kind: model.KindEndEvent},
			},
			Flows:         []model.SequenceFlow{{ID: "f1", Source: "start", Target: "end"}},
			CancelActions: cancel,
		}
	}
	cases := []struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		{
			name: "nil cancel actions is valid",
			def:  base(nil),
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		{
			name: "non-empty cancel action names are valid",
			def:  base([]string{"notify", "refund"}),
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		{
			name: "empty cancel action name is rejected",
			def:  base([]string{"notify", ""}),
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, model.ErrEmptyCancelAction)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.assert(t, model.Validate(tc.def)) })
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./model/... -run TestValidateCancelActions`
Expected: FAIL — `undefined: model.ErrEmptyCancelAction` (and the field).

- [ ] **Step 3: Add the field** — `model/definition.go`

```go
type ProcessDefinition struct {
	ID      string
	Version int
	Nodes   []Node
	Flows   []SequenceFlow
	// CancelActions are optional, ordered ServiceAction names invoked best-effort
	// by the engine when the instance is cancelled (see ADR-0028). Empty means no
	// cancel actions. Action-name existence is not validated here (the catalog is
	// not available at validate time); an unresolved name is logged at runtime.
	CancelActions []string
}
```

- [ ] **Step 4: Add the sentinel + validation** — `model/validate.go`

Add to the sentinel `var (...)` block:

```go
	ErrEmptyCancelAction = errors.New("workflow-model: empty cancel action name")
```

In `validate(...)`, after the start-node check (before/after the flow loop — anywhere inside the function before the final join), add:

```go
	for i, name := range d.CancelActions {
		if name == "" {
			errs = append(errs, fmt.Errorf("%w: CancelActions[%d]", ErrEmptyCancelAction, i))
		}
	}
```

(Confirm the function accumulates into `errs` and returns `errors.Join(errs...)`; match the existing pattern exactly.)

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./model/...`
Expected: PASS (all model tests green).

- [ ] **Step 6: Commit**

```bash
git add model/definition.go model/validate.go model/validate_test.go
git commit -m "feat(model): ProcessDefinition.CancelActions + validation

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: `engine.InvokeCancelAction` command + `CancelRequested` emission

**Files:**
- Modify: `engine/command.go` (new command + `isCommand()` + compile assertion)
- Modify: `engine/step.go` (`CancelRequested` emits `InvokeCancelAction` per `def.CancelActions`)
- Modify/Create: `engine/command_test.go` and the cancel test in `engine/step_errors_test.go`

**Interfaces:**
- Consumes: `model.ProcessDefinition.CancelActions` (Task 1).
- Produces: `engine.InvokeCancelAction{Name string; Input map[string]any}` (a `Command`).

- [ ] **Step 1: Write the failing test** — add to `engine/step_errors_test.go` (black-box `package engine_test`)

```go
func TestCancelRequestedEmitsCancelActions(t *testing.T) {
	def := &model.ProcessDefinition{
		ID: "d", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "svc", Kind: model.KindServiceTask, Action: "work"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
		},
		CancelActions: []string{"notify", "refund"},
	}
	// A running instance with a live token and some variables.
	st := engine.InstanceState{
		InstanceID: "i1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning,
		Variables: map[string]any{"amount": 10},
		Tokens:    []engine.Token{{ID: "i1-t1", NodeID: "svc", State: engine.TokenActive}},
	}
	res, err := engine.Step(def, st, engine.NewCancelRequested(time.Unix(100, 0).UTC()), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusTerminated, res.State.Status)
	assert.Empty(t, res.State.Tokens)

	// The first two commands are the cancel actions, in definition order, before FailInstance.
	var cancelNames []string
	var sawFail bool
	for _, c := range res.Commands {
		switch cmd := c.(type) {
		case engine.InvokeCancelAction:
			cancelNames = append(cancelNames, cmd.Name)
			assert.Equal(t, 10, cmd.Input["amount"], "cancel action receives a variables snapshot")
			assert.False(t, sawFail, "cancel actions must be emitted before FailInstance")
		case engine.FailInstance:
			sawFail = true
		}
	}
	assert.Equal(t, []string{"notify", "refund"}, cancelNames)
	assert.True(t, sawFail, "FailInstance must still be emitted")
}

func TestCancelRequestedNoCancelActionsUnchanged(t *testing.T) {
	def := &model.ProcessDefinition{
		ID: "d", Version: 1,
		Nodes: []model.Node{{ID: "start", Kind: model.KindStartEvent}, {ID: "end", Kind: model.KindEndEvent}},
		Flows: []model.SequenceFlow{{ID: "f1", Source: "start", Target: "end"}},
	}
	st := engine.InstanceState{
		InstanceID: "i1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning,
		Tokens: []engine.Token{{ID: "i1-t1", NodeID: "start", State: engine.TokenActive}},
	}
	res, err := engine.Step(def, st, engine.NewCancelRequested(time.Unix(100, 0).UTC()), engine.StepOptions{})
	require.NoError(t, err)
	for _, c := range res.Commands {
		_, isCancel := c.(engine.InvokeCancelAction)
		assert.False(t, isCancel, "no InvokeCancelAction when CancelActions is empty")
	}
}
```

(Verify `engine.TokenActive` / `engine.StepOptions{}` / the `Step` signature against the package; adapt field names to the real `Token`/`InstanceState` if they differ — do NOT change the engine API, just match it.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./engine/... -run TestCancelRequested`
Expected: FAIL — `undefined: engine.InvokeCancelAction`.

- [ ] **Step 3: Add the command** — `engine/command.go`

```go
// InvokeCancelAction asks the runtime to run a named ServiceAction as a
// best-effort side effect during cancellation. Unlike InvokeAction it carries no
// CommandID and its result is never fed back into the engine (the instance is
// already terminal). The runtime logs a failure; it never fails the cancel.
type InvokeCancelAction struct {
	Name  string
	Input map[string]any
}
```

Add the marker to the `isCommand()` block:

```go
func (InvokeCancelAction) isCommand() {}
```

Add a compile-time assertion near the others (or create one if the file uses them):

```go
var _ Command = InvokeCancelAction{}
```

- [ ] **Step 4: Emit from `CancelRequested`** — `engine/step.go`

Replace the terminal-command assembly in the `case CancelRequested:` block:

```go
		s.Tokens = nil
		// Emit best-effort cancel actions (fire-and-forget) in definition order,
		// then the terminal command and scheduler-resource cancellations.
		var cmds []Command
		for _, name := range def.CancelActions {
			cmds = append(cmds, InvokeCancelAction{Name: name, Input: copyVars(s.Variables)})
		}
		cmds = append(cmds, FailInstance{Err: "cancelled"})
		cmds = append(cmds, s.cancelAllTimers()...)
		cmds = append(cmds, s.cancelAllArmsAndBoundaries()...)
		return StepResult{State: s, Commands: cmds}, nil
```

(Update the handler's doc comment to mention cancel actions. `copyVars` and `def` are already in scope.)

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./engine/...`
Expected: PASS (incl. the existing `TestCancelRequestedTerminates` — empty `CancelActions` keeps its command set unchanged).

- [ ] **Step 6: Verify engine purity unchanged**

Run: `go list -deps ./engine ./model | grep -E 'transport|persistence|watermill|gocron|clockwork' || echo "PURE"`
Expected: `PURE`.

- [ ] **Step 7: Commit**

```bash
git add engine/command.go engine/step.go engine/step_errors_test.go engine/command_test.go
git commit -m "feat(engine): InvokeCancelAction command; CancelRequested emits cancel actions

Fire-and-forget cancel actions emitted in definition order before FailInstance.
Step stays deterministic + pure. Empty CancelActions = unchanged behavior.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Runtime — best-effort `perform(InvokeCancelAction)` + `Runner.CancelInstance`

**Files:**
- Modify: `runtime/runner.go` (new `perform` case + `CancelInstance` method)
- Create: `runtime/cancel_test.go`

**Interfaces:**
- Consumes: `engine.InvokeCancelAction` (Task 2), `engine.NewCancelRequested`.
- Produces: `(*Runner).CancelInstance(ctx context.Context, def *model.ProcessDefinition, instanceID string) (engine.InstanceState, error)`.

- [ ] **Step 1: Write the failing test** — `runtime/cancel_test.go` (black-box `package runtime_test`)

```go
package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// cancelDef parks at a service task that waits on an external action result,
// so the instance stays Running until cancelled. Simpler: park at a human task.
func cancelDef(cancelActions []string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "cancel-def", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "wait", Kind: model.KindUserTask, CandidateRoles: []string{"r"}},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait"},
			{ID: "f2", Source: "wait", Target: "end"},
		},
		CancelActions: cancelActions,
	}
}

func TestRunnerCancelInstanceRunsCancelActions(t *testing.T) {
	fc := clockwork.NewFakeClock()
	var ran []string
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"notify": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			ran = append(ran, "notify")
			return nil, nil
		}),
		"boom": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			ran = append(ran, "boom")
			return nil, errors.New("cancel action failed on purpose")
		}),
	})
	store := runtime.NewMemStore()
	r := runtime.NewRunner(cat, fc, store, runtime.WithHumanTasks(staticResolver(), memTaskStore(), allowAll()))
	def := cancelDef([]string{"notify", "boom"})

	_, err := r.Run(t.Context(), def, "c1", nil)
	require.NoError(t, err)

	// Cancel: both actions run; the failing "boom" is logged but does NOT fail the cancel.
	st, err := r.CancelInstance(t.Context(), def, "c1")
	require.NoError(t, err, "a failing cancel action must not fail CancelInstance")
	assert.Equal(t, engine.StatusTerminated, st.Status)
	assert.Empty(t, st.Tokens)
	assert.Equal(t, []string{"notify", "boom"}, ran, "both cancel actions ran in order")
}

func TestRunnerCancelInstanceMissingActionIsBestEffort(t *testing.T) {
	fc := clockwork.NewFakeClock()
	store := runtime.NewMemStore()
	// No catalog entry for "ghost" — must be logged + skipped, cancel still succeeds.
	r := runtime.NewRunner(action.NewMapCatalog(nil), fc, store, runtime.WithHumanTasks(staticResolver(), memTaskStore(), allowAll()))
	def := cancelDef([]string{"ghost"})
	_, err := r.Run(t.Context(), def, "c2", nil)
	require.NoError(t, err)

	st, err := r.CancelInstance(t.Context(), def, "c2")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusTerminated, st.Status)
}
```

Reuse the package's existing human-task test helpers if present (grep `runtime/*_test.go` for `WithHumanTasks(` to find the real `staticResolver()`/`memTaskStore()`/`allowAll()` equivalents and use those exact constructors; if the package uses a different parking construct, park at whatever keeps the instance `Running` — the point is a live instance to cancel). The assertions on `ran`/`StatusTerminated` are the invariant.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./runtime/... -run TestRunnerCancelInstance`
Expected: FAIL — `r.CancelInstance undefined` (and the `InvokeCancelAction` perform case missing → action never runs).

- [ ] **Step 3: Add the `perform` case** — `runtime/runner.go` (in the `perform` switch, after the `InvokeAction` case)

```go
	case engine.InvokeCancelAction:
		// Best-effort, fire-and-forget: run the action for its side effect, log any
		// failure, and NEVER feed a result back or return an error — the instance is
		// already terminal and cancellation must report success regardless (ADR-0028).
		if r.cat == nil {
			r.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: cancel action skipped: no catalog",
				slog.String("action", cmd.Name))
			return nil, nil
		}
		a, ok := r.cat.Resolve(cmd.Name)
		if !ok {
			r.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: cancel action not found",
				slog.String("action", cmd.Name))
			return nil, nil
		}
		if _, err := a.Do(ctx, cmd.Input); err != nil {
			r.obs.tel.Logger.LogAttrs(ctx, slog.LevelError, "runtime: cancel action failed",
				slog.String("action", cmd.Name), slog.Any("error", err))
		}
		return nil, nil
```

(Confirm `slog` is imported in `runner.go` — it is, used by the timer-fire logging. Confirm the logger accessor is `r.obs.tel.Logger`.)

- [ ] **Step 4: Add `Runner.CancelInstance`** — `runtime/runner.go` (near `ResolveIncident`)

```go
// CancelInstance terminates a running instance by delivering a CancelRequested
// trigger. Any definition-level CancelActions run best-effort inside the same
// deliverLoop (failures are logged, never fail the cancel). Returns the
// terminated InstanceState. See ADR-0028.
func (r *Runner) CancelInstance(ctx context.Context, def *model.ProcessDefinition, instanceID string) (engine.InstanceState, error) {
	return r.Deliver(ctx, def, instanceID, engine.NewCancelRequested(r.clk.Now()))
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./runtime/...`
Expected: PASS (full package — no regressions).

- [ ] **Step 6: Commit**

```bash
git add runtime/runner.go runtime/cancel_test.go
git commit -m "feat(runtime): best-effort perform(InvokeCancelAction) + Runner.CancelInstance

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Service — `CancelInstance` + request type + interface

**Files:**
- Modify: `service/request.go` (add `CancelInstanceRequest`)
- Modify: `service/service.go` (add `CancelInstance` to the `Service` interface + the `*Engine` method)
- Create: `service/cancel_instance_test.go`

**Interfaces:**
- Consumes: `Runner.CancelInstance` (Task 3), `resolveDefinition`, `isTerminal`, `ErrConflict`.
- Produces: `service.CancelInstanceRequest{InstanceID string}`; `Service.CancelInstance(ctx, CancelInstanceRequest) (engine.InstanceState, error)`.

- [ ] **Step 1: Write the failing test** — `service/cancel_instance_test.go` (black-box `package service_test`)

```go
func TestCancelInstance(t *testing.T) {
	cases := []struct {
		name   string
		assert func(t *testing.T, svc *service.Engine)
	}{
		{
			name: "cancels a running instance",
			assert: func(t *testing.T, svc *service.Engine) {
				st, err := svc.CancelInstance(t.Context(), service.CancelInstanceRequest{InstanceID: "ci-run"})
				require.NoError(t, err)
				assert.Equal(t, engine.StatusTerminated, st.Status)
				assert.Empty(t, st.Tokens)
			},
		},
		{
			name: "already-terminal returns ErrConflict",
			assert: func(t *testing.T, svc *service.Engine) {
				_, err := svc.CancelInstance(t.Context(), service.CancelInstanceRequest{InstanceID: "ci-done"})
				require.ErrorIs(t, err, service.ErrConflict)
			},
		},
		{
			name: "unknown instance returns ErrInstanceNotFound",
			assert: func(t *testing.T, svc *service.Engine) {
				_, err := svc.CancelInstance(t.Context(), service.CancelInstanceRequest{InstanceID: "nope"})
				require.ErrorIs(t, err, runtime.ErrInstanceNotFound)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newCancelTestService(t) // builds Engine; seeds "ci-run" parked-running and "ci-done" terminal
			tc.assert(t, svc)
		})
	}
}
```

Build `newCancelTestService(t)` by mirroring the existing service test setup (grep `service/*_test.go` for how `resolve_incident_test.go` constructs `service.New(...)` with a Runner + registry; reuse that). Seed `ci-run` by starting a process that parks at a human task (use the same human-task helpers as the existing service tests), and `ci-done` by starting + completing a one-node process so it is terminal. Register the def in the registry under `"<DefID>:<Version>"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./service/... -run TestCancelInstance`
Expected: FAIL — `undefined: service.CancelInstanceRequest` / `svc.CancelInstance undefined`.

- [ ] **Step 3: Add the request type** — `service/request.go`

```go
// CancelInstanceRequest carries the parameters for cancelling a process instance.
type CancelInstanceRequest struct {
	// InstanceID identifies the process instance to cancel.
	InstanceID string
}
```

- [ ] **Step 4: Add to the `Service` interface** — `service/service.go`

```go
	// CancelInstance terminates a running process instance, running any
	// definition-level cancel actions best-effort. Returns ErrConflict when the
	// instance has already reached a terminal state.
	CancelInstance(ctx context.Context, req CancelInstanceRequest) (engine.InstanceState, error)
```

- [ ] **Step 5: Add the `*Engine` method** — `service/service.go` (near `ResolveIncident`)

```go
// CancelInstance resolves the instance's definition, rejects an already-terminal
// instance with ErrConflict, and delegates to Runner.CancelInstance.
func (e *Engine) CancelInstance(ctx context.Context, req CancelInstanceRequest) (engine.InstanceState, error) {
	def, st, err := e.resolveDefinition(ctx, req.InstanceID)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("workflow-service: cancel instance: %w", err)
	}
	if isTerminal(st.Status) {
		return engine.InstanceState{}, fmt.Errorf("%w: instance %q is already terminal", ErrConflict, req.InstanceID)
	}
	st, err = e.runner.CancelInstance(ctx, def, req.InstanceID)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("workflow-service: cancel instance: %w", err)
	}
	return st, nil
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./service/...`
Expected: PASS (the `var _ Service = (*Engine)(nil)` assertion still compiles with the new interface method).

- [ ] **Step 7: Commit**

```bash
git add service/request.go service/service.go service/cancel_instance_test.go
git commit -m "feat(service): CancelInstance with already-terminal ErrConflict guard

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: REST — admin `POST /admin/instances/{id}/cancel`

**Files:**
- Modify: `transport/rest/handler.go` (route registration + doc comment)
- Modify: `transport/rest/admin.go` (`handleCancelInstance`)
- Create: `transport/rest/cancel_instance_test.go`

**Interfaces:**
- Consumes: `service.CancelInstanceRequest` / `Service.CancelInstance` (Task 4).

- [ ] **Step 1: Write the failing test** — `transport/rest/cancel_instance_test.go` (black-box, mirror `resolve_incident_test.go`)

```go
func TestHandleCancelInstance(t *testing.T) {
	cases := []struct {
		name       string
		middleware bool       // install allow-admin middleware?
		svc        service.Service // a stub returning a fixed result/error
		wantStatus int
	}{
		{name: "default-deny without admin middleware", middleware: false, svc: okCancelStub(), wantStatus: http.StatusForbidden},
		{name: "admin success", middleware: true, svc: okCancelStub(), wantStatus: http.StatusOK},
		{name: "already-terminal maps to 422", middleware: true, svc: conflictCancelStub(), wantStatus: http.StatusUnprocessableEntity},
		{name: "unknown instance maps to 404", middleware: true, svc: notFoundCancelStub(), wantStatus: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var opts []rest.Option
			if tc.middleware {
				opts = append(opts, rest.WithAdminMiddleware(allowAdmin))
			}
			h := rest.NewHandler(tc.svc, opts...)
			req := httptest.NewRequest(http.MethodPost, "/admin/instances/p1/cancel", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			assert.Equal(t, tc.wantStatus, rec.Code)
		})
	}
}
```

Build the stubs (`okCancelStub` returns a terminated `InstanceState`; `conflictCancelStub` returns `fmt.Errorf("%w", service.ErrConflict)`; `notFoundCancelStub` returns `runtime.ErrInstanceNotFound`) and `allowAdmin` by mirroring `transport/rest/resolve_incident_test.go` exactly (it already has a service stub + an allow-admin middleware + `rest.NewHandler` usage — copy that scaffolding; the stub must implement the full `service.Service` interface, so embed an existing test stub or implement all methods returning zero values except `CancelInstance`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./transport/rest/... -run TestHandleCancelInstance`
Expected: FAIL — route 404 (not registered) so success case returns 404 not 200/403.

- [ ] **Step 3: Register the route** — `transport/rest/handler.go`

Add next to the `resolve` admin route registration:

```go
	mux.Handle("POST /admin/instances/{id}/cancel",
		cfg.adminMiddleware(http.HandlerFunc(h.handleCancelInstance)))
```

Add a line to the admin-routes doc comment:

```go
	//	POST   /admin/instances/{id}/cancel — cancel a running instance (runs cancel actions)
```

- [ ] **Step 4: Add the handler** — `transport/rest/admin.go`

```go
// handleCancelInstance handles POST /admin/instances/{id}/cancel. It cancels the
// instance (running any definition-level cancel actions best-effort) and renders
// the resulting terminated instance. No request body.
func (h *handler) handleCancelInstance(w http.ResponseWriter, r *http.Request) {
	instanceID := r.PathValue("id")
	st, err := h.svc.CancelInstance(r.Context(), service.CancelInstanceRequest{InstanceID: instanceID})
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	h.renderInstance(w, r, http.StatusOK, st)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./transport/rest/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add transport/rest/handler.go transport/rest/admin.go transport/rest/cancel_instance_test.go
git commit -m "feat(transport/rest): admin POST /admin/instances/{id}/cancel

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: gRPC — `rpc CancelInstance` (proto + regen + server)

**Files:**
- Modify: `transport/grpc/proto/workflow.proto` (RPC + message)
- Regenerate: `transport/grpc/workflowpb/*.pb.go` (via `go generate`)
- Modify: `transport/grpc/server.go` (`CancelInstance` method)
- Create: `transport/grpc/cancel_instance_test.go`

**Interfaces:**
- Consumes: `service.CancelInstance` (Task 4); generated `workflowpb.CancelInstanceRequest`.

- [ ] **Step 1: Edit the proto** — `transport/grpc/proto/workflow.proto`

Add to the `service WorkflowService` block:

```protobuf
  // CancelInstance terminates a running instance (running cancel actions best-effort).
  rpc CancelInstance(CancelInstanceRequest) returns (InstanceResponse);
```

Add a request message (next to `GetInstanceRequest`):

```protobuf
message CancelInstanceRequest {
  string instance_id = 1;
}
```

- [ ] **Step 2: Regenerate workflowpb**

Run: `go generate ./transport/grpc/...`
Expected: `transport/grpc/workflowpb/workflow.pb.go` + `workflow_grpc.pb.go` updated with `CancelInstanceRequest` + the `CancelInstance` method on the server/client interfaces. (Requires `protoc` + `protoc-gen-go` + `protoc-gen-go-grpc` on PATH. If unavailable, STOP and report BLOCKED — do not hand-edit generated files.)

- [ ] **Step 3: Write the failing test** — `transport/grpc/cancel_instance_test.go` (black-box, bufconn, mirror an existing server test)

```go
func TestServerCancelInstance(t *testing.T) {
	cases := []struct {
		name     string
		svc      service.Service
		wantCode codes.Code // codes.OK on success
	}{
		{name: "success", svc: okCancelStub(), wantCode: codes.OK},
		{name: "already-terminal -> FailedPrecondition", svc: conflictCancelStub(), wantCode: codes.FailedPrecondition},
		{name: "unknown -> NotFound", svc: notFoundCancelStub(), wantCode: codes.NotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, cleanup := newBufconnClient(t, tc.svc) // mirror existing grpc test dialer
			defer cleanup()
			resp, err := client.CancelInstance(t.Context(), &workflowpb.CancelInstanceRequest{InstanceId: "p1"})
			if tc.wantCode == codes.OK {
				require.NoError(t, err)
				assert.NotNil(t, resp.GetInstance())
			} else {
				assert.Equal(t, tc.wantCode, status.Code(err))
			}
		})
	}
}
```

Reuse the package's existing bufconn dialer + service stubs (grep `transport/grpc/*_test.go` for the existing `newBufconnClient`/server-start helper and the `service.Service` stub; the stub needs a `CancelInstance` method now — extend the shared stub or add the three cancel stubs mirroring how other RPCs are stubbed).

- [ ] **Step 4: Run test to verify it fails**

Run: `go test ./transport/grpc/... -run TestServerCancelInstance`
Expected: FAIL — `server` does not implement `CancelInstance` (or the client method is missing) until Step 5.

- [ ] **Step 5: Implement the server method** — `transport/grpc/server.go` (mirror `StartInstance`)

```go
// CancelInstance terminates a running process instance.
func (s *server) CancelInstance(ctx context.Context, req *workflowpb.CancelInstanceRequest) (*workflowpb.InstanceResponse, error) {
	ctx, span := s.startSpan(ctx, "CancelInstance")
	defer span.End()

	st, err := s.svc.CancelInstance(ctx, service.CancelInstanceRequest{InstanceID: req.GetInstanceId()})
	if err != nil {
		recordSpanErr(span, err)
		return nil, mapToGRPCStatus(err)
	}
	proto, err := instanceToProto(st)
	if err != nil {
		recordSpanErr(span, err)
		return nil, status.Errorf(codes.Internal, "response serialization: %s", err)
	}
	return &workflowpb.InstanceResponse{Instance: proto}, nil
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./transport/grpc/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add transport/grpc/proto/workflow.proto transport/grpc/workflowpb/ transport/grpc/server.go transport/grpc/cancel_instance_test.go
git commit -m "feat(transport/grpc): CancelInstance RPC

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: ADR-0028 + HANDOVER + final gate

**Files:**
- Create: `docs/adr/0028-cancel-instance.md`
- Modify: `docs/plans/HANDOVER.md`

- [ ] **Step 1: Write ADR-0028** (Nygard template — Status: Accepted, Date: 2026-06-22)

Follow `docs/adr/0001-record-architecture-decisions.md` + a recent ADR for house style. Cover: Context (cancel already works in the engine; missing surface; the requirement to run business tasks on cancel; the InvokeAction-result-feedback hazard against a terminal instance). Decision: surface `CancelInstance` through runner/service/REST(admin)/gRPC; definition-level `CancelActions` run by the engine via a new fire-and-forget `InvokeCancelAction` command (why a new command, not reused `InvokeAction`: avoid feeding results back to the terminal instance); best-effort/log semantics; already-terminal→`ErrConflict`; REST admin-gated vs gRPC consumer-interceptor asymmetry; rejected alternative (runtime callback hook). Consequences: engine/model now carry the command + field (determinism/purity preserved); cancel actions are side-effect-only; deferred follow-ups (per-node cancel handlers, cancel reason/audit, cancel-action observability, gRPC admin interceptor sample). Cross-reference ADR-0011 (consumer-mounted transports), ADR-0026 (ErrConflict/ErrInvalidTransition), the compensation ADR.

- [ ] **Step 2: Run the full verification gate**

```bash
go test -race -p 1 -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1
golangci-lint run ./...
go list -deps ./engine ./model | grep -E 'transport|persistence|watermill|gocron|clockwork' || echo "PURE"
```

Expected: all green; touched pkgs (model, engine, runtime, service, transport/rest, transport/grpc) ≥85%; lint 0; `PURE`.

- [ ] **Step 3: Update HANDOVER.md** — add a "CancelInstance + cancel actions sub-project — ✅ COMPLETE" section (branch, ADR-0028, what-shipped table across the layers, gate numbers, deferred follow-ups). Remove `CancelInstance` from the START-HERE top picks; promote the next pick (gRPC `ResolveIncident` + DLQ admin REST). Note that engine/model changed this track (the InvokeCancelAction command + CancelActions field).

- [ ] **Step 4: Commit**

```bash
git add docs/adr/0028-cancel-instance.md docs/plans/HANDOVER.md
git commit -m "docs(adr): 0028 CancelInstance + definition-level cancel actions; HANDOVER

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- §1 `CancelActions` field + validate → Task 1. ✅
- §2 `InvokeCancelAction` command + `CancelRequested` emission → Task 2. ✅
- §3 best-effort `perform` + `Runner.CancelInstance` → Task 3. ✅
- §4 service `CancelInstance` + `ErrConflict` guard → Task 4. ✅
- §5 admin REST route → Task 5. ✅
- §6 gRPC RPC → Task 6. ✅
- §7 ADR-0028 + tests → Task 7 (+ per-task tests). ✅
- Testing strategy (model validate, engine emission+order, runtime best-effort/missing-action, service terminal/not-found, REST 403/200/422/404, gRPC OK/FailedPrecondition/NotFound) → Tasks 1–6. ✅
- Verification gate (incl. engine/model purity-of-imports) → Task 7. ✅

**Placeholder scan:** Test scaffolding (`newCancelTestService`, bufconn dialer, REST stubs, human-task helpers) is described as "reuse the existing X via grep" rather than restated, because those helpers live in existing test files this plan must not duplicate verbatim (drift risk); the assertions and the SUT calls are fully specified. No `TODO`/`TBD`. All production code steps show complete code.

**Type consistency:** `model.CancelActions`, `model.ErrEmptyCancelAction`, `engine.InvokeCancelAction{Name,Input}`, `Runner.CancelInstance`, `service.CancelInstanceRequest{InstanceID}`, `Service.CancelInstance`, REST `handleCancelInstance`, gRPC `CancelInstanceRequest{InstanceId}` / `server.CancelInstance` used consistently across tasks. `InvokeCancelAction` has NO `CommandID` (fire-and-forget) — consistent everywhere.
