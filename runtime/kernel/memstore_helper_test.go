package kernel_test

// Package-scoped test helpers for kernel_test. Mirrors of these constructors
// live in the root runtime_test package and the behavioural sub-packages'
// test packages (Go test helpers cannot be shared across packages); keep the
// copies in sync when editing. Only the helpers kernel_test actually uses are
// kept here.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
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
