// Package transporttest provides a reusable in-memory test harness for the
// transport/http/* packages. It wires a real service.Service backed by in-memory
// stores so endpoint tests exercise the full service layer without mocks.
//
// Precedent: database.RunTestDatabase(t) — the harness is a non-test .go file
// so it can be imported by every transport/http/* test package.
package transporttest

import (
	"context"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/service"
)

// Harness holds the low-level components wired by NewHarness.
// Tests that need direct access to kernel internals (e.g. to run a definition
// and capture the resulting task token) can reach through it.
type Harness struct {
	Driver    *runtime.ProcessDriver
	Store     *kernel.MemInstanceStore
	TaskStore *humantask.MemTaskStore
	Clock     *clockwork.FakeClock
}

// greetAction is the default service action registered under "greet".
type greetAction struct{}

func (greetAction) Do(_ context.Context, in map[string]any) (map[string]any, error) {
	name, _ := in["name"].(string)
	return map[string]any{"greeting": "hi " + name}, nil
}

// NewHarness constructs a ready service.Service backed by in-memory stores and a
// FakeClock. The returned *Harness exposes the underlying components for tests
// that need to seed state directly (e.g. run a definition to park at a user task
// and capture the task token).
//
// defs are registered immediately; pass them as variadic arguments so tests that
// only need a subset avoid registering unnecessary definitions.
func NewHarness(t testing.TB, defs ...*model.ProcessDefinition) (*Harness, service.Service) {
	t.Helper()

	fc := clockwork.NewFakeClock()

	store, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)

	taskStore := humantask.NewMemTaskStore()

	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"manager": {{ID: "alice", Roles: []string{"manager"}}},
	})
	az := authz.RoleAuthorizer{}

	cat := action.NewCatalog(map[string]action.Action{
		"greet": greetAction{},
	})

	// NewMapDefinitionRegistry indexes each definition under both its short ("id")
	// and versioned ("id:version") keys internally. The driver resolves a
	// correlated instance's definition from this registry (ADR-0121), so it is
	// wired into the driver as well as the service facade.
	reg := kernel.NewMapDefinitionRegistry(defs...)

	driver, err := runtime.NewProcessDriver(
		runtime.WithActionCatalog(cat),
		runtime.WithInstanceStore(store),
		runtime.WithClock(fc),
		runtime.WithHumanTasks(resolver, taskStore, az),
		runtime.WithDefinitions(reg),
	)
	require.NoError(t, err)

	svc, err := service.NewEngine(
		service.WithProcessDriver(driver),
		service.WithInstanceStore(store),
		service.WithDefinitions(reg),
		service.WithLister(store),
		service.WithHumanTasks(taskStore, az),
		service.WithClock(fc),
	)
	require.NoError(t, err)

	h := &Harness{
		Driver:    driver,
		Store:     store,
		TaskStore: taskStore,
		Clock:     fc,
	}
	return h, svc
}

// --- Standard process definitions ---

// LinearProcess is a simple greeting process:
//
//	start → greet (service task) → end
func LinearProcess() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "greeting", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("greet", activity.WithTaskAction("greet")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "greet"},
			{ID: "f2", Source: "greet", Target: "end"},
		},
	}
}

// ApprovalProcess is a human-task approval process:
//
//	start → approve (user task, role=manager) → end
func ApprovalProcess() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "approval", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("approve", activity.WithEligibleRoles("manager")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}
}

// SignalProcess is a signal-catch process:
//
//	start → wait (catch signal=signalName) → end
func SignalProcess(signalName string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "signal-catch-" + signalName, Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewIntermediateCatch("wait", event.WithSignalName(signalName)),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait"},
			{ID: "f2", Source: "wait", Target: "end"},
		},
	}
}

// MessageProcess is a message-catch process:
//
//	start → wait-msg (catch message=msgName, correlationKey="orderId") → end
func MessageProcess(msgName string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "message-catch-" + msgName, Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewIntermediateCatch("wait-msg", event.WithMessageCorrelator(msgName, "orderId")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait-msg"},
			{ID: "f2", Source: "wait-msg", Target: "end"},
		},
	}
}

// StartedApprovalInstance runs a fresh approval process instance and parks at
// the user-task node. It returns the task token for use in claim/complete/reassign
// tests. Calls t.Fatal when the instance does not park at a user task.
func StartedApprovalInstance(t testing.TB, h *Harness, instanceID string) (taskToken string) {
	t.Helper()
	def := ApprovalProcess()
	st, err := h.Driver.Drive(context.Background(), def, instanceID, nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, st.Status, "approval instance must park at user task")
	require.NotEmpty(t, st.Tokens, "approval instance must have at least one parked token")
	token := st.Tokens[0].AwaitCommand
	require.NotEmpty(t, token, "task token must not be empty")
	return token
}
