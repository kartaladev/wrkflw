package definition_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition"
)

func linearDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID:      "p1",
		Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewServiceTask("greet", definition.WithActionName("greet")),
			definition.NewEndEvent("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "greet"},
			{ID: "f2", Source: "greet", Target: "end"},
		},
	}
}

func TestProcessDefinitionLookups(t *testing.T) {
	d := linearDef()

	n, ok := d.Node("greet")
	require.True(t, ok)
	assert.Equal(t, definition.KindServiceTask, n.Kind())

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
	d := &definition.ProcessDefinition{
		ID:      "p2",
		Version: 1,
		Nodes: []definition.Node{
			definition.NewUserTask("approve", []string{"manager", "admin"},
				definition.WithName("Approve Request"),
				definition.WithEligibilityExpr("amount > 1000"),
			),
		},
		Flows: []definition.SequenceFlow{},
	}

	n, ok := d.Node("approve")
	require.True(t, ok)
	assert.Equal(t, definition.KindUserTask, n.Kind())
	assert.Equal(t, "Approve Request", n.Name())
	ut, ok := n.(definition.UserTask)
	require.True(t, ok)
	assert.Equal(t, []string{"manager", "admin"}, ut.CandidateRoles)
	assert.Equal(t, "amount > 1000", ut.EligibilityExpr)
}

