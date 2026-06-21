package runtime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func TestAlwaysOwnAlwaysAcquires(t *testing.T) {
	var o runtime.Ownership = runtime.AlwaysOwn{}

	owned, err := o.Acquire(t.Context(), "any-instance")
	require.NoError(t, err)
	assert.True(t, owned)

	assert.NoError(t, o.Release(t.Context(), "any-instance"))
}
