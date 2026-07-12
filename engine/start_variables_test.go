package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// TestStartInstance_CapturesStartVariables verifies that InstanceState.StartVariables
// is populated as an independent snapshot of the variables the instance began with,
// captured once on StartInstance. A later full ReverseInstance uses this snapshot to
// restore a fresh slate when resuming at the start node.
func TestStartInstance_CapturesStartVariables(t *testing.T) {
	def := &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{event.NewStart("start"), event.NewEnd("end")},
		Flows: []flow.SequenceFlow{{ID: "f1", Source: "start", Target: "end"}},
	}
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	r, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, map[string]any{"amount": 100}), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"amount": 100}, r.State.StartVariables)
	// Mutating live Variables must not change the snapshot.
	r.State.Variables["amount"] = 999
	assert.Equal(t, 100, r.State.StartVariables["amount"], "StartVariables must be an independent copy")
}
