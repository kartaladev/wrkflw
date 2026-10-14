package kernel_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// TestBaselineMemRegistryRefusesDifferentContentAndKeepsTheFirst characterizes
// the definition-store scenario "A duplicate version with different content is
// refused", row "an in-memory registry" (openspec/specs/definition-store/spec.md):
// re-registering "sub" version 2 with different content fails with
// ErrDefinitionExists and the first registration is the one that resolves.
func TestBaselineMemRegistryRefusesDifferentContentAndKeepsTheFirst(t *testing.T) {
	t.Parallel()

	reg := kernel.NewMemDefinitionRegistry()

	first := minimalValidDef("sub", 2)
	first.CancelActions = []string{"first"}
	require.NoError(t, reg.Register(first))

	second := minimalValidDef("sub", 2)
	second.CancelActions = []string{"second"}
	require.ErrorIs(t, reg.Register(second), kernel.ErrDefinitionExists)

	got, err := reg.Lookup(t.Context(), model.Version("sub", 2))
	require.NoError(t, err)
	assert.Equal(t, []string{"first"}, got.CancelActions, "the first registration must be kept")
}
