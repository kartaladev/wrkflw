package definition_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
)

// counterfeitNode satisfies model.Node by embedding model.Base — which no seal
// can prevent, since the genuine leaf types do the same — and reports whatever
// kind it is told to.
type counterfeitNode struct {
	model.Base

	kind model.NodeKind
}

func (c counterfeitNode) Kind() model.NodeKind { return c.kind }

func counterfeit(id string, kind model.NodeKind) model.Node {
	return counterfeitNode{Base: model.NewBase(id, id), kind: kind}
}

// TestForeignNodeRejectedAtEveryGatedIngress covers both gated entry points at
// once, because the defect this pins was that they did not agree.
//
// model.Validate and the Builder reach a node through different bare
// assertions — Validate through the ToWire specs, the Builder through
// ValidationGet during strategy reconciliation, which runs BEFORE it validates.
// Gating only one leaves the other panicking, so every case below is asserted
// against both.
//
// The four kinds declaring a ValidationGet (userTask, receiveTask, startEvent,
// intermediateCatchEvent) are enumerated explicitly: they are the only kinds
// whose counterfeit can reach the Builder's assertion, so a regression there
// would be invisible if the table only tried one kind.
func TestForeignNodeRejectedAtEveryGatedIngress(t *testing.T) {
	t.Parallel()

	nested := func(inner []model.Node) []model.Node {
		return []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sp", &model.ProcessDefinition{
				ID: "inner", Version: 1,
				Nodes: inner,
				Flows: []flow.SequenceFlow{{ID: "nf", Source: inner[0].ID(), Target: "inner-end"}},
			}),
			event.NewEnd("end"),
		}
	}

	type testCase struct {
		name  string
		nodes []model.Node
	}

	cases := []testCase{
		{
			name:  "top level",
			nodes: []model.Node{counterfeit("task", model.KindUserTask), event.NewEnd("end")},
		},
		{
			name: "nested under KindStartEvent",
			nodes: nested([]model.Node{
				counterfeit("inner-start", model.KindStartEvent),
				event.NewEnd("inner-end"),
			}),
		},
		{
			name: "depth two",
			nodes: []model.Node{
				event.NewStart("start"),
				activity.NewSubProcess("sp", &model.ProcessDefinition{
					ID: "mid", Version: 1,
					Nodes: nested([]model.Node{
						counterfeit("inner-start", model.KindStartEvent),
						event.NewEnd("inner-end"),
					}),
				}),
				event.NewEnd("end"),
			},
		},
	}

	// The four kinds that declare a ValidationGet — the Builder's reconciliation
	// path dispatches that spec, so these are the kinds that reach its assertion.
	for _, k := range []model.NodeKind{
		model.KindUserTask,
		model.KindReceiveTask,
		model.KindStartEvent,
		model.KindIntermediateCatchEvent,
	} {
		cases = append(cases, testCase{
			name:  "ValidationGet kind " + k.String(),
			nodes: []model.Node{counterfeit("task", k), event.NewEnd("end")},
		})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			t.Run("model.Validate", func(t *testing.T) {
				t.Parallel()

				err := model.Validate(&model.ProcessDefinition{
					ID: "p", Version: 1, Nodes: tc.nodes,
				})
				require.ErrorIs(t, err, model.ErrForeignNodeType)
			})

			t.Run("Builder.Build", func(t *testing.T) {
				t.Parallel()

				b := definition.NewBuilder("p", 1)
				for _, n := range tc.nodes {
					b.Add(n)
				}
				_, err := b.Build()
				require.ErrorIs(t, err, model.ErrForeignNodeType)
			})
		})
	}
}
