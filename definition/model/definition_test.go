package model_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

func linearDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "p1",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("greet", activity.WithTaskAction("greet")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
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
			activity.NewUserTask("approve", activity.WithCandidateRoles("manager", "admin"),
				activity.WithName("Approve Request"),
				activity.WithEligibilityExpr("amount > 1000"),
			),
		},
		Flows: []flow.SequenceFlow{},
	}

	n, ok := d.Node("approve")
	require.True(t, ok)
	assert.Equal(t, model.KindUserTask, n.Kind())
	assert.Equal(t, "Approve Request", n.Name())
	ut, ok := n.(activity.UserTask)
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
			node: event.NewIntermediateCatch("sig-catch", event.WithSignalName("order.placed")),
		},
		{
			name: "message-catch",
			node: event.NewIntermediateCatch("msg-catch", event.WithMessageCorrelator("payment.received", "order.id")),
		},
		{
			name: "signal-throw",
			node: event.NewIntermediateThrow("sig-throw", event.WithThrowSignalName("order.shipped")),
		},
		{
			// Zero-value NonInterrupting (false) = interrupting — the default.
			name: "boundary-interrupting-default",
			node: event.NewBoundary("boundary-1", "task-1", event.WithSignalName("cancel.signal")),
		},
		{
			// NonInterrupting: true = non-interrupting boundary event.
			name: "boundary-non-interrupting",
			node: event.NewBoundary("boundary-2", "task-2",
				event.WithMessageCorrelator("reminder.msg", ""),
				event.WithBoundaryNonInterrupting(),
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
				Flows:   []flow.SequenceFlow{},
			}
			n, ok := d.Node(tc.node.ID())
			require.True(t, ok)
			switch expected := tc.node.(type) {
			case event.IntermediateCatchEvent:
				got, ok := n.(event.IntermediateCatchEvent)
				require.True(t, ok)
				assert.Equal(t, expected.SignalName, got.SignalName)
				assert.Equal(t, expected.MessageName, got.MessageName)
				assert.Equal(t, expected.CorrelationKey, got.CorrelationKey)
			case event.IntermediateThrowEvent:
				got, ok := n.(event.IntermediateThrowEvent)
				require.True(t, ok)
				assert.Equal(t, expected.SignalName, got.SignalName)
			case event.BoundaryEvent:
				got, ok := n.(event.BoundaryEvent)
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
			event.NewStart("ns-start"),
			activity.NewServiceTask("ns-task", activity.WithTaskAction("inner-action")),
			event.NewEnd("ns-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "nf1", Source: "ns-start", Target: "ns-task"},
			{ID: "nf2", Source: "ns-task", Target: "ns-end"},
		},
	}

	d := &model.ProcessDefinition{
		ID:      "outer",
		Version: 1,
		Nodes:   []model.Node{activity.NewSubProcess("subprocess-1", nested, activity.WithName("Inner Subprocess"))},
		Flows:   []flow.SequenceFlow{},
	}

	n, ok := d.Node("subprocess-1")
	require.True(t, ok)
	assert.Equal(t, model.KindSubProcess, n.Kind())
	assert.Equal(t, "Inner Subprocess", n.Name())
	sp, ok := n.(activity.SubProcess)
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
			event.NewStart("es-start", event.WithSignalName("cancel.signal")),
			event.NewEnd("es-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "ef1", Source: "es-start", Target: "es-end"},
		},
	}

	d := &model.ProcessDefinition{
		ID:      "outer2",
		Version: 1,
		Nodes:   []model.Node{event.NewEventSubProcess("event-sub-1", nested, event.WithName("Cancel Handler"))},
		Flows:   []flow.SequenceFlow{},
	}

	n, ok := d.Node("event-sub-1")
	require.True(t, ok)
	assert.Equal(t, model.KindEventSubProcess, n.Kind())
	esp, ok := n.(event.EventSubProcess)
	require.True(t, ok)
	require.NotNil(t, esp.Subprocess)
	assert.Equal(t, "event-nested-proc", esp.Subprocess.ID)
	// The trigger is encoded on the nested start event's SignalName field.
	starts := esp.Subprocess.StartNodes()
	require.Len(t, starts, 1)
	se, ok := starts[0].(event.StartEvent)
	require.True(t, ok)
	assert.Equal(t, "cancel.signal", se.SignalName)
}

