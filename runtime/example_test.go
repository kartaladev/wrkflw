package runtime_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func linearDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "greeting", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "greet", Kind: model.KindServiceTask, Action: "greet"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "greet"},
			{ID: "f2", Source: "greet", Target: "end"},
		},
	}
}

func TestRunnerExecutesLinearProcess(t *testing.T) {
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"greet": action.Func(func(_ context.Context, in map[string]any) (map[string]any, error) {
			return map[string]any{"greeting": "hi " + in["name"].(string)}, nil
		}),
	})
	jnl := runtime.NewMemJournal()
	r := runtime.NewRunner(cat, clock.System(), runtime.NewMemStateStore(), jnl, runtime.NewMemOutbox())

	final, err := r.Run(t.Context(), linearDef(), "i1", map[string]any{"name": "Ada"})
	require.NoError(t, err)

	assert.Equal(t, engine.StatusCompleted, final.Status)
	assert.Equal(t, "hi Ada", final.Variables["greeting"])
	assert.Empty(t, final.Tokens)

	// Journal recorded StartInstance + ActionCompleted (audit trail).
	assert.Len(t, jnl.Entries("i1"), 2)
}
