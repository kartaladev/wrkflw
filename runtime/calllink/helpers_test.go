package calllink_test

// Package-scoped test helpers for calllink_test. These mirror the same-named
// helpers in runtime/kernel and the root runtime_test package (Go test helpers
// cannot be shared across packages); keep them in sync when editing.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/calllink"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

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
func mustTaskService(t *testing.T, store humantask.TaskStore, az authz.Authorizer, opts ...runtime.TaskServiceOption) *runtime.TaskService {
	t.Helper()
	svc, err := runtime.NewTaskService(store, az, opts...)
	require.NoError(t, err)
	return svc
}

// mustCallNotifier builds a CallNotifier or fails the test.
func mustCallNotifier(t *testing.T, cl kernel.CallLinkStore, deliver calllink.CallDeliverFunc, reg kernel.DefinitionRegistry, opts ...calllink.CallNotifierOption) *calllink.CallNotifier {
	t.Helper()
	n, err := calllink.NewCallNotifier(cl, deliver, reg, opts...)
	require.NoError(t, err)
	return n
}
