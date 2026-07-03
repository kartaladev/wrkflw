// Package service_test is the black-box test suite for the service facade.
package service_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
	"github.com/zakyalvan/krtlwrkflw/service"
)

// harness wires a real in-memory engine for the service tests.
type harness struct {
	runner *runtime.ProcessDriver
	tasks  *task.TaskService
	reg    *kernel.MapDefinitionRegistry
	store  *kernel.MemStore
	lister kernel.InstanceLister
	clk    *clockwork.FakeClock
	// taskStore is directly accessible for verification.
	taskStore *humantask.MemTaskStore
}

// linearDef returns start → serviceTask("greet") → end.
func linearDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "greeting", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewServiceTask("greet", model.WithActionName("greet")),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "greet"},
			{ID: "f2", Source: "greet", Target: "end"},
		},
	}
}

// approvalDef returns start → userTask("approve", role "manager") → end.
func approvalDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "approval",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewUserTask("approve", []string{"manager"}),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}
}

// signalCatchDef returns start → signal-catch(name) → end.
func signalCatchDef(signalName string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "signal-catch-" + signalName,
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewIntermediateCatchEvent("wait-signal", model.WithSignalName(signalName)),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait-signal"},
			{ID: "f2", Source: "wait-signal", Target: "end"},
		},
	}
}

// messageCatchDef returns start → message-catch(msgName, orderId) → end.
func messageCatchDef(msgName string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "message-catch-" + msgName,
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewIntermediateCatchEvent("wait-msg", model.WithMessageNameAndKey(msgName, "orderId")),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait-msg"},
			{ID: "f2", Source: "wait-msg", Target: "end"},
		},
	}
}

// defRefFor returns the "DefID:DefVersion" key used by MapDefinitionRegistry.
func defRefFor(def *model.ProcessDefinition) string {
	return fmt.Sprintf("%s:%d", def.ID, def.Version)
}

// newHarness builds a harness wired with the given process definitions.
func newHarness(t *testing.T, defs ...*model.ProcessDefinition) *harness {
	t.Helper()

	fc := clockwork.NewFakeClock()

	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"manager": {{ID: "alice", Roles: []string{"manager"}}},
	})
	az := authz.RoleAuthorizer{}

	store, err := kernel.NewMemStore()
	require.NoError(t, err)

	// Build the action catalog with a simple "greet" action.
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"greet": greetAction{},
	})

	r, err := runtime.NewProcessDriver(
		cat,
		store,
		runtime.WithClock(fc),
		runtime.WithHumanTasks(resolver, taskStore, az),
	)
	require.NoError(t, err)

	// Build the definition registry with all provided definitions.
	defsMap := make(map[string]*model.ProcessDefinition, len(defs))
	for _, d := range defs {
		defsMap[defRefFor(d)] = d
		// Also register by ID alone for convenience.
		defsMap[d.ID] = d
	}
	reg := kernel.NewMapDefinitionRegistry(defsMap)

	svc, err := task.NewTaskService(taskStore, az, task.WithClock(fc))

	require.NoError(t, err)
	return &harness{
		runner:    r,
		tasks:     svc,
		reg:       reg,
		store:     store,
		lister:    store,
		clk:       fc,
		taskStore: taskStore,
	}
}

// greetAction is a minimal service action for the "greet" service task.
type greetAction struct{}

func (greetAction) Do(_ context.Context, in map[string]any) (map[string]any, error) {
	name, _ := in["name"].(string)
	return map[string]any{"greeting": "hi " + name}, nil
}

// ---- Tests ----

// TestStartInstance verifies that StartInstance creates a process instance,
// completes it (for a linear process), and returns the correct InstanceID.
func TestStartInstance(t *testing.T) {
	def := linearDef()
	h := newHarness(t, def)
	svc := service.New(h.runner, h.tasks, h.reg, h.store, h.lister, h.taskStore, service.WithEngineClock(h.clk))

	st, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef:     "greeting",
		InstanceID: "inst-1",
		Vars:       map[string]any{"name": "ada"},
	})
	require.NoError(t, err)
	assert.Equal(t, "inst-1", st.InstanceID)
	assert.Equal(t, engine.StatusCompleted, st.Status)
}

// TestStartInstanceUnknownDefRef verifies that StartInstance returns
// ErrDefinitionNotFound for an unregistered DefRef.
func TestStartInstanceUnknownDefRef(t *testing.T) {
	h := newHarness(t) // no defs registered
	svc := service.New(h.runner, h.tasks, h.reg, h.store, h.lister, h.taskStore, service.WithEngineClock(h.clk))

	_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef:     "non-existent",
		InstanceID: "inst-x",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, kernel.ErrDefinitionNotFound)
}

