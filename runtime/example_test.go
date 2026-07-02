package runtime_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
)

func linearDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "greeting", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewServiceTask("greet", model.WithActionName("greet")),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "greet"},
			{ID: "f2", Source: "greet", Target: "end"},
		},
	}
}

func TestRunnerExecutesParallelDiamond(t *testing.T) {
	def := &model.ProcessDefinition{
		ID: "diamond", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewParallelGateway("fork"),
			model.NewServiceTask("a", model.WithActionName("a")),
			model.NewServiceTask("b", model.WithActionName("b")),
			model.NewParallelGateway("join"),
			model.NewEndEvent("end"),
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
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"a": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"a": true}, nil
		}),
		"b": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"b": true}, nil
		}),
	})
	r := mustRunner(t, cat, mustMemStore(t))

	final, err := r.Run(t.Context(), def, "i1", nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status)
	assert.Empty(t, final.Tokens)
	assert.Equal(t, true, final.Variables["a"])
	assert.Equal(t, true, final.Variables["b"])

	// Every NodeVisit opened during the fork/join must be properly closed: no
	// visit should have a nil LeftAt (which would indicate a dangling open visit).
	for _, v := range final.History {
		assert.NotNilf(t, v.LeftAt, "NodeVisit for node %q (token %q) was never closed", v.NodeID, v.TokenID)
	}
}

func TestRunnerExecutesInclusiveTwoOfThree(t *testing.T) {
	def := &model.ProcessDefinition{
		ID: "ord", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewInclusiveGateway("orsplit"),
			model.NewServiceTask("ta", model.WithActionName("a")),
			model.NewServiceTask("tb", model.WithActionName("b")),
			model.NewServiceTask("tc", model.WithActionName("c")),
			model.NewInclusiveGateway("orjoin"),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "orsplit"},
			{ID: "f2", Source: "orsplit", Target: "ta", Condition: "a > 0"},
			{ID: "f3", Source: "orsplit", Target: "tb", Condition: "b > 0"},
			{ID: "f4", Source: "orsplit", Target: "tc", Condition: "c > 0"},
			{ID: "f5", Source: "ta", Target: "orjoin"},
			{ID: "f6", Source: "tb", Target: "orjoin"},
			{ID: "f7", Source: "tc", Target: "orjoin"},
			{ID: "f8", Source: "orjoin", Target: "end"},
		},
	}
	mk := func(key string) action.ServiceAction {
		return action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{key: true}, nil
		})
	}
	cat := action.NewMapCatalog(map[string]action.ServiceAction{"a": mk("ra"), "b": mk("rb"), "c": mk("rc")})
	r := mustRunner(t, cat, mustMemStore(t))

	final, err := r.Run(t.Context(), def, "i1", map[string]any{"a": 1, "b": 1, "c": 0})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status)
	assert.Empty(t, final.Tokens)
	assert.Equal(t, true, final.Variables["ra"])
	assert.Equal(t, true, final.Variables["rb"])
	_, ranC := final.Variables["rc"]
	assert.False(t, ranC, "branch c must not run when its condition is false")

	// Every NodeVisit opened during the fork/join must be properly closed: no
	// visit should have a nil LeftAt (which would indicate a dangling open visit).
	for _, v := range final.History {
		assert.NotNilf(t, v.LeftAt, "NodeVisit for node %q (token %q) was never closed", v.NodeID, v.TokenID)
	}
}

func TestRunnerExecutesLinearProcess(t *testing.T) {
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"greet": action.Func(func(_ context.Context, in map[string]any) (map[string]any, error) {
			return map[string]any{"greeting": "hi " + in["name"].(string)}, nil
		}),
	})
	store := mustMemStore(t)
	r := mustRunner(t, cat, store)

	final, err := r.Run(t.Context(), linearDef(), "i1", map[string]any{"name": "Ada"})
	require.NoError(t, err)

	assert.Equal(t, engine.StatusCompleted, final.Status)
	assert.Equal(t, "hi Ada", final.Variables["greeting"])
	assert.Empty(t, final.Tokens)

	// Journal recorded StartInstance + ActionCompleted (audit trail).
	entries, err := store.Entries(t.Context(), "i1")
	require.NoError(t, err)
	assert.Len(t, entries, 2)
}
