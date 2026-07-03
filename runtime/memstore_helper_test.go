package runtime_test

// Package-scoped test helpers for the root runtime_test package. Mirrors of these
// constructors live in runtime/kernel and the behavioural sub-packages' test
// packages (Go test helpers cannot be shared across packages); keep the copies in
// sync when editing. Only the helpers the root package's tests actually use are
// kept here.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/calllink"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/signal"
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
)

// mustMemStore builds a MemStore or fails the test. Keeps option-free call sites terse.
func mustMemStore(t *testing.T, opts ...kernel.MemStoreOption) *kernel.MemStore {
	t.Helper()
	m, err := kernel.NewMemStore(opts...)
	require.NoError(t, err)
	return m
}

// mustRunner builds a ProcessDriver with the given catalog and store, failing the
// test on any error.
func mustRunner(t *testing.T, cat action.Catalog, store kernel.Store, opts ...runtime.Option) *runtime.ProcessDriver {
	t.Helper()
	if cat == nil {
		cat = action.NewMapCatalog(nil)
	}
	r, err := runtime.NewProcessDriver(cat, store, opts...)
	require.NoError(t, err)
	return r
}

// mustTaskService builds a TaskService with the given store and authorizer,
// failing the test on any error.
func mustTaskService(t *testing.T, store humantask.TaskStore, az authz.Authorizer, opts ...task.TaskServiceOption) *task.TaskService {
	t.Helper()
	svc, err := task.NewTaskService(store, az, opts...)
	require.NoError(t, err)
	return svc
}

// mustSignalBus builds a SignalBus or fails the test.
func mustSignalBus(t *testing.T, deliver signal.DeliverFunc, opts ...signal.SignalBusOption) *signal.SignalBus {
	t.Helper()
	bus, err := signal.NewSignalBus(deliver, opts...)
	require.NoError(t, err)
	return bus
}

// mustCallNotifier builds a CallNotifier or fails the test.
func mustCallNotifier(t *testing.T, cl kernel.CallLinkStore, deliver calllink.CallDeliverFunc, reg kernel.DefinitionRegistry, opts ...calllink.CallNotifierOption) *calllink.CallNotifier {
	t.Helper()
	n, err := calllink.NewCallNotifier(cl, deliver, reg, opts...)
	require.NoError(t, err)
	return n
}
