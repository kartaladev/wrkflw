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

func TestProcessDefinitionLookups(t *testing.T) {
	d := linearDef()

	n, ok := d.Node("greet")
	require.True(t, ok)
	assert.Equal(t, model.KindServiceTask, n.Kind())

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
	assert.Equal(t, "start", starts[0].ID())
}

func TestNodeUserTaskFields(t *testing.T) {
	d := &model.ProcessDefinition{
		ID:      "p2",
		Version: 1,
		Nodes: []model.Node{
			model.NewUserTask("approve", []string{"manager", "admin"},
				model.WithName("Approve Request"),
				model.WithEligibilityExpr("amount > 1000"),
			),
		},
		Flows: []model.SequenceFlow{},
	}

	n, ok := d.Node("approve")
	require.True(t, ok)
	assert.Equal(t, model.KindUserTask, n.Kind())
	assert.Equal(t, "Approve Request", n.Name())
	ut, ok := n.(model.UserTask)
	require.True(t, ok)
	assert.Equal(t, []string{"manager", "admin"}, ut.CandidateRoles)
	assert.Equal(t, "amount > 1000", ut.EligibilityExpr)
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
			node: model.NewIntermediateCatchEvent("sig-catch", model.WithSignalName("order.placed")),
		},
		{
			name: "message-catch",
			node: model.NewIntermediateCatchEvent("msg-catch", model.WithMessageNameAndKey("payment.received", "order.id")),
		},
		{
			name: "signal-throw",
			node: model.NewIntermediateThrowEvent("sig-throw", model.WithThrowSignal("order.shipped")),
		},
		{
			// Zero-value NonInterrupting (false) = interrupting — the default.
			name: "boundary-interrupting-default",
			node: model.NewBoundaryEvent("boundary-1", "task-1", model.WithBoundarySignal("cancel.signal")),
		},
		{
			// NonInterrupting: true = non-interrupting boundary event.
			name: "boundary-non-interrupting",
			node: model.NewBoundaryEvent("boundary-2", "task-2",
				model.WithBoundaryMessage("reminder.msg", ""),
				model.BoundaryNonInterrupting(),
			),
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
			n, ok := d.Node(tc.node.ID())
			require.True(t, ok)
			switch expected := tc.node.(type) {
			case model.IntermediateCatchEvent:
				got, ok := n.(model.IntermediateCatchEvent)
				require.True(t, ok)
				assert.Equal(t, expected.SignalName, got.SignalName)
				assert.Equal(t, expected.MessageName, got.MessageName)
				assert.Equal(t, expected.CorrelationKey, got.CorrelationKey)
			case model.IntermediateThrowEvent:
				got, ok := n.(model.IntermediateThrowEvent)
				require.True(t, ok)
				assert.Equal(t, expected.SignalName, got.SignalName)
			case model.BoundaryEvent:
				got, ok := n.(model.BoundaryEvent)
				require.True(t, ok)
				assert.Equal(t, expected.SignalName, got.SignalName)
				assert.Equal(t, expected.MessageName, got.MessageName)
				assert.Equal(t, expected.CorrelationKey, got.CorrelationKey)
				assert.Equal(t, expected.AttachedTo, got.AttachedTo)
				assert.Equal(t, expected.NonInterrupting, got.NonInterrupting)
			}
		})
	}
}

// TestNodeSubProcessField asserts that a KindSubProcess node with a nested
// Subprocess *ProcessDefinition round-trips through ProcessDefinition.Node.
func TestNodeSubProcessField(t *testing.T) {
	nested := &model.ProcessDefinition{
		ID:      "nested-proc",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("ns-start"),
			model.NewServiceTask("ns-task", model.WithActionName("inner-action")),
			model.NewEndEvent("ns-end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "nf1", Source: "ns-start", Target: "ns-task"},
			{ID: "nf2", Source: "ns-task", Target: "ns-end"},
		},
	}

	d := &model.ProcessDefinition{
		ID:      "outer",
		Version: 1,
		Nodes:   []model.Node{model.NewSubProcess("subprocess-1", nested, model.WithName("Inner Subprocess"))},
		Flows:   []model.SequenceFlow{},
	}

	n, ok := d.Node("subprocess-1")
	require.True(t, ok)
	assert.Equal(t, model.KindSubProcess, n.Kind())
	assert.Equal(t, "Inner Subprocess", n.Name())
	sp, ok := n.(model.SubProcess)
	require.True(t, ok)
	require.NotNil(t, sp.Subprocess)
	assert.Equal(t, "nested-proc", sp.Subprocess.ID)
	assert.Len(t, sp.Subprocess.Nodes, 3)
	assert.Len(t, sp.Subprocess.Flows, 2)
}