// TestNodeCallActivityDefRef asserts that a KindCallActivity node with a DefRef
// field round-trips through ProcessDefinition.Node correctly.
func TestNodeCallActivityDefRef(t *testing.T) {
	d := &model.ProcessDefinition{
		ID:      "outer3",
		Version: 1,
		Nodes:   []model.Node{activity.NewCallActivity("call-1", model.Latest("external-process-v2"), activity.WithName("Call External Process"))},
		Flows:   []flow.SequenceFlow{},
	}

	n, ok := d.Node("call-1")
	require.True(t, ok)
	assert.Equal(t, model.KindCallActivity, n.Kind())
	ca, ok := n.(activity.CallActivity)
	require.True(t, ok)
	assert.Equal(t, model.Latest("external-process-v2"), ca.DefRef)
}

// TestNodeTimerDeadlineReminderFields asserts that the six new timer/deadline/reminder
// fields on model.Node round-trip through ProcessDefinition.Node correctly.
func TestNodeTimerDeadlineReminderFields(t *testing.T) {
	cases := []struct {
		name  string
		node  model.Node
		check func(t *testing.T, n model.Node)
	}{
		{
			name: "timer-intermediate",
			node: event.NewIntermediateCatch("wait-1h",
				event.WithCatchTimer(schedule.AfterExpr("PT1H")),
				event.WithName("Wait 1 hour"),
			),
			check: func(t *testing.T, n model.Node) {
				ice, ok := n.(event.IntermediateCatchEvent)
				require.True(t, ok)
				assert.False(t, ice.Timer.IsZero(), "Timer should be set")
				expr, _, exprOk := ice.Timer.Expr()
				require.True(t, exprOk, "Timer should be an expr form")
				assert.Equal(t, "PT1H", expr, "Timer expr value should be PT1H")
				assert.Equal(t, "Wait 1 hour", n.Name())
			},
		},
		{
			name: "deadline-with-flow-and-action",
			node: activity.NewUserTask("review",
				activity.WithName("Review"),
				activity.WithWaitDeadline(schedule.AfterDuration(24*time.Hour), "sla-breach-flow"), activity.WithDeadlineAction("notify-manager"),
			),
			check: func(t *testing.T, n model.Node) {
				ut, ok := n.(activity.UserTask)
				require.True(t, ok)
				d, ok := ut.DeadlineTimer.Duration()
				require.True(t, ok)
				assert.Equal(t, 24*time.Hour, d)
				assert.Equal(t, "sla-breach-flow", ut.DeadlineFlow)
				assert.Equal(t, "notify-manager", ut.DeadlineAction)
			},
		},
		{
			name: "reminder-every-with-action",
			node: activity.NewUserTask("approve",
				activity.WithName("Approve"),
				activity.WithWaitAction(schedule.Every(4*time.Hour), "send-reminder"),
			),
			check: func(t *testing.T, n model.Node) {
				ut, ok := n.(activity.UserTask)
				require.True(t, ok)
				d, ok := ut.WaitEvery.Duration()
				require.True(t, ok)
				assert.Equal(t, 4*time.Hour, d)
				assert.Equal(t, "send-reminder", ut.WaitAction)
			},
		},
		{
			name: "all-six-fields",
			node: activity.NewUserTask("task-full",
				activity.WithName("Full Task"),
				activity.WithWaitDeadline(schedule.AfterDuration(48*time.Hour), "escalate"), activity.WithDeadlineAction("escalate-action"),
				activity.WithWaitAction(schedule.Every(6*time.Hour), "remind-action"),
			),
			check: func(t *testing.T, n model.Node) {
				ut, ok := n.(activity.UserTask)
				require.True(t, ok)
				dd, ok := ut.DeadlineTimer.Duration()
				require.True(t, ok)
				assert.Equal(t, 48*time.Hour, dd)
				assert.Equal(t, "escalate", ut.DeadlineFlow)
				assert.Equal(t, "escalate-action", ut.DeadlineAction)
				rd, ok := ut.WaitEvery.Duration()
				require.True(t, ok)
				assert.Equal(t, 6*time.Hour, rd)
				assert.Equal(t, "remind-action", ut.WaitAction)
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
				Flows:   []flow.SequenceFlow{},
			}
			n, ok := d.Node(tc.node.ID())
			require.True(t, ok)
			tc.check(t, n)
		})
	}
}
