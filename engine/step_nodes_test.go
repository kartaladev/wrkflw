package engine

import (
	"testing"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

// armBearingKinds is the complete set of node kinds that have a drive() strategy.
// Keep in sync with nodeStrategies in step_nodes.go.
var armBearingKinds = []model.NodeKind{
	model.KindStartEvent,
	model.KindEndEvent,
	model.KindUserTask,
	model.KindIntermediateCatchEvent,
	model.KindErrorEndEvent,
	model.KindSubProcess,
	model.KindExclusiveGateway,
	model.KindParallelGateway,
	model.KindInclusiveGateway,
	model.KindEventBasedGateway,
	model.KindCallActivity,
	model.KindIntermediateThrowEvent,
	model.KindServiceTask,
	model.KindBusinessRuleTask,
	model.KindReceiveTask,
	model.KindSendTask,
}

// intentionallyUnhandledKinds is the set of node kinds that must NOT have a
// drive() strategy — they fall through to the default park logic in drive().
var intentionallyUnhandledKinds = []model.NodeKind{
	model.KindTerminateEndEvent,
	model.KindBoundaryEvent,
	model.KindEventSubProcess,
	model.KindUnspecified,
}

// TestNodeStrategyRegistry asserts that nodeStrategies covers exactly the
// 16 arm-bearing kinds and does NOT include the 4 intentionally-unhandled kinds.
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
}
