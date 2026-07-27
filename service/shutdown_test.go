package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/service"
)

// newOwnedProcessEngine builds a ProcessEngine that OWNS its process driver (no WithProcessDriver),
// so ProcessEngine.Shutdown drains the driver and sets its draining flag. The greeting
// definition (linearDef) is registered so StartInstance's Lookup succeeds and the
// rejection surfaces from the driver's Drive gate rather than from the registry.
func newOwnedProcessEngine(t *testing.T) *service.ProcessEngine {
	t.Helper()
	store, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	reg := kernel.NewMapDefinitionRegistry(linearDef())
	eng, err := service.NewProcessEngine(
		service.WithInstanceStore(store),
		service.WithDefinitions(reg),
		service.WithLister(store),
	)
	require.NoError(t, err)
	return eng
}

func TestEngineHumanTaskRejectedDuringShutdown(t *testing.T) {
	// Each human-task op writes to the task store before reaching the driver's gate,
	// so the ProcessEngine must reject early — before any task-store side effect — once the
	// driver is draining.
	tests := map[string]func(e *service.ProcessEngine) error{
		"ClaimTask": func(e *service.ProcessEngine) error {
			_, err := e.ClaimTask(context.Background(), service.ClaimTaskRequest{
				TaskID: "t", Actor: authz.Actor{ID: "a"},
			})
			return err
		},
		"CompleteTask": func(e *service.ProcessEngine) error {
			_, err := e.CompleteTask(context.Background(), service.CompleteTaskRequest{
				TaskID: "t", Actor: authz.Actor{ID: "a"},
			})
			return err
		},
		"ReassignTask": func(e *service.ProcessEngine) error {
			_, err := e.ReassignTask(context.Background(), service.ReassignTaskRequest{
				TaskID: "t", From: "a", To: "b", By: authz.Actor{ID: "a"},
			})
			return err
		},
		"RefreshTaskCandidates": func(e *service.ProcessEngine) error {
			_, err := e.RefreshTaskCandidates(context.Background(), service.RefreshTaskCandidatesRequest{
				TaskID: "t", By: authz.Actor{ID: "a"},
			})
			return err
		},
	}
	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			eng := newOwnedProcessEngine(t)
			require.NoError(t, eng.Shutdown(context.Background()))
			assert.ErrorIs(t, call(eng), runtime.ErrDriverShuttingDown)
		})
	}
}

func TestEngineStartInstanceRejectedDuringShutdown(t *testing.T) {
	eng := newOwnedProcessEngine(t)
	require.NoError(t, eng.Shutdown(context.Background()))

	_, err := eng.StartInstance(context.Background(), service.StartInstanceRequest{
		DefRef: model.Latest("greeting"),
	})
	assert.ErrorIs(t, err, runtime.ErrDriverShuttingDown)
}
