package kinds_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	_ "github.com/kartaladev/wrkflw/definition/kinds"
	"github.com/kartaladev/wrkflw/definition/model"
)

// foreignUserTask is a consumer-shaped counterfeit: it satisfies model.Node by
// embedding model.Base — which no seal can prevent, since Base is exported and
// the leaf types embed it too — and claims KindUserTask. Every bare assertion in
// the leaf ToWire specs and in engine/step_nodes.go assumes a node reporting
// KindUserTask *is* an activity.UserTask, so this type is exactly the hazard
// model.ErrForeignNodeType exists to stop.
type foreignUserTask struct {
	model.Base
}

func (foreignUserTask) Kind() model.NodeKind { return model.KindUserTask }

// unregisteredKindNode reports a kind no leaf package ever registered. It is the
// P3 case: the type gate must stay silent here and leave the diagnosis to
// ErrKindNotRegistered, because a kind with no registered spec has no recorded
// concrete type to compare against. engine.unspecifiedKindNode is a real
// in-repo instance of this shape.
type unregisteredKindNode struct {
	model.Base
}

func (unregisteredKindNode) Kind() model.NodeKind { return model.NodeKind(9997) }

// linearDefWith frames the node under test as the middle of start → task → end,
// with the node id fixed at "task" so the flows resolve. The surrounding
// definition is otherwise well-formed, so any error Validate reports is
// attributable to the node.
func linearDefWith(n model.Node) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			n,
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
}

// TestValidateRejectsForeignNodeType covers the run-time half of sealing Node:
// a concrete type that is not the one its kind registered is refused by
// model.Validate before any bare type assertion can reach it.
func TestValidateRejectsForeignNodeType(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name: "foreign type under a registered kind is rejected",
			def:  linearDefWith(foreignUserTask{Base: model.NewBase("task", "task")}),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrForeignNodeType)
				assert.Contains(t, err.Error(), `"task"`, "error must name the offending node id")
				assert.Contains(t, err.Error(), "activity.UserTask", "error must name the registered type")
				assert.Contains(t, err.Error(), "foreignUserTask", "error must name the actual type")
			},
		},
		{
			name: "foreign type inside a nested subprocess definition is rejected",
			def: &model.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewSubProcess("sp", linearDefWith(
						foreignUserTask{Base: model.NewBase("task", "task")},
					)),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "sp"},
					{ID: "f2", Source: "sp", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrForeignNodeType)
				assert.Contains(t, err.Error(), `subprocess "sp"`, "error must locate the nested definition")
			},
		},
		{
			name: "unregistered kind is left to ErrKindNotRegistered, not the type gate",
			def:  linearDefWith(unregisteredKindNode{Base: model.NewBase("task", "task")}),
			assert: func(t *testing.T, err error) {
				require.NotErrorIs(t, err, model.ErrForeignNodeType)
			},
		},
		{
			name: "the real leaf type under its own kind validates clean",
			def:  linearDefWith(activity.NewUserTask("task")),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, model.Validate(tc.def))
		})
	}
}
