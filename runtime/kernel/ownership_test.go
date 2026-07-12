package kernel_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlwaysOwnAlwaysAcquires(t *testing.T) {
	var o kernel.InstanceOwnership = kernel.AlwaysOwn{}

	owned, err := o.Acquire(t.Context(), "any-instance")
	require.NoError(t, err)
	assert.True(t, owned)

	assert.NoError(t, o.Release(t.Context(), "any-instance"))
}
