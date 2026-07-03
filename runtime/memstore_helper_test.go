package runtime_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// mustMemStore builds a MemStore or fails the test. Keeps option-free call sites terse.
func mustMemStore(t *testing.T, opts ...runtime.MemStoreOption) *runtime.MemStore {
	t.Helper()
	m, err := runtime.NewMemStore(opts...)
	require.NoError(t, err)
	return m
}

// mustRunner builds a Runner with the given catalog and store, failing the test
// on any error. Use at sites where NewRunner is called many times with valid args.
func mustRunner(t *testing.T, cat action.Catalog, store runtime.Store, opts ...runtime.Option) *runtime.ProcessDriver {
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
func mustCachingStore(t *testing.T, backing runtime.Store, owner runtime.Ownership, opts ...runtime.CachingStoreOption) *runtime.CachingStore {
	t.Helper()
	s, err := runtime.NewCachingStore(backing, owner, opts...)
	require.NoError(t, err)
	return s
}

// mustCachingDefinitionRegistry builds a CachingDefinitionRegistry or fails the test.
func mustCachingDefinitionRegistry(t *testing.T, backing runtime.DefinitionRegistry, ttl time.Duration, opts ...runtime.CachingDefinitionRegistryOption) *runtime.CachingDefinitionRegistry {
	t.Helper()
	c, err := runtime.NewCachingDefinitionRegistry(backing, ttl, opts...)
	require.NoError(t, err)
	return c
}

// mustSignalBus builds a SignalBus or fails the test.
func mustSignalBus(t *testing.T, deliver runtime.DeliverFunc, opts ...runtime.SignalBusOption) *runtime.SignalBus {
	t.Helper()
	bus, err := runtime.NewSignalBus(deliver, opts...)
	require.NoError(t, err)
	return bus
}

// mustCallNotifier builds a CallNotifier or fails the test.
func mustCallNotifier(t *testing.T, cl runtime.CallLinkStore, deliver runtime.CallDeliverFunc, reg runtime.DefinitionRegistry, opts ...runtime.CallNotifierOption) *runtime.CallNotifier {
	t.Helper()
	n, err := runtime.NewCallNotifier(cl, deliver, reg, opts...)
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
func mustLineageReader(t *testing.T, calls runtime.CallLineageReader, chains runtime.ChainLineageReader) *runtime.LineageReader {
	t.Helper()
	r, err := runtime.NewLineageReader(calls, chains)
	require.NoError(t, err)
	return r
}
