package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// exclusiveDef: start -> xor -{amount > 100}-> big ; -default-> small ; both -> end
func exclusiveDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "xor", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "xor", Kind: model.KindExclusiveGateway},
			{ID: "big", Kind: model.KindServiceTask, Action: "big"},
			{ID: "small", Kind: model.KindServiceTask, Action: "small"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "xor"},
			{ID: "f2", Source: "xor", Target: "big", Condition: "amount > 100"},
			{ID: "f3", Source: "xor", Target: "small", IsDefault: true},
			{ID: "f4", Source: "big", Target: "end"},
			{ID: "f5", Source: "small", Target: "end"},
		},
	}
}

func TestExclusiveGatewayTakesConditionalBranch(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	res, err := engine.Step(exclusiveDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"amount": 150}), engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, res.Commands, 1)
	ia := res.Commands[0].(engine.InvokeAction)
	assert.Equal(t, "big", ia.Name)
	require.Len(t, res.State.Tokens, 1)
	assert.Equal(t, "big", res.State.Tokens[0].NodeID)
}

func TestExclusiveGatewayTakesDefaultBranch(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	res, err := engine.Step(exclusiveDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"amount": 5}), engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, res.Commands, 1)
	assert.Equal(t, "small", res.Commands[0].(engine.InvokeAction).Name)
}

func TestExclusiveGatewayNoMatchNoDefaultErrors(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := &model.ProcessDefinition{
		ID: "xor", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "xor", Kind: model.KindExclusiveGateway},
			{ID: "big", Kind: model.KindServiceTask, Action: "big"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "xor"},
			{ID: "f2", Source: "xor", Target: "big", Condition: "amount > 100"},
			{ID: "f3", Source: "big", Target: "end"},
		},
	}
	_, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"amount": 5}), engine.StepOptions{})
	require.ErrorIs(t, err, engine.ErrNoMatchingFlow)
}
