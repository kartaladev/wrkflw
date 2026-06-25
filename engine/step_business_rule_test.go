package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
)

func TestServiceTaskAndBusinessRuleTaskEmitInvokeAction(t *testing.T) {
	t.Parallel()

	inlineAction := action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
		return nil, nil
	})

	type testCase struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, ia engine.InvokeAction)
	}

	cases := []testCase{
		{
			name: "service task default-by-id: Name equals node id, no inline",
			def: &model.ProcessDefinition{
				ID: "p-svc-default", Version: 1,
				Nodes: []model.Node{
					model.NewStartEvent("start"),
					model.NewServiceTask("work"), // no WithActionName → default-by-id
					model.NewEndEvent("end"),
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "work"},
					{ID: "f2", Source: "work", Target: "end"},
				},
			},
			assert: func(t *testing.T, ia engine.InvokeAction) {
				assert.Equal(t, "work", ia.Name, "Name must equal node id (default-by-id)")
				assert.Nil(t, ia.Inline, "no inline action declared → Inline must be nil")
			},
		},
		{
			name: "service task explicit name: Name is explicit, no inline",
			def: &model.ProcessDefinition{
				ID: "p-svc-explicit", Version: 1,
				Nodes: []model.Node{
					model.NewStartEvent("start"),
					model.NewServiceTask("work", model.WithActionName("pay")),
					model.NewEndEvent("end"),
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "work"},
					{ID: "f2", Source: "work", Target: "end"},
				},
			},
			assert: func(t *testing.T, ia engine.InvokeAction) {
				assert.Equal(t, "pay", ia.Name, "Name must equal explicit action name")
				assert.Nil(t, ia.Inline, "named action → Inline must be nil")
			},
		},
		{
			name: "service task inline: carries the inline action on the command",
			def: &model.ProcessDefinition{
				ID: "p-svc-inline", Version: 1,
				Nodes: []model.Node{
					model.NewStartEvent("start"),
					model.NewServiceTask("work", model.WithAction(inlineAction)),
					model.NewEndEvent("end"),
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "work"},
					{ID: "f2", Source: "work", Target: "end"},
				},
			},
			assert: func(t *testing.T, ia engine.InvokeAction) {
				assert.Equal(t, "work", ia.Name, "Name still defaults to node id")
				assert.NotNil(t, ia.Inline, "inline node → Inline must carry the inline action")
			},
		},
		{
			name: "business rule task explicit name: Name is explicit, no inline",
			def: &model.ProcessDefinition{
				ID: "p-br-explicit", Version: 1,
				Nodes: []model.Node{
					model.NewStartEvent("start"),
					model.NewBusinessRuleTask("rule", model.WithActionName("decide")),
					model.NewEndEvent("end"),
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "rule"},
					{ID: "f2", Source: "rule", Target: "end"},
				},
			},
			assert: func(t *testing.T, ia engine.InvokeAction) {
				assert.Equal(t, "decide", ia.Name, "Name must equal explicit action name")
				assert.Nil(t, ia.Inline, "named action → Inline must be nil")
			},
		},
		{
			name: "business rule task default-by-id: Name equals node id, no inline",
			def: &model.ProcessDefinition{
				ID: "p-br-default", Version: 1,
				Nodes: []model.Node{
					model.NewStartEvent("start"),
					model.NewBusinessRuleTask("brule"), // no WithActionName → default-by-id
					model.NewEndEvent("end"),
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "brule"},
					{ID: "f2", Source: "brule", Target: "end"},
				},
			},
			assert: func(t *testing.T, ia engine.InvokeAction) {
				assert.Equal(t, "brule", ia.Name, "Name must equal node id (default-by-id)")
				assert.Nil(t, ia.Inline, "no inline action declared → Inline must be nil")
			},
		},
		{
			name: "business rule task inline: carries the inline action on the command",
			def: &model.ProcessDefinition{
				ID: "p-br-inline", Version: 1,
				Nodes: []model.Node{
					model.NewStartEvent("start"),
					model.NewBusinessRuleTask("brule", model.WithAction(inlineAction)),
					model.NewEndEvent("end"),
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "brule"},
					{ID: "f2", Source: "brule", Target: "end"},
				},
			},
			assert: func(t *testing.T, ia engine.InvokeAction) {
				assert.Equal(t, "brule", ia.Name, "Name still defaults to node id")
				assert.NotNil(t, ia.Inline, "inline node → Inline must carry the inline action")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			at := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
			res, err := engine.Step(tc.def, engine.InstanceState{InstanceID: "i1"},
				engine.NewStartInstance(at, nil), engine.StepOptions{})
			require.NoError(t, err)

			ia := firstInvokeAction(t, res.Commands)
			tc.assert(t, ia)
		})
	}
}
