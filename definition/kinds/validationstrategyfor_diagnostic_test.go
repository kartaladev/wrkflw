package kinds_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	_ "github.com/kartaladev/wrkflw/definition/kinds"
	"github.com/kartaladev/wrkflw/definition/model"
)

// TestValidationStrategyForDiagnosticOnForeignNode covers the #147 residue: the
// exported model.ValidationStrategyFor still panics on a foreign node — its
// signature is deliberately left unchanged (see ErrForeignNodeType's doc
// comment) — but the panic now carries the same diagnostic Validate would
// report, instead of Go's opaque "interface conversion" message from the leaf
// ValidationGet's own bare assertion. This is an improvement to the residue,
// not a fix: a direct caller still panics, it is just told why.
func TestValidationStrategyForDiagnosticOnForeignNode(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		run  func(t *testing.T)
	}

	cases := []testCase{
		{
			name: "the real leaf type under its own kind does not panic",
			run: func(t *testing.T) {
				assert.NotPanics(t, func() {
					model.ValidationStrategyFor(activity.NewUserTask("task"))
				})
			},
		},
		{
			name: "a foreign node under a kind with ValidationGet panics naming ErrForeignNodeType",
			run: func(t *testing.T) {
				defer func() {
					r := recover()
					require.NotNil(t, r, "expected ValidationStrategyFor to panic on a foreign node")
					err, ok := r.(error)
					require.True(t, ok, "panic value must be an error, got %T", r)
					assert.ErrorIs(t, err, model.ErrForeignNodeType)
					assert.Contains(t, err.Error(), `"task"`, "the node id locates the offender")
					assert.Contains(t, err.Error(), "activity.UserTask", "the registered type is named")
					assert.Contains(t, err.Error(), "foreignUserTask", "the actual type is named")
				}()

				model.ValidationStrategyFor(foreignUserTask{Base: model.NewBase("task", "task")})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.run(t)
		})
	}
}
