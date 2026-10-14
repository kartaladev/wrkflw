package expr_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vexpr "github.com/kartaladev/wrkflw/definition/model/validate/expr"
)

// TestBaselineExprValidationNamesFailingPredicate characterizes the expr
// validation kind of the expression-evaluation capability
// (openspec/specs/expression-evaluation/spec.md): a refusal names the predicate
// that failed, whether it evaluated false or referenced an absent variable. The
// validator quotes the predicate with %q, so the assertion looks for its
// Go-quoted form.
func TestBaselineExprValidationNamesFailingPredicate(t *testing.T) {
	t.Parallel()

	const (
		decisionPred = `decision in ['approve','reject']`
		amountPred   = `amount > 0`
	)

	type testCase struct {
		name   string
		input  map[string]any
		assert func(t *testing.T, err error)
	}

	refusedNaming := func(pred, other string) func(t *testing.T, err error) {
		return func(t *testing.T, err error) {
			t.Helper()
			require.Error(t, err)
			assert.Contains(t, err.Error(), strconv.Quote(pred), "the error must name the failing predicate")
			assert.NotContains(t, err.Error(), strconv.Quote(other), "only the failing predicate is named")
		}
	}

	cases := []testCase{
		{
			name:   "a predicate that evaluates false is named",
			input:  map[string]any{"decision": "maybe", "amount": 5},
			assert: refusedNaming(decisionPred, amountPred),
		},
		{
			name:   "a predicate over an absent variable is named",
			input:  map[string]any{"decision": "approve"},
			assert: refusedNaming(amountPred, decisionPred),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v, err := vexpr.New(decisionPred, amountPred).NewValidator()
			require.NoError(t, err)

			tc.assert(t, v.Validate(t.Context(), tc.input))
		})
	}
}
