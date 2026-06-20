package expreval_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/file"
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

// TestExprNilComparisonErrorShapeUnchanged is a canary test that guards against
// expr-lang/expr changing its nil-operand error format in a future upgrade.
// EvalBool silently treats a nil-operand runtime error as (false, nil) — meaning
// a process variable that is absent from the env causes a gateway condition to
// evaluate to false rather than to an error.  This relies on the error produced
// by expr when AllowUndefinedVariables() compiles a nil identifier into a
// comparison being a *file.Error whose Message starts with "invalid operation:"
// and contains "<nil>".
//
// Verified against github.com/expr-lang/expr v1.17.8.
//
// If this test fails after an expr upgrade it means the library changed its
// error wording; review isNilOperandError in expreval.go before proceeding.
func TestExprNilComparisonErrorShapeUnchanged(t *testing.T) {
	// Compile with AllowUndefinedVariables so that "amount" compiles to nil.
	p, err := expr.Compile("amount > 100", expr.AllowUndefinedVariables())
	require.NoError(t, err)

	// Run with an empty env so "amount" resolves to nil at runtime.
	_, runErr := expr.Run(p, map[string]any{})
	require.Error(t, runErr, "expr.Run must error when comparing nil to int")

	// The error must be a *file.Error — the typed wrapper the VM uses for panics.
	var fileErr *file.Error
	require.True(t, errors.As(runErr, &fileErr),
		"expr runtime error must be *file.Error (got %T: %v)", runErr, runErr)

	// The Message field (without location / snippet decoration) must contain both
	// "invalid operation:" and "<nil>", which isNilOperandError relies on.
	assert.True(t, strings.HasPrefix(fileErr.Message, "invalid operation:"),
		"file.Error.Message must start with %q (got %q)", "invalid operation:", fileErr.Message)
	assert.Contains(t, fileErr.Message, "<nil>",
		"file.Error.Message must contain %q (got %q)", "<nil>", fileErr.Message)
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
