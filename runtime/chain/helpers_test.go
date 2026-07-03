package chain_test

// Package-scoped test helper for chain_test. Mirrors the same-named helper in
// the root runtime_test / kernel_test packages (Go test helpers cannot be shared
// across packages); keep in sync when editing.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/runtime/chain"
)

// mustChainer builds a Chainer or fails the test.
func mustChainer(t *testing.T, starter chain.InstanceStarter, policy chain.SuccessorPolicy, opts ...chain.ChainerOption) *chain.Chainer {
	t.Helper()
	c, err := chain.NewChainer(starter, policy, opts...)
	require.NoError(t, err)
	return c
}
