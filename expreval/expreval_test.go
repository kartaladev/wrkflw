package expreval_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/expreval"
)

func TestEvalBool(t *testing.T) {
	tests := map[string]struct {
		code   string
		env    map[string]any
		assert func(t *testing.T, got bool, err error)
	}{
		"true comparison": {
			code: "amount > 100", env: map[string]any{"amount": 150},
			assert: func(t *testing.T, got bool, err error) {
				require.NoError(t, err)
				assert.True(t, got)
			},
		},
		"false comparison": {
			code: "amount > 100", env: map[string]any{"amount": 50},
			assert: func(t *testing.T, got bool, err error) {
				require.NoError(t, err)
				assert.False(t, got)
			},
		},
		"undefined variable treated as nil (no error)": {
			code: "amount > 100", env: map[string]any{},
			assert: func(t *testing.T, got bool, err error) {
				require.NoError(t, err)
				assert.False(t, got)
			},
		},
		"non-bool result errors": {
			code: "amount + 1", env: map[string]any{"amount": 1},
			assert: func(t *testing.T, got bool, err error) {
				require.Error(t, err)
			},
		},
		"syntax error": {
			code: "amount >", env: map[string]any{"amount": 1},
			assert: func(t *testing.T, got bool, err error) {
				require.Error(t, err)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			e := expreval.New()
			got, err := e.EvalBool(tc.code, tc.env)
			tc.assert(t, got, err)
		})
	}
}

func TestEvalBoolMemoizes(t *testing.T) {
	e := expreval.New()
	// Same code evaluated twice with different envs must use the cached program
	// and still return per-env results.
	got1, err := e.EvalBool("x == 1", map[string]any{"x": 1})
	require.NoError(t, err)
	assert.True(t, got1)
	got2, err := e.EvalBool("x == 1", map[string]any{"x": 2})
	require.NoError(t, err)
	assert.False(t, got2)
}
