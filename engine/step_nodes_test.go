package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
)

// armBearingKinds is the complete set of node kinds that have a drive() strategy.
// Keep in sync with nodeStrategies in step_nodes.go.
var armBearingKinds = []model.NodeKind{
	model.KindStartEvent,
	model.KindEndEvent,
	model.KindUserTask,
	model.KindIntermediateCatchEvent,
	model.KindSubProcess,
	model.KindExclusiveGateway,
	model.KindParallelGateway,
	model.KindInclusiveGateway,
	model.KindEventBasedGateway,
	model.KindCallActivity,
	model.KindIntermediateThrowEvent,
	model.KindCompensationThrowEvent,
	model.KindServiceTask,
	model.KindBusinessRuleTask,
	model.KindReceiveTask,
	model.KindSendTask,
}

// intentionallyUnhandledKinds is the set of node kinds that must NOT have a
// drive() strategy — they fall through to the default park logic in drive().
var intentionallyUnhandledKinds = []model.NodeKind{
	model.KindBoundaryEvent,
	model.KindUnspecified,
}

// TestNodeStrategyRegistry asserts that nodeStrategies covers exactly the
// 16 arm-bearing kinds and does NOT include the 2 intentionally-unhandled kinds.
func TestNodeStrategyRegistry(t *testing.T) {
	t.Run("all arm-bearing kinds are registered", func(t *testing.T) {
		for _, k := range armBearingKinds {
			if _, ok := nodeStrategies[k]; !ok {
				t.Errorf("no nodeStrategy registered for %v", k)
			}
		}
	})

	t.Run("registry size matches arm-bearing set", func(t *testing.T) {
		if got, want := len(nodeStrategies), len(armBearingKinds); got != want {
			t.Errorf("nodeStrategies has %d entries; want %d", got, want)
		}
	})

	t.Run("intentionally-unhandled kinds are NOT registered", func(t *testing.T) {
		for _, k := range intentionallyUnhandledKinds {
			if _, ok := nodeStrategies[k]; ok {
				t.Errorf("nodeStrategy unexpectedly registered for unhandled kind %v", k)
			}
		}
	})

	// businessRuleTask shares serviceTask's strategy rather than owning a
	// character-identical copy of it: until the rule-engine adapter ships the two
	// kinds behave identically, and #27's decision was to collapse the duplicate to
	// one dispatch-table line.
	//
	// This pins a DELIBERATELY TEMPORARY state. The adapter is expected to give
	// businessRuleTask a distinct strategy again for its rule branch, and to delete
	// this subtest when it does — the row is here to stop the duplicate creeping
	// back before then, not to forbid the adapter. The durable invariant, that both
	// kinds emit the same InvokeAction, lives in
	// TestServiceTaskAndBusinessRuleTaskEmitInvokeAction and is unaffected.
	//
	// The assertion is identity against serviceTaskStrategy{}, not merely that the
	// two entries are == each other: a third duplicated zero-size type used for
	// both would satisfy "equal to each other" while restoring exactly the drift
	// the collapse removed. The userTask row is the discriminating negative — a
	// check reporting every kind as serviceTaskStrategy would satisfy the two
	// positive rows just as well.
	t.Run("businessRuleTask shares the serviceTask strategy", func(t *testing.T) {
		cases := []struct {
			name   string
			kind   model.NodeKind
			assert func(t *testing.T, strat nodeStrategy)
		}{
			{
				name:   "serviceTask",
				kind:   model.KindServiceTask,
				assert: func(t *testing.T, strat nodeStrategy) { assert.Equal(t, serviceTaskStrategy{}, strat) },
			},
			{
				name:   "businessRuleTask",
				kind:   model.KindBusinessRuleTask,
				assert: func(t *testing.T, strat nodeStrategy) { assert.Equal(t, serviceTaskStrategy{}, strat) },
			},
			{
				name:   "userTask does not",
				kind:   model.KindUserTask,
				assert: func(t *testing.T, strat nodeStrategy) { assert.NotEqual(t, serviceTaskStrategy{}, strat) },
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				strat, ok := nodeStrategies[tc.kind]
				require.True(t, ok, "kind %v has no dispatch-table entry", tc.kind)
				tc.assert(t, strat)
			})
		}
	})
}
