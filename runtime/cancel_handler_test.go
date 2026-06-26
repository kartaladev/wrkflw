package runtime_test

// cancel_handler_test.go — Task 2: runtime e2e for per-node cancel handlers.
//
// Design: docs/specs/2026-06-23-cancel-handlers-design.md §3 (runtime bullet).
// ADR: 0035.
//
// Verifies that when a running instance is cancelled, the engine emits an
// InvokeCancelAction for each active node whose Node.CancelHandler is non-empty,
// and the runner executes it best-effort before marking the instance Terminated.

import (
	"context"
	"sync/atomic"
	"testing"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// cancelHandlerDef returns:
//
//	start → approve (KindUserTask, CancelHandler:"cleanup") → end
//
// The user task parks the instance (StatusRunning). On cancel the engine emits
// InvokeCancelAction{Name:"cleanup"} for the active "approve" node.
func cancelHandlerDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "cancel-handler-def", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewUserTask("approve", []string{"reviewer"}, model.WithCancelHandler("cleanup")),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}
}

// TestRunnerPerNodeCancelHandlerFires is the runtime e2e for per-node cancel
// handlers (ADR-0035).
//
// Asserts:
//  1. Run parks at the user task → StatusRunning.
//  2. CancelInstance executes the "cleanup" cancel handler exactly once.
//  3. A failing cancel handler does NOT fail CancelInstance (best-effort).
//  4. Final status is StatusTerminated with no live tokens.
func TestRunnerPerNodeCancelHandlerFires(t *testing.T) {
	fc := clockwork.NewFakeClock()

	var cleanupRan atomic.Int32
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"cleanup": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			cleanupRan.Add(1)
			return nil, nil
		}),
	})

	store := runtime.NewMemStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()
	r := runtime.NewRunner(cat, store, runtime.WithRunnerClock(fc), runtime.WithHumanTasks(resolver, tasks, nil))

	def := cancelHandlerDef()
	const instanceID = "ch-i1"

	// Run: parks at the user task.
	runSt, err := r.Run(t.Context(), def, instanceID, nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, runSt.Status, "instance must park at approve (StatusRunning)")
	assert.EqualValues(t, 0, cleanupRan.Load(), "cleanup must not run before cancel")

	// Cancel: node cancel handler must fire.
	cancelSt, err := r.CancelInstance(t.Context(), def, instanceID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusTerminated, cancelSt.Status, "instance must be terminated after cancel")
	assert.Empty(t, cancelSt.Tokens, "no live tokens after cancel")
	assert.EqualValues(t, 1, cleanupRan.Load(), "cleanup cancel handler must run exactly once")
}

// TestRunnerPerNodeCancelHandlerFailIsBestEffort verifies that a failing
// per-node cancel handler does NOT cause CancelInstance to return an error.
func TestRunnerPerNodeCancelHandlerFailIsBestEffort(t *testing.T) {
	fc := clockwork.NewFakeClock()

	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"cleanup": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return nil, assert.AnError // always fails
		}),
	})

	store := runtime.NewMemStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()
	r := runtime.NewRunner(cat, store, runtime.WithRunnerClock(fc), runtime.WithHumanTasks(resolver, tasks, nil))

	def := cancelHandlerDef()
	const instanceID = "ch-i2"

	_, err := r.Run(t.Context(), def, instanceID, nil)
	require.NoError(t, err)

	cancelSt, err := r.CancelInstance(t.Context(), def, instanceID)
	require.NoError(t, err, "a failing per-node cancel handler must not fail CancelInstance")
	assert.Equal(t, engine.StatusTerminated, cancelSt.Status)
}

// TestRunnerPerNodeCancelHandlerMissingActionBestEffort verifies that when the
// cancel handler name resolves to nothing in the catalog, CancelInstance still
// succeeds and returns StatusTerminated.
func TestRunnerPerNodeCancelHandlerMissingActionBestEffort(t *testing.T) {
	fc := clockwork.NewFakeClock()

	store := runtime.NewMemStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()
	// Empty catalog — "cleanup" will not resolve.
	r := runtime.NewRunner(action.NewMapCatalog(nil), store, runtime.WithRunnerClock(fc), runtime.WithHumanTasks(resolver, tasks, nil))

	def := cancelHandlerDef()
	const instanceID = "ch-i3"

	_, err := r.Run(t.Context(), def, instanceID, nil)
	require.NoError(t, err)

	cancelSt, err := r.CancelInstance(t.Context(), def, instanceID)
	require.NoError(t, err, "an unresolved per-node cancel handler must not fail CancelInstance")
	assert.Equal(t, engine.StatusTerminated, cancelSt.Status)
}
