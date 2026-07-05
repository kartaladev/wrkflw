// Package runtimetest provides shared test-support constructors, doubles, and
// process-definition fixtures for the runtime package tree. It exists so the
// root runtime_test package and every behavioural sub-package's test package
// (kernel, signal, calllink, chain, task, monitor) can share one copy of these
// helpers instead of duplicating them per package.
//
// It is an internal package: only code under runtime/ may import it, and in
// practice only _test.go files do, so it is never linked into a shipped binary.
// Every helper is built purely from the exported runtime APIs.
package runtimetest

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/calllink"
	"github.com/zakyalvan/krtlwrkflw/runtime/chain"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/monitor"
	"github.com/zakyalvan/krtlwrkflw/runtime/signal"
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
)

// MustMemStore builds a MemStore or fails the test. Keeps option-free call sites terse.
func MustMemStore(t *testing.T, opts ...kernel.MemInstanceStoreOption) *kernel.MemInstanceStore {
	t.Helper()
	m, err := kernel.NewMemInstanceStore(opts...)
	require.NoError(t, err)
	return m
}

// MustRunner builds a ProcessDriver with the given catalog and store, failing the
// test on any error. A nil catalog defaults to an empty MapCatalog.
func MustRunner(t *testing.T, cat action.Catalog, store kernel.InstanceStore, opts ...runtime.Option) *runtime.ProcessDriver {
	t.Helper()
	if cat == nil {
		cat = action.NewMapCatalog(nil)
	}
	r, err := runtime.NewProcessDriver(cat, store, opts...)
	require.NoError(t, err)
	return r
}

// MustTaskService builds a TaskService with the given store and authorizer,
// failing the test on any error.
func MustTaskService(t *testing.T, store humantask.TaskStore, az authz.Authorizer, opts ...task.TaskServiceOption) *task.TaskService {
	t.Helper()
	svc, err := task.NewTaskService(store, az, opts...)
	require.NoError(t, err)
	return svc
}

// MustCachingStore builds a CachingStore or fails the test.
func MustCachingStore(t *testing.T, backing kernel.InstanceStore, owner kernel.Ownership, opts ...kernel.CachingInstanceStoreOption) *kernel.CachingInstanceStore {
	t.Helper()
	s, err := kernel.NewCachingInstanceStore(backing, owner, opts...)
	require.NoError(t, err)
	return s
}

// MustCachingDefinitionRegistry builds a CachingDefinitionRegistry or fails the test.
func MustCachingDefinitionRegistry(t *testing.T, backing kernel.DefinitionRegistry, ttl time.Duration, opts ...kernel.CachingDefinitionRegistryOption) *kernel.CachingDefinitionRegistry {
	t.Helper()
	c, err := kernel.NewCachingDefinitionRegistry(backing, ttl, opts...)
	require.NoError(t, err)
	return c
}

// MustSignalBus builds a SignalBus or fails the test.
func MustSignalBus(t *testing.T, deliver signal.DeliverFunc, opts ...signal.SignalBusOption) *signal.SignalBus {
	t.Helper()
	bus, err := signal.NewSignalBus(deliver, opts...)
	require.NoError(t, err)
	return bus
}

// MustCallNotifier builds a CallNotifier or fails the test.
func MustCallNotifier(t *testing.T, cl kernel.CallLinkStore, deliver calllink.CallDeliverFunc, reg kernel.DefinitionRegistry, opts ...calllink.CallNotifierOption) *calllink.CallNotifier {
	t.Helper()
	n, err := calllink.NewCallNotifier(cl, deliver, reg, opts...)
	require.NoError(t, err)
	return n
}

// MustChainer builds a Chainer or fails the test.
func MustChainer(t *testing.T, starter chain.InstanceStarter, policy chain.SuccessorPolicy, opts ...chain.ChainerOption) *chain.Chainer {
	t.Helper()
	c, err := chain.NewChainer(starter, policy, opts...)
	require.NoError(t, err)
	return c
}

// MustLineageReader builds a LineageReader or fails the test.
func MustLineageReader(t *testing.T, calls kernel.CallLineageReader, chains kernel.ChainLineageReader) *monitor.LineageReader {
	t.Helper()
	r, err := monitor.NewLineageReader(calls, chains)
	require.NoError(t, err)
	return r
}
