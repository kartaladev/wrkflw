package engine

import (
	"testing"

	"github.com/zakyalvan/krtlwrkflw/model"
)

// TestNodeStrategyRegistry asserts that the nodeStrategies map contains the
// expected node kinds. Adjust the set as kinds migrate (Task 3 adds all 13).
func TestNodeStrategyRegistry(t *testing.T) {
	// migrated-so-far set (Task 2: ServiceTask only)
	// TODO: tighten to full 13-kind set in Task 3
	want := []model.NodeKind{
		model.KindServiceTask,
	}
	for _, k := range want {
		if _, ok := nodeStrategies[k]; !ok {
			t.Errorf("no nodeStrategy registered for %v", k)
		}
	}
}