// TestNodeEventBoundaryFields asserts that the five new event/boundary fields
// on definition.Node round-trip through ProcessDefinition.Node correctly.
func TestNodeEventBoundaryFields(t *testing.T) {
	cases := []struct {
		name string
		node definition.Node
	}{
		{
			name: "signal-catch",
			node: definition.NewIntermediateCatchEvent("sig-catch", definition.WithSignalName("order.placed")),
		},
		{
			name: "message-catch",
			node: definition.NewIntermediateCatchEvent("msg-catch", definition.WithMessageNameAndKey("payment.received", "order.id")),
		},
		{
			name: "signal-throw",
			node: definition.NewIntermediateThrowEvent("sig-throw", definition.WithThrowSignal("order.shipped")),
		},
		{
			// Zero-value NonInterrupting (false) = interrupting — the default.
			name: "boundary-interrupting-default",
			node: definition.NewBoundaryEvent("boundary-1", "task-1", definition.WithBoundarySignal("cancel.signal")),
		},
		{
			// NonInterrupting: true = non-interrupting boundary event.
			name: "boundary-non-interrupting",
			node: definition.NewBoundaryEvent("boundary-2", "task-2",
				definition.WithBoundaryMessage("reminder.msg", ""),
				definition.BoundaryNonInterrupting(),
			),
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			d := &definition.ProcessDefinition{
				ID:      "p-event",
				Version: 1,
				Nodes:   []definition.Node{tc.node},
				Flows:   []definition.SequenceFlow{},
			}
			n, ok := d.Node(tc.node.ID())
			require.True(t, ok)
			switch expected := tc.node.(type) {
			case definition.IntermediateCatchEvent:
				got, ok := n.(definition.IntermediateCatchEvent)
				require.True(t, ok)
				assert.Equal(t, expected.SignalName, got.SignalName)
				assert.Equal(t, expected.MessageName, got.MessageName)
				assert.Equal(t, expected.CorrelationKey, got.CorrelationKey)
			case definition.IntermediateThrowEvent:
				got, ok := n.(definition.IntermediateThrowEvent)
				require.True(t, ok)
				assert.Equal(t, expected.SignalName, got.SignalName)
			case definition.BoundaryEvent:
				got, ok := n.(definition.BoundaryEvent)
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
	nested := &definition.ProcessDefinition{
		ID:      "nested-proc",
		Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("ns-start"),
			definition.NewServiceTask("ns-task", definition.WithActionName("inner-action")),
			definition.NewEndEvent("ns-end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "nf1", Source: "ns-start", Target: "ns-task"},
			{ID: "nf2", Source: "ns-task", Target: "ns-end"},
		},
	}

	d := &definition.ProcessDefinition{
		ID:      "outer",
		Version: 1,
		Nodes:   []definition.Node{definition.NewSubProcess("subprocess-1", nested, definition.WithName("Inner Subprocess"))},
		Flows:   []definition.SequenceFlow{},
	}

	n, ok := d.Node("subprocess-1")
	require.True(t, ok)
	assert.Equal(t, definition.KindSubProcess, n.Kind())
	assert.Equal(t, "Inner Subprocess", n.Name())
	sp, ok := n.(definition.SubProcess)
	require.True(t, ok)
	require.NotNil(t, sp.Subprocess)
	assert.Equal(t, "nested-proc", sp.Subprocess.ID)
	assert.Len(t, sp.Subprocess.Nodes, 3)
	assert.Len(t, sp.Subprocess.Flows, 2)
}

// TestNodeEventSubProcessField asserts that a KindEventSubProcess node with a
// nested Subprocess *ProcessDefinition round-trips correctly.
func TestNodeEventSubProcessField(t *testing.T) {
	nested := &definition.ProcessDefinition{
		ID:      "event-nested-proc",
		Version: 1,
		Nodes: []definition.Node{
			// The trigger is encoded on the nested StartEvent's SignalName field.
			definition.NewStartEvent("es-start", definition.WithStartSignal("cancel.signal")),
			definition.NewEndEvent("es-end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "ef1", Source: "es-start", Target: "es-end"},
		},
	}

	d := &definition.ProcessDefinition{
		ID:      "outer2",
		Version: 1,
		Nodes:   []definition.Node{definition.NewEventSubProcess("event-sub-1", nested, definition.WithName("Cancel Handler"))},
		Flows:   []definition.SequenceFlow{},
	}

	n, ok := d.Node("event-sub-1")
	require.True(t, ok)
	assert.Equal(t, definition.KindEventSubProcess, n.Kind())
	esp, ok := n.(definition.EventSubProcess)
	require.True(t, ok)
	require.NotNil(t, esp.Subprocess)
	assert.Equal(t, "event-nested-proc", esp.Subprocess.ID)
	// The trigger is encoded on the nested start event's SignalName field.
	starts := esp.Subprocess.StartNodes()
	require.Len(t, starts, 1)
	se, ok := starts[0].(definition.StartEvent)
	require.True(t, ok)
	assert.Equal(t, "cancel.signal", se.SignalName)
}

// TestNodeCallActivityDefRef asserts that a KindCallActivity node with a DefRef
// field round-trips through ProcessDefinition.Node correctly.
func TestNodeCallActivityDefRef(t *testing.T) {
	d := &definition.ProcessDefinition{
		ID:      "outer3",
		Version: 1,
		Nodes:   []definition.Node{definition.NewCallActivity("call-1", "external-process-v2", definition.WithName("Call External Process"))},
		Flows:   []definition.SequenceFlow{},
	}

	n, ok := d.Node("call-1")
	require.True(t, ok)
	assert.Equal(t, definition.KindCallActivity, n.Kind())
	ca, ok := n.(definition.CallActivity)
	require.True(t, ok)
	assert.Equal(t, "external-process-v2", ca.DefRef)
}

// TestNodeTimerDeadlineReminderFields asserts that the six new timer/deadline/reminder
// fields on definition.Node round-trip through ProcessDefinition.Node correctly.
func TestNodeTimerDeadlineReminderFields(t *testing.T) {
	cases := []struct {
		name  string
		node  definition.Node
		check func(t *testing.T, n definition.Node)
	}{
		{
			name: "timer-intermediate",
			node: definition.NewIntermediateCatchEvent("wait-1h",
				definition.WithTimerDuration("PT1H"),
				definition.WithName("Wait 1 hour"),
			),
			check: func(t *testing.T, n definition.Node) {
				ice, ok := n.(definition.IntermediateCatchEvent)
				require.True(t, ok)
				assert.Equal(t, "PT1H", ice.TimerDuration)
				assert.Equal(t, "Wait 1 hour", n.Name())
			},
		},
		{
			name: "deadline-with-flow-and-action",
			node: definition.NewUserTask("review", nil,
				definition.WithName("Review"),
				definition.WithDeadline("P1D", "sla-breach-flow", "notify-manager"),
			),
			check: func(t *testing.T, n definition.Node) {
				ut, ok := n.(definition.UserTask)
				require.True(t, ok)
				assert.Equal(t, "P1D", ut.DeadlineDuration)
				assert.Equal(t, "sla-breach-flow", ut.DeadlineFlow)
				assert.Equal(t, "notify-manager", ut.DeadlineAction)
			},
		},
		{
			name: "reminder-every-with-action",
			node: definition.NewUserTask("approve", nil,
				definition.WithName("Approve"),
				definition.WithReminder("PT4H", "send-reminder"),
			),
			check: func(t *testing.T, n definition.Node) {
				ut, ok := n.(definition.UserTask)
				require.True(t, ok)
				assert.Equal(t, "PT4H", ut.ReminderEvery)
				assert.Equal(t, "send-reminder", ut.ReminderAction)
			},
		},
		{
			name: "all-six-fields",
			node: definition.NewUserTask("task-full", nil,
				definition.WithName("Full Task"),
				definition.WithDeadline("P2D", "escalate", "escalate-action"),
				definition.WithReminder("PT6H", "remind-action"),
			),
			check: func(t *testing.T, n definition.Node) {
				ut, ok := n.(definition.UserTask)
				require.True(t, ok)
				assert.Equal(t, "P2D", ut.DeadlineDuration)
				assert.Equal(t, "escalate", ut.DeadlineFlow)
				assert.Equal(t, "escalate-action", ut.DeadlineAction)
				assert.Equal(t, "PT6H", ut.ReminderEvery)
				assert.Equal(t, "remind-action", ut.ReminderAction)
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			d := &definition.ProcessDefinition{
				ID:      "p-timer",
				Version: 1,
				Nodes:   []definition.Node{tc.node},
				Flows:   []definition.SequenceFlow{},
			}
			n, ok := d.Node(tc.node.ID())
			require.True(t, ok)
			tc.check(t, n)
		})
	}
}
