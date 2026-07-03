package runtime_test

// This helper set is intentionally duplicated with runtime/kernel's
// memstore_helper_test.go: Go test helpers are package-scoped, and both the
// root runtime_test package and the kernel_test package need the same
// constructors. Keeping a copy in each package avoids introducing a shared
// non-test support package that would drag testing.T and testify into
// production import graphs. Keep the two copies in sync when editing.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/calllink"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/signal"
)

// mustMemStore builds a MemStore or fails the test. Keeps option-free call sites terse.
func mustMemStore(t *testing.T, opts ...kernel.MemStoreOption) *kernel.MemStore {
	t.Helper()
	m, err := kernel.NewMemStore(opts...)
	require.NoError(t, err)
	return m
}

// mustRunner builds a ProcessDriver with the given catalog and store, failing the
// test on any error. Use at sites where NewProcessDriver is called many times with
// valid args.
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
func mustTaskService(t *testing.T, store humantask.TaskStore, az authz.Authorizer, opts ...runtime.TaskServiceOption) *runtime.TaskService {
	t.Helper()
	svc, err := runtime.NewTaskService(store, az, opts...)
	require.NoError(t, err)
	return svc
}

// mustCachingStore builds a CachingStore or fails the test.
func mustCachingStore(t *testing.T, backing kernel.Store, owner kernel.Ownership, opts ...kernel.CachingStoreOption) *kernel.CachingStore {
	t.Helper()
	s, err := kernel.NewCachingStore(backing, owner, opts...)
	require.NoError(t, err)
	return s
}

// mustCachingDefinitionRegistry builds a CachingDefinitionRegistry or fails the test.
func mustCachingDefinitionRegistry(t *testing.T, backing kernel.DefinitionRegistry, ttl time.Duration, opts ...kernel.CachingDefinitionRegistryOption) *kernel.CachingDefinitionRegistry {
	t.Helper()
	c, err := kernel.NewCachingDefinitionRegistry(backing, ttl, opts...)
	require.NoError(t, err)
	return c
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

// mustChainer builds a Chainer or fails the test.
func mustChainer(t *testing.T, starter runtime.InstanceStarter, policy runtime.SuccessorPolicy, opts ...runtime.ChainerOption) *runtime.Chainer {
	t.Helper()
	c, err := runtime.NewChainer(starter, policy, opts...)
	require.NoError(t, err)
	return c
}

// mustLineageReader builds a LineageReader or fails the test.
func mustLineageReader(t *testing.T, calls kernel.CallLineageReader, chains kernel.ChainLineageReader) *runtime.LineageReader {
	t.Helper()
	r, err := runtime.NewLineageReader(calls, chains)
	require.NoError(t, err)
	return r
}
