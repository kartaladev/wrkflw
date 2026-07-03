package monitor_test

// Package-scoped test helper for monitor_test. Mirrors the same-named helper in
// the root runtime_test / kernel_test packages (Go test helpers cannot be shared
// across packages); keep in sync when editing.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/monitor"
)

// mustLineageReader builds a LineageReader or fails the test.
func mustLineageReader(t *testing.T, calls kernel.CallLineageReader, chains kernel.ChainLineageReader) *monitor.LineageReader {
	t.Helper()
	r, err := monitor.NewLineageReader(calls, chains)
	require.NoError(t, err)
	return r
}
