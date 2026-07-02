package runtime_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/action"
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
func mustRunner(t *testing.T, cat action.Catalog, store runtime.Store, opts ...runtime.Option) *runtime.Runner {
	t.Helper()
	if cat == nil {
		cat = action.NewMapCatalog(nil)
	}
	r, err := runtime.NewRunner(cat, store, opts...)
	require.NoError(t, err)
	return r
}
