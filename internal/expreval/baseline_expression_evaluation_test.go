package expreval_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/expreval"
)

// TestBaselineExpressionRefusalNamesTheExpression characterizes the "refused with
// an error naming the expression" clause of the expression-evaluation capability
// (openspec/specs/expression-evaluation/spec.md) for conditions, durations and
// correlation keys. The evaluator quotes the expression with %q, so the
// assertion looks for its Go-quoted form.
func TestBaselineExpressionRefusalNamesTheExpression(t *testing.T) {
	t.Parallel()

	evalBool := func(e *expreval.Evaluator, code string, env map[string]any) error {
		_, err := e.EvalBool(code, env)
		return err
	}
	evalDuration := func(e *expreval.Evaluator, code string, env map[string]any) error {
		_, err := e.EvalDuration(code, env)
		return err
	}
	evalString := func(e *expreval.Evaluator, code string, env map[string]any) error {
		_, err := e.EvalString(code, env)
		return err
	}

	type testCase struct {
		name   string
		code   string
		env    map[string]any
		eval   func(e *expreval.Evaluator, code string, env map[string]any) error
		assert func(t *testing.T, code string, err error)
	}

	namesExpression := func(t *testing.T, code string, err error) {
		t.Helper()
		require.Error(t, err)
		assert.Contains(t, err.Error(), strconv.Quote(code), "the error must name the expression")
	}

	cases := []testCase{
		{
			name: "condition that does not compile",
			code: "amount >", env: map[string]any{"amount": 1},
			eval: evalBool, assert: namesExpression,
		},
		{
			name: "condition with a non-boolean result",
			code: "amount + 1", env: map[string]any{"amount": 1},
			eval: evalBool, assert: namesExpression,
		},
		{
			name: "duration with a boolean result",
			code: "true", env: map[string]any{},
			eval: evalDuration, assert: namesExpression,
		},
		{
			name: "duration with an unparseable string result",
			code: `"xyz"`, env: map[string]any{},
			eval: evalDuration, assert: namesExpression,
		},
		{
			name: "duration that does not compile",
			code: "duration >", env: map[string]any{},
			eval: evalDuration, assert: namesExpression,
		},
		{
			name: "correlation key that does not compile",
			code: "orderId >", env: map[string]any{"orderId": "x"},
			eval: evalString, assert: namesExpression,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.eval(expreval.New(), tc.code, tc.env)
			tc.assert(t, tc.code, err)
		})
	}
}
