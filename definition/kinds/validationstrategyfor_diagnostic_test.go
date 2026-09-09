package kinds_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	_ "github.com/kartaladev/wrkflw/definition/kinds"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/model/validate"
)

// TestValidationStrategyForDiagnosticOnForeignNode covers the #147 residue: the
// exported model.ValidationStrategyFor still panics on a foreign node under a
// kind with a ValidationGet slot — its signature is deliberately left
// unchanged (see ErrForeignNodeType's doc comment) — but the panic now carries
// the same diagnostic Validate would report, instead of Go's opaque "interface
// conversion" message from the leaf ValidationGet's own bare assertion. This
// is an improvement to the residue, not a fix: a direct caller still panics,
// it is just told why.
//
// The call and its recover() live in the shared loop below, not inside each
// case's assert closure: both cases invoke the same SUT the same way, so only
// the assertion over the outcome (result, recovered) should vary per case —
// the table-test rule this file previously violated by giving each case its
// own run func.
func TestValidationStrategyForDiagnosticOnForeignNode(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		node   model.Node
		assert func(t *testing.T, result validate.ValidationStrategy, recovered any)
	}

	cases := []testCase{
		{
			name: "the real leaf type under its own kind does not panic",
			node: activity.NewUserTask("task"),
			assert: func(t *testing.T, result validate.ValidationStrategy, recovered any) {
				assert.Nil(t, recovered, "expected no panic, got %v", recovered)
			},
		},
		{
			name: "a foreign node under a kind with ValidationGet panics naming ErrForeignNodeType",
			node: foreignUserTask{Base: model.NewBase("task", "task")},
			assert: func(t *testing.T, result validate.ValidationStrategy, recovered any) {
				require.NotNil(t, recovered, "expected ValidationStrategyFor to panic on a foreign node")
				err, ok := recovered.(error)
				require.True(t, ok, "panic value must be an error, got %T", recovered)
				assert.ErrorIs(t, err, model.ErrForeignNodeType)
				assert.Contains(t, err.Error(), `"task"`, "the node id locates the offender")
				assert.Contains(t, err.Error(), "activity.UserTask", "the registered type is named")
				assert.Contains(t, err.Error(), "foreignUserTask", "the actual type is named")
			},
		},
		{
			// Pins the branch #147's whole fail-open argument rests on: the
			// pre-existing early return (no ValidationGet slot) sits ABOVE the
			// type check and is unchanged by this PR, so a foreign node under a
			// kind with no ValidationGet answers nil — same as a genuine node of
			// that kind — rather than panicking or, worse, being hoisted above
			// the early return by some future tidy-up. exclusiveGateway is
			// registered but has no ValidationGet (see
			// definition/gateway/gateway.go).
			name: "a foreign node under a kind with NO ValidationGet returns nil, not a panic",
			node: foreignNode{Base: model.NewBase("g", "g"), kind: model.KindExclusiveGateway},
			assert: func(t *testing.T, result validate.ValidationStrategy, recovered any) {
				assert.Nil(t, recovered, "expected no panic, got %v", recovered)
				assert.Nil(t, result)
			},
		},
		{
			// The other half of the same branch: a kind nothing ever registered
			// has no ValidationGet to dispatch either, so it answers nil for the
			// same reason an unregistered kind is ErrKindNotRegistered's concern,
			// never this function's.
			name: "an unregistered kind returns nil, not a panic",
			node: unregisteredKindNode{Base: model.NewBase("task", "task")},
			assert: func(t *testing.T, result validate.ValidationStrategy, recovered any) {
				assert.Nil(t, recovered, "expected no panic, got %v", recovered)
				assert.Nil(t, result)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var result validate.ValidationStrategy
			recovered := func() (r any) {
				defer func() { r = recover() }()
				result = model.ValidationStrategyFor(tc.node)
				return
			}()

			tc.assert(t, result, recovered)
		})
	}
}
