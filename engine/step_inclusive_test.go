package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// inclusiveForkDef: start -> or -{a>0}-> ta ; -{b>0}-> tb ; -default-> tc ; each -> its end
func inclusiveForkDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "or", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "or", Kind: model.KindInclusiveGateway},
			{ID: "ta", Kind: model.KindServiceTask, Action: "a"},
			{ID: "tb", Kind: model.KindServiceTask, Action: "b"},
			{ID: "tc", Kind: model.KindServiceTask, Action: "c"},
			{ID: "ea", Kind: model.KindEndEvent},
			{ID: "eb", Kind: model.KindEndEvent},
			{ID: "ec", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "or"},
			{ID: "f2", Source: "or", Target: "ta", Condition: "a > 0"},
			{ID: "f3", Source: "or", Target: "tb", Condition: "b > 0"},
			{ID: "f4", Source: "or", Target: "tc", IsDefault: true},
			{ID: "f5", Source: "ta", Target: "ea"},
			{ID: "f6", Source: "tb", Target: "eb"},
			{ID: "f7", Source: "tc", Target: "ec"},
		},
	}
}

func actionNames(cmds []engine.Command) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok {
			out = append(out, ia.Name)
		}
	}
	return out
}

func TestInclusiveForkTakesAllTrueBranches(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	res, err := engine.Step(inclusiveForkDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"a": 1, "b": 1}), engine.StepOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b"}, actionNames(res.Commands))
	require.Len(t, res.State.Tokens, 2)
}

func TestInclusiveForkSingleTrueBranch(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	res, err := engine.Step(inclusiveForkDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"a": 1, "b": 0}), engine.StepOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a"}, actionNames(res.Commands))
}

func TestInclusiveForkFallsBackToDefault(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	res, err := engine.Step(inclusiveForkDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"a": 0, "b": 0}), engine.StepOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"c"}, actionNames(res.Commands))
}

func TestInclusiveForkNoMatchNoDefaultErrors(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := &model.ProcessDefinition{
		ID: "or", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "or", Kind: model.KindInclusiveGateway},
			{ID: "ta", Kind: model.KindServiceTask, Action: "a"},
			{ID: "ea", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "or"},
			{ID: "f2", Source: "or", Target: "ta", Condition: "a > 0"},
			{ID: "f3", Source: "ta", Target: "ea"},
		},
	}
	_, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"a": 0}), engine.StepOptions{})
	require.ErrorIs(t, err, engine.ErrNoMatchingFlow)
}