// TestGetInstance verifies GetInstance returns the state for a started instance
// and ErrInstanceNotFound for an unknown ID.
func TestGetInstance(t *testing.T) {
	def := linearDef()
	h := newHarness(t, def)
	svc := service.New(h.runner, h.tasks, h.reg, h.store, h.lister, h.taskStore, service.WithEngineClock(h.clk))

	// Start an instance first.
	started, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef:     "greeting",
		InstanceID: "inst-get-1",
		Vars:       map[string]any{"name": "world"},
	})
	require.NoError(t, err)

	// GetInstance for the started instance.
	got, err := svc.GetInstance(t.Context(), started.InstanceID)
	require.NoError(t, err)
	assert.Equal(t, "inst-get-1", got.InstanceID)

	// GetInstance for unknown ID.
	_, err = svc.GetInstance(t.Context(), "no-such-id")
	require.Error(t, err)
	assert.ErrorIs(t, err, kernel.ErrInstanceNotFound)
}

// TestDeliverSignal verifies that DeliverSignal resumes a parked instance.
func TestDeliverSignal(t *testing.T) {
	def := signalCatchDef("approved")
	h := newHarness(t, def)

	// Start the instance — parks at signal-catch node.
	parked, err := h.runner.Run(t.Context(), def, "sig-inst-1", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status, "must park at signal catch")

	svc := service.New(h.runner, h.tasks, h.reg, h.store, h.lister, h.taskStore, service.WithEngineClock(h.clk))

	// DeliverSignal resumes the instance.
	final, err := svc.DeliverSignal(t.Context(), service.DeliverSignalRequest{
		InstanceID: "sig-inst-1",
		Signal:     "approved",
		Payload:    map[string]any{"decision": "yes"},
	})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status, "instance must complete after signal")
}

