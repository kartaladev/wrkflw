package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// at is a fixed test timestamp shared by all cases in this file.
var atBR = time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)

func TestServiceTaskAndBusinessRuleTaskEmitInvokeAction(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, ia engine.InvokeAction)
	}

	cases := []testCase{
		{
			name: "service task default-by-id: NodeID and Name both equal node id",
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
				assert.Equal(t, "work", ia.NodeID, "NodeID must equal node id")
				assert.Equal(t, "work", ia.Name, "Name must equal node id (default-by-id)")
			},
		},
		{
			name: "service task explicit name: NodeID is node id, Name is explicit",
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
				assert.Equal(t, "work", ia.NodeID, "NodeID must equal node id")
				assert.Equal(t, "pay", ia.Name, "Name must equal explicit action name")
			},
		},
		{
			name: "business rule task explicit name: emits InvokeAction with NodeID and Name",
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
				assert.Equal(t, "rule", ia.NodeID, "NodeID must equal node id")
				assert.Equal(t, "decide", ia.Name, "Name must equal explicit action name")
			},
		},
		{
			name: "business rule task default-by-id: NodeID and Name both equal node id",
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
				assert.Equal(t, "brule", ia.NodeID, "NodeID must equal node id")
				assert.Equal(t, "brule", ia.Name, "Name must equal node id (default-by-id)")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res, err := engine.Step(tc.def, engine.InstanceState{InstanceID: "i1"},
				engine.NewStartInstance(atBR, nil), engine.StepOptions{})
			require.NoError(t, err)

			ia := firstInvokeAction(t, res.Commands)
			tc.assert(t, ia)
		})
	}
}
