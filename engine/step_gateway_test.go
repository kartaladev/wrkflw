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
	require.Len(t, res.State.Tokens, 1)
	assert.Equal(t, "small", res.State.Tokens[0].NodeID)
}

// parallelForkDef: start -> fork => a, b (service tasks) -> end (each)
func parallelForkDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "par", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "fork", Kind: model.KindParallelGateway},
			{ID: "a", Kind: model.KindServiceTask, Action: "a"},
			{ID: "b", Kind: model.KindServiceTask, Action: "b"},
			{ID: "enda", Kind: model.KindEndEvent},
			{ID: "endb", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fork"},
			{ID: "f2", Source: "fork", Target: "a"},
			{ID: "f3", Source: "fork", Target: "b"},
			{ID: "f4", Source: "a", Target: "enda"},
			{ID: "f5", Source: "b", Target: "endb"},
		},
	}
}

func TestParallelGatewayForksAllBranches(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	res, err := engine.Step(parallelForkDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Both branches fire their service action in one macro step.
	require.Len(t, res.Commands, 2)
	names := []string{
		res.Commands[0].(engine.InvokeAction).Name,
		res.Commands[1].(engine.InvokeAction).Name,
	}
	assert.ElementsMatch(t, []string{"a", "b"}, names)

	// Two tokens, one parked on each service task; the fork token is gone.
	require.Len(t, res.State.Tokens, 2)
	nodes := []string{res.State.Tokens[0].NodeID, res.State.Tokens[1].NodeID}
	assert.ElementsMatch(t, []string{"a", "b"}, nodes)
	for _, tk := range res.State.Tokens {
		assert.Equal(t, engine.TokenWaitingCommand, tk.State)
	}
}

// diamondDef: start -> fork => a,b -> join -> end. Join waits for both a and b.
func diamondDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "diamond", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "fork", Kind: model.KindParallelGateway},
			{ID: "a", Kind: model.KindServiceTask, Action: "a"},
			{ID: "b", Kind: model.KindServiceTask, Action: "b"},
			{ID: "join", Kind: model.KindParallelGateway},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fork"},
			{ID: "f2", Source: "fork", Target: "a"},
			{ID: "f3", Source: "fork", Target: "b"},
			{ID: "f4", Source: "a", Target: "join"},
			{ID: "f5", Source: "b", Target: "join"},
			{ID: "f6", Source: "join", Target: "end"},
		},
	}
}

func TestParallelJoinWaitsForAllBranches(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := diamondDef()

	r0, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r0.Commands, 2) // a and b invoked
	cmdA := r0.Commands[0].(engine.InvokeAction)
	cmdB := r0.Commands[1].(engine.InvokeAction)

	// Complete the first branch: token parks at the join, instance not done.
	r1, err := engine.Step(def, r0.State,
		engine.NewActionCompleted(at.Add(time.Second), cmdA.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Empty(t, r1.Commands)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)
	require.Len(t, r1.State.Tokens, 2)

	// Complete the second branch: join fires, reaches end, instance completes.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(2*time.Second), cmdB.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.Commands, 1)
	_, ok := r2.Commands[0].(engine.CompleteInstance)
	require.True(t, ok)
	assert.Equal(t, engine.StatusCompleted, r2.State.Status)
	assert.Empty(t, r2.State.Tokens)
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