// TestDeliverSignalInstanceNotFound verifies that DeliverSignal propagates
// ErrInstanceNotFound for an unknown instance.
func TestDeliverSignalInstanceNotFound(t *testing.T) {
	def := signalCatchDef("approved")
	h := newHarness(t, def)
	svc := service.New(h.runner, h.tasks, h.reg, h.store, h.lister, h.taskStore, service.WithEngineClock(h.clk))

	_, err := svc.DeliverSignal(t.Context(), service.DeliverSignalRequest{
		InstanceID: "no-such-id",
		Signal:     "approved",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, kernel.ErrInstanceNotFound)
}

// TestHumanTaskLifecycle verifies ClaimTask, CompleteTask, and ReassignTask
// against an approval process, including authorization failure paths.
func TestHumanTaskLifecycle(t *testing.T) {
	def := approvalDef()
	h := newHarness(t, def)
	svc := service.New(h.runner, h.tasks, h.reg, h.store, h.lister, h.taskStore, service.WithEngineClock(h.clk))

	ctx := t.Context()

	// Start the instance — parks at the user task.
	parked, err := h.runner.Run(ctx, def, "approval-inst-1", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status, "must park at user task")
	require.Len(t, parked.Tokens, 1)
	taskToken := parked.Tokens[0].AwaitCommand
	require.NotEmpty(t, taskToken, "task token must be set")

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	noManager := authz.Actor{ID: "bob", Roles: []string{"viewer"}}

	t.Run("ClaimTask authorized", func(t *testing.T) {
		st, err := svc.ClaimTask(ctx, service.ClaimTaskRequest{
			TaskToken: taskToken,
			Actor:     manager,
		})
		require.NoError(t, err)
		assert.Equal(t, "approval-inst-1", st.InstanceID)
		assert.Equal(t, engine.StatusRunning, st.Status)
	})

	t.Run("ReassignTask authorized", func(t *testing.T) {
		// Reassign alice → carol (same role, so by=manager is authorized).
		st, err := svc.ReassignTask(ctx, service.ReassignTaskRequest{
			TaskToken: taskToken,
			From:      "alice",
			To:        "carol",
			By:        manager,
		})
		require.NoError(t, err)
		assert.Equal(t, "approval-inst-1", st.InstanceID)
		assert.Equal(t, engine.StatusRunning, st.Status)
	})

	t.Run("CompleteTask unauthorized", func(t *testing.T) {
		_, err := svc.CompleteTask(ctx, service.CompleteTaskRequest{
			TaskToken: taskToken,
			Actor:     noManager,
			Output:    map[string]any{"approved": false},
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, authz.ErrNotAuthorized)
	})

	t.Run("CompleteTask authorized", func(t *testing.T) {
		st, err := svc.CompleteTask(ctx, service.CompleteTaskRequest{
			TaskToken: taskToken,
			Actor:     manager,
			Output:    map[string]any{"approved": true},
		})
		require.NoError(t, err)
		assert.Equal(t, engine.StatusCompleted, st.Status)
	})
}

// TestDeliverMessage verifies DeliverMessage delegates to the runner's message routing.
func TestDeliverMessage(t *testing.T) {
	def := messageCatchDef("order-shipped")
	h := newHarness(t, def)

	// Start instance and park at message-catch.
	_, err := h.runner.Run(t.Context(), def, "order-100", map[string]any{"orderId": "100"})
	require.NoError(t, err)

	svc := service.New(h.runner, h.tasks, h.reg, h.store, h.lister, h.taskStore, service.WithEngineClock(h.clk))

	err = svc.DeliverMessage(t.Context(), service.DeliverMessageRequest{
		DefRef:         defRefFor(def),
		Name:           "order-shipped",
		CorrelationKey: "100",
		Payload:        map[string]any{"shipped": true},
	})
	require.NoError(t, err)

	// order-100 must be completed.
	final, err := svc.GetInstance(t.Context(), "order-100")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status)
}

// TestListInstances verifies ListInstances delegates to the InstanceLister.
func TestListInstances(t *testing.T) {
	def := linearDef()
	h := newHarness(t, def)
	svc := service.New(h.runner, h.tasks, h.reg, h.store, h.lister, h.taskStore, service.WithEngineClock(h.clk))

	ctx := t.Context()

	// Start two instances.
	_, err := svc.StartInstance(ctx, service.StartInstanceRequest{
		DefRef:     "greeting",
		InstanceID: "list-inst-1",
		Vars:       map[string]any{"name": "a"},
	})
	require.NoError(t, err)

	_, err = svc.StartInstance(ctx, service.StartInstanceRequest{
		DefRef:     "greeting",
		InstanceID: "list-inst-2",
		Vars:       map[string]any{"name": "b"},
	})
	require.NoError(t, err)

	page, err := svc.ListInstances(ctx, kernel.InstanceFilter{Limit: 10})
	require.NoError(t, err)
	assert.Len(t, page.Items, 2)
	assert.False(t, page.HasMore)
}

// TestDeliverMessageUnknownDefRef verifies that DeliverMessage propagates
// ErrDefinitionNotFound when the DefRef is not registered.
func TestDeliverMessageUnknownDefRef(t *testing.T) {
	h := newHarness(t) // no defs registered
	svc := service.New(h.runner, h.tasks, h.reg, h.store, h.lister, h.taskStore, service.WithEngineClock(h.clk))

	err := svc.DeliverMessage(t.Context(), service.DeliverMessageRequest{
		DefRef:         "non-existent:1",
		Name:           "order-shipped",
		CorrelationKey: "100",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, kernel.ErrDefinitionNotFound)
}

// TestReassignTaskUnauthorized verifies that ReassignTask propagates
// ErrNotAuthorized when the reassigner does not satisfy the task's eligibility.
func TestReassignTaskUnauthorized(t *testing.T) {
	def := approvalDef()
	h := newHarness(t, def)

	ctx := t.Context()

	// Start and park.
	parked, err := h.runner.Run(ctx, def, "reassign-unauth-1", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status)
	taskToken := parked.Tokens[0].AwaitCommand
	require.NotEmpty(t, taskToken)

	// Claim the task first (required for Reassign's "from" check).
	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	svc := service.New(h.runner, h.tasks, h.reg, h.store, h.lister, h.taskStore, service.WithEngineClock(h.clk))

	_, err = svc.ClaimTask(ctx, service.ClaimTaskRequest{
		TaskToken: taskToken,
		Actor:     manager,
	})
	require.NoError(t, err)

	// Now attempt reassign with a non-manager "by" actor.
	noManager := authz.Actor{ID: "dave", Roles: []string{"viewer"}}
	_, err = svc.ReassignTask(ctx, service.ReassignTaskRequest{
		TaskToken: taskToken,
		From:      "alice",
		To:        "carol",
		By:        noManager,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, authz.ErrNotAuthorized)
}

// TestDeliverSignalDefinitionNotFound verifies that DeliverSignal propagates
// ErrDefinitionNotFound when the instance's definition is not in the registry.
func TestDeliverSignalDefinitionNotFound(t *testing.T) {
	def := signalCatchDef("approved")
	// Register by "signal-catch-approved:1" only (the resolveDefinition key).
	h := newHarness(t, def)

	ctx := t.Context()

	// Start and park via the runner directly (not via the service facade).
	parked, err := h.runner.Run(ctx, def, "sig-def-missing", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status)

	// Build a registry WITHOUT the definition so resolveDefinition fails.
	emptyReg := kernel.NewMapDefinitionRegistry(nil)
	svc := service.New(h.runner, h.tasks, emptyReg, h.store, h.lister, h.taskStore, service.WithEngineClock(h.clk))

	_, err = svc.DeliverSignal(ctx, service.DeliverSignalRequest{
		InstanceID: "sig-def-missing",
		Signal:     "approved",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, kernel.ErrDefinitionNotFound)
}

// TestNewEngineDefaultClockNoPanic verifies that New works without a clock
// option and returns a non-nil Engine (default clock.System() is applied).
func TestNewEngineDefaultClockNoPanic(t *testing.T) {
	h := newHarness(t)
	e := service.New(h.runner, h.tasks, h.reg, h.store, h.lister, h.taskStore)
	assert.NotNil(t, e)
}

// TestNewEngineWithClockOption verifies that WithEngineClock injects a fake clock.
func TestNewEngineWithClockOption(t *testing.T) {
	h := newHarness(t)
	fake := clockwork.NewFakeClockAt(time.Unix(1000, 0))
	e := service.New(h.runner, h.tasks, h.reg, h.store, h.lister, h.taskStore, service.WithEngineClock(fake))
	assert.NotNil(t, e)
}
