package engine

import (
	"testing"

	"github.com/zakyalvan/krtlwrkflw/definition"
)

// armBearingKinds is the complete set of node kinds that have a drive() strategy.
// Keep in sync with nodeStrategies in step_nodes.go.
var armBearingKinds = []definition.NodeKind{
	definition.KindStartEvent,
	definition.KindEndEvent,
	definition.KindUserTask,
	definition.KindIntermediateCatchEvent,
	definition.KindErrorEndEvent,
	definition.KindSubProcess,
	definition.KindExclusiveGateway,
	definition.KindParallelGateway,
	definition.KindInclusiveGateway,
	definition.KindEventBasedGateway,
	definition.KindCallActivity,
	definition.KindIntermediateThrowEvent,
	definition.KindServiceTask,
	definition.KindBusinessRuleTask,
	definition.KindReceiveTask,
	definition.KindSendTask,
}

// intentionallyUnhandledKinds is the set of node kinds that must NOT have a
// drive() strategy — they fall through to the default park logic in drive().
var intentionallyUnhandledKinds = []definition.NodeKind{
	definition.KindTerminateEndEvent,
	definition.KindBoundaryEvent,
	definition.KindEventSubProcess,
	definition.KindUnspecified,
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