// TestNodeEventSubProcessField asserts that a KindEventSubProcess node with a
// nested Subprocess *ProcessDefinition round-trips correctly.
func TestNodeEventSubProcessField(t *testing.T) {
	nested := &model.ProcessDefinition{
		ID:      "event-nested-proc",
		Version: 1,
		Nodes: []model.Node{
			// The trigger is encoded on the nested StartEvent's SignalName field.
			model.NewStartEvent("es-start", model.WithStartSignal("cancel.signal")),
			model.NewEndEvent("es-end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "ef1", Source: "es-start", Target: "es-end"},
		},
	}

	d := &model.ProcessDefinition{
		ID:      "outer2",
		Version: 1,
		Nodes:   []model.Node{model.NewEventSubProcess("event-sub-1", nested, model.WithName("Cancel Handler"))},
		Flows:   []model.SequenceFlow{},
	}

	n, ok := d.Node("event-sub-1")
	require.True(t, ok)
	assert.Equal(t, model.KindEventSubProcess, n.Kind())
	esp, ok := n.(model.EventSubProcess)
	require.True(t, ok)
	require.NotNil(t, esp.Subprocess)
	assert.Equal(t, "event-nested-proc", esp.Subprocess.ID)
	// The trigger is encoded on the nested start event's SignalName field.
	starts := esp.Subprocess.StartNodes()
	require.Len(t, starts, 1)
	se, ok := starts[0].(model.StartEvent)
	require.True(t, ok)
	assert.Equal(t, "cancel.signal", se.SignalName)
}

// TestNodeCallActivityDefRef asserts that a KindCallActivity node with a DefRef
// field round-trips through ProcessDefinition.Node correctly.
func TestNodeCallActivityDefRef(t *testing.T) {
	d := &model.ProcessDefinition{
		ID:      "outer3",
		Version: 1,
		Nodes:   []model.Node{model.NewCallActivity("call-1", "external-process-v2", model.WithName("Call External Process"))},
		Flows:   []model.SequenceFlow{},
	}

	n, ok := d.Node("call-1")
	require.True(t, ok)
	assert.Equal(t, model.KindCallActivity, n.Kind())
	ca, ok := n.(model.CallActivity)
	require.True(t, ok)
	assert.Equal(t, "external-process-v2", ca.DefRef)
}

// TestNodeTimerSLAReminderFields asserts that the six new timer/SLA/reminder
// fields on model.Node round-trip through ProcessDefinition.Node correctly.
func TestNodeTimerSLAReminderFields(t *testing.T) {
	cases := []struct {
		name  string
		node  model.Node
		check func(t *testing.T, n model.Node)
	}{
		{
			name: "timer-intermediate",
			node: model.NewIntermediateCatchEvent("wait-1h",
				model.WithTimerDuration("PT1H"),
				model.WithName("Wait 1 hour"),
			),
			check: func(t *testing.T, n model.Node) {
				ice, ok := n.(model.IntermediateCatchEvent)
				require.True(t, ok)
				assert.Equal(t, "PT1H", ice.TimerDuration)
				assert.Equal(t, "Wait 1 hour", n.Name())
			},
		},
		{
			name: "sla-with-flow-and-action",
			node: model.NewUserTask("review", nil,
				model.WithName("Review"),
				model.WithSLA("P1D", "sla-breach-flow", "notify-manager"),
			),
			check: func(t *testing.T, n model.Node) {
				ut, ok := n.(model.UserTask)
				require.True(t, ok)
				assert.Equal(t, "P1D", ut.SLADuration)
				assert.Equal(t, "sla-breach-flow", ut.SLAFlow)
				assert.Equal(t, "notify-manager", ut.SLAAction)
			},
		},
		{
			name: "reminder-every-with-action",
			node: model.NewUserTask("approve", nil,
				model.WithName("Approve"),
				model.WithReminder("PT4H", "send-reminder"),
			),
			check: func(t *testing.T, n model.Node) {
				ut, ok := n.(model.UserTask)
				require.True(t, ok)
				assert.Equal(t, "PT4H", ut.ReminderEvery)
				assert.Equal(t, "send-reminder", ut.ReminderAction)
			},
		},
		{
			name: "all-six-fields",
			node: model.NewUserTask("task-full", nil,
				model.WithName("Full Task"),
				model.WithSLA("P2D", "escalate", "escalate-action"),
				model.WithReminder("PT6H", "remind-action"),
			),
			check: func(t *testing.T, n model.Node) {
				ut, ok := n.(model.UserTask)
				require.True(t, ok)
				assert.Equal(t, "P2D", ut.SLADuration)
				assert.Equal(t, "escalate", ut.SLAFlow)
				assert.Equal(t, "escalate-action", ut.SLAAction)
				assert.Equal(t, "PT6H", ut.ReminderEvery)
				assert.Equal(t, "remind-action", ut.ReminderAction)
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
			n, ok := d.Node(tc.node.ID())
			require.True(t, ok)
			tc.check(t, n)
		})
	}
}
