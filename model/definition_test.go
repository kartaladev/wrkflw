package model_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/model"
)

func linearDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "p1",
		Version: 1,
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

func TestProcessDefinitionLookups(t *testing.T) {
	d := linearDef()

	n, ok := d.Node("greet")
	require.True(t, ok)
	assert.Equal(t, model.KindServiceTask, n.Kind)

	_, ok = d.Node("missing")
	assert.False(t, ok)

	out := d.Outgoing("start")
	require.Len(t, out, 1)
	assert.Equal(t, "greet", out[0].Target)

	in := d.Incoming("end")
	require.Len(t, in, 1)
	assert.Equal(t, "greet", in[0].Source)

	starts := d.StartNodes()
	require.Len(t, starts, 1)
	assert.Equal(t, "start", starts[0].ID)
}

func TestNodeUserTaskFields(t *testing.T) {
	d := &model.ProcessDefinition{
		ID:      "p2",
		Version: 1,
		Nodes: []model.Node{
			{
				ID:              "approve",
				Kind:            model.KindUserTask,
				Name:            "Approve Request",
				CandidateRoles:  []string{"manager", "admin"},
				EligibilityExpr: "amount > 1000",
			},
		},
		Flows: []model.SequenceFlow{},
	}

	n, ok := d.Node("approve")
	require.True(t, ok)
	assert.Equal(t, model.KindUserTask, n.Kind)
	assert.Equal(t, "Approve Request", n.Name)
	assert.Equal(t, []string{"manager", "admin"}, n.CandidateRoles)
	assert.Equal(t, "amount > 1000", n.EligibilityExpr)
}

// TestNodeEventBoundaryFields asserts that the five new event/boundary fields
// on model.Node round-trip through ProcessDefinition.Node correctly.
func TestNodeEventBoundaryFields(t *testing.T) {
	cases := []struct {
		name string
		node model.Node
	}{
		{
			name: "signal-catch",
			node: model.Node{
				ID:         "sig-catch",
				Kind:       model.KindIntermediateCatchEvent,
				SignalName: "order.placed",
			},
		},
		{
			name: "message-catch",
			node: model.Node{
				ID:             "msg-catch",
				Kind:           model.KindIntermediateCatchEvent,
				MessageName:    "payment.received",
				CorrelationKey: "order.id",
			},
		},
		{
			name: "signal-throw",
			node: model.Node{
				ID:         "sig-throw",
				Kind:       model.KindIntermediateThrowEvent,
				SignalName: "order.shipped",
			},
		},
		{
			// Zero-value NonInterrupting (false) = interrupting — the BPMN default.
			name: "boundary-interrupting-default",
			node: model.Node{
				ID:              "boundary-1",
				Kind:            model.KindBoundaryEvent,
				SignalName:      "cancel.signal",
				AttachedTo:      "task-1",
				NonInterrupting: false,
			},
		},
		{
			// NonInterrupting: true = non-interrupting boundary event.
			name: "boundary-non-interrupting",
			node: model.Node{
				ID:              "boundary-2",
				Kind:            model.KindBoundaryEvent,
				MessageName:     "reminder.msg",
				AttachedTo:      "task-2",
				NonInterrupting: true,
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			d := &model.ProcessDefinition{
				ID:      "p-event",
				Version: 1,
				Nodes:   []model.Node{tc.node},
				Flows:   []model.SequenceFlow{},
			}
			n, ok := d.Node(tc.node.ID)
			require.True(t, ok)
			assert.Equal(t, tc.node.SignalName, n.SignalName)
			assert.Equal(t, tc.node.MessageName, n.MessageName)
			assert.Equal(t, tc.node.CorrelationKey, n.CorrelationKey)
			assert.Equal(t, tc.node.AttachedTo, n.AttachedTo)
			assert.Equal(t, tc.node.NonInterrupting, n.NonInterrupting)
		})
	}
}

// TestNodeTimerSLAReminderFields asserts that the six new timer/SLA/reminder
// fields on model.Node round-trip through ProcessDefinition.Node correctly.
func TestNodeTimerSLAReminderFields(t *testing.T) {
	cases := []struct {
		name string
		node model.Node
	}{
		{
			name: "timer-intermediate",
			node: model.Node{
				ID:            "wait-1h",
				Kind:          model.KindIntermediateCatchEvent,
				Name:          "Wait 1 hour",
				TimerDuration: "PT1H",
			},
		},
		{
			name: "sla-with-flow-and-action",
			node: model.Node{
				ID:          "review",
				Kind:        model.KindUserTask,
				Name:        "Review",
				SLADuration: "P1D",
				SLAFlow:     "sla-breach-flow",
				SLAAction:   "notify-manager",
			},
		},
		{
			name: "reminder-every-with-action",
			node: model.Node{
				ID:             "approve",
				Kind:           model.KindUserTask,
				Name:           "Approve",
				ReminderEvery:  "PT4H",
				ReminderAction: "send-reminder",
			},
		},
		{
			name: "all-six-fields",
			node: model.Node{
				ID:             "task-full",
				Kind:           model.KindUserTask,
				Name:           "Full Task",
				TimerDuration:  "PT30M",
				SLADuration:    "P2D",
				SLAFlow:        "escalate",
				SLAAction:      "escalate-action",
				ReminderEvery:  "PT6H",
				ReminderAction: "remind-action",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			d := &model.ProcessDefinition{
				ID:      "p-timer",
				Version: 1,
				Nodes:   []model.Node{tc.node},
				Flows:   []model.SequenceFlow{},
			}
			n, ok := d.Node(tc.node.ID)
			require.True(t, ok)
			assert.Equal(t, tc.node.TimerDuration, n.TimerDuration)
			assert.Equal(t, tc.node.SLADuration, n.SLADuration)
			assert.Equal(t, tc.node.SLAFlow, n.SLAFlow)
			assert.Equal(t, tc.node.SLAAction, n.SLAAction)
			assert.Equal(t, tc.node.ReminderEvery, n.ReminderEvery)
			assert.Equal(t, tc.node.ReminderAction, n.ReminderAction)
		})
	}
}
