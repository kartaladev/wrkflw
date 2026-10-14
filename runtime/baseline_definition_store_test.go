package runtime_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// TestBaselineRegisterDefinitionRefusesDifferentContentAndKeepsTheFirst
// characterizes the definition-store scenario "A duplicate version with
// different content is refused", row "the process-global registry, via
// RegisterDefinition" (openspec/specs/definition-store/spec.md).
//
// The spec's "sub" is suffixed with a per-test sequence number: the registry is
// process-global and first-registration-wins, so a fixed ID would collide with
// any other test in this package that registers "sub" version 2.
func TestBaselineRegisterDefinitionRefusesDifferentContentAndKeepsTheFirst(t *testing.T) {
	t.Parallel()

	id := fmt.Sprintf("sub-baseline-%d", uniqueDefSeq.Add(1))

	first := validDef(id)
	first.Version = 2
	first.CancelActions = []string{"first"}
	require.NoError(t, runtime.RegisterDefinition(first))

	second := validDef(id)
	second.Version = 2
	second.CancelActions = []string{"second"}
	require.ErrorIs(t, runtime.RegisterDefinition(second), kernel.ErrDefinitionExists)

	got, err := runtime.DefaultDefinitionRegistry().Lookup(t.Context(), model.Version(id, 2))
	require.NoError(t, err)
	assert.Equal(t, []string{"first"}, got.CancelActions, "the first registration must be kept")
}
