package kinds_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	_ "github.com/kartaladev/wrkflw/definition/kinds"
	"github.com/kartaladev/wrkflw/definition/model"
)

// TestMarshalJSONRejectsForeignNodeType covers the fourth ingress named in #147:
// ProcessDefinition.MarshalJSON reaches ValidationStrategyFor, inside its node
// loop, with no Validate in front of it, so a foreign node — one satisfying
// model.Node and claiming a registered kind without being that kind's concrete
// type — panicked instead of being refused. The fix runs the same
// checkNodeTypes gate Validate already uses, at the top of MarshalJSON, before
// any node is touched. (A prior version of this comment cited a specific line
// number for the ValidationStrategyFor call site; it went stale the moment
// this file's own comments grew past it — described structurally instead.)
//
// foreignUserTask, linearDefWith and wrapInSubProcess are shared with
// foreign_node_test.go's TestValidateRejectsForeignNodeType in this package.
func TestMarshalJSONRejectsForeignNodeType(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, data []byte, err error)
	}

	cases := []testCase{
		{
			name: "a foreign node at top level is refused, not panicked",
			def:  linearDefWith(foreignUserTask{Base: model.NewBase("task", "task")}),
			assert: func(t *testing.T, data []byte, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, model.ErrForeignNodeType)
				assert.Nil(t, data)
			},
		},
		{
			name: "a foreign node one subprocess level down is refused",
			def: wrapInSubProcess("outer", linearDefWith(
				foreignUserTask{Base: model.NewBase("task", "task")},
			)),
			assert: func(t *testing.T, data []byte, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, model.ErrForeignNodeType)
				assert.Nil(t, data)
			},
		},
		{
			name: "a foreign node two subprocess levels down is refused (the recursion proof)",
			def: wrapInSubProcess("outer", wrapInSubProcess("mid", linearDefWith(
				foreignUserTask{Base: model.NewBase("task", "task")},
			))),
			assert: func(t *testing.T, data []byte, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, model.ErrForeignNodeType)
				assert.Nil(t, data)
			},
		},
		{
			// F2: the three cases above all use foreignUserTask, the most-
			// asserting kind in the repo (it has a ValidationGet AND an
			// asserting ToWire), so they cannot distinguish "the gate ran" from
			// "the assertion happened to fire on its own". exclusiveGateway has
			// neither: its ToWire is a no-op and it has no ValidationGet (see
			// definition/gateway/gateway.go), so before this gate a foreign node
			// under it marshalled CLEANLY — success, not panic. This row is the
			// one that proves checkNodeTypes itself catches it, not a
			// pre-existing assertion.
			name: "a foreign node under a NON-ASSERTING kind is refused too (was success, not panic, before this gate)",
			def:  linearDefWith(foreignNode{Base: model.NewBase("task", "task"), kind: model.KindExclusiveGateway}),
			assert: func(t *testing.T, data []byte, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, model.ErrForeignNodeType)
				assert.Nil(t, data)
			},
		},
		{
			// Discrimination pin (rule 13): a gate that rejects everything would
			// pass the three cases above too. The real leaf type under its own
			// kind, at the same nesting depth as the recursion proof, must still
			// marshal cleanly.
			name: "the real leaf type under its own kind still marshals cleanly, at depth two",
			def: wrapInSubProcess("outer", wrapInSubProcess("mid", linearDefWith(
				activity.NewUserTask("task"),
			))),
			assert: func(t *testing.T, data []byte, err error) {
				require.NoError(t, err)
				assert.NotEmpty(t, data)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tc.def)
			tc.assert(t, data, err)
		})
	}
}
