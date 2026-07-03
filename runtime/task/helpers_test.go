package task_test

// Package-scoped test helpers for task_test. These mirror the same-named helpers
// in the root runtime_test / kernel_test packages (Go test helpers cannot be
// shared across packages); keep them in sync when editing.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
)

// approvalDef returns a minimal process: start → userTask("approve", role "manager") → end.
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

// mustMemStore builds a MemStore or fails the test.
func mustMemStore(t *testing.T, opts ...kernel.MemStoreOption) *kernel.MemStore {
	t.Helper()
	m, err := kernel.NewMemStore(opts...)
	require.NoError(t, err)
	return m
}

// mustRunner builds a ProcessDriver or fails the test.
func mustRunner(t *testing.T, cat action.Catalog, store kernel.Store, opts ...runtime.Option) *runtime.ProcessDriver {
	t.Helper()
	if cat == nil {
		cat = action.NewMapCatalog(nil)
	}
	r, err := runtime.NewProcessDriver(cat, store, opts...)
	require.NoError(t, err)
	return r
}

// mustTaskService builds a TaskService or fails the test.
func mustTaskService(t *testing.T, store humantask.TaskStore, az authz.Authorizer, opts ...task.TaskServiceOption) *task.TaskService {
	t.Helper()
	svc, err := task.NewTaskService(store, az, opts...)
	require.NoError(t, err)
	return svc
}
