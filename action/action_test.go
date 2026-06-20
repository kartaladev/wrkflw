package action_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
)

func TestMapCatalogResolveAndRun(t *testing.T) {
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"greet": action.Func(func(_ context.Context, in map[string]any) (map[string]any, error) {
			return map[string]any{"greeting": "hi " + in["name"].(string)}, nil
		}),
	})

	a, ok := cat.Resolve("greet")
	require.True(t, ok)

	out, err := a.Do(t.Context(), map[string]any{"name": "Ada"})
	require.NoError(t, err)
	assert.Equal(t, "hi Ada", out["greeting"])

	_, ok = cat.Resolve("missing")
	assert.False(t, ok)
}
