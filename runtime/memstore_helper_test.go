package runtime_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// mustMemStore builds a MemStore or fails the test. Keeps option-free call sites terse.
func mustMemStore(t *testing.T, opts ...runtime.MemStoreOption) *runtime.MemStore {
	t.Helper()
	m, err := runtime.NewMemStore(opts...)
	require.NoError(t, err)
	return m
}
