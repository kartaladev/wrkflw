package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// catchReminderInterruptDef builds a sub-process that forks into a
// catch-with-reminder branch and an error-end branch. The error-end throws E1,
// which the sub-process's interrupting error boundary catches — cancelling the
// scope's parked catch token. The catch token carries an in-wait reminder that
// must be cancelled by the interrupt so it cannot re-fire afterward.
//
//	Root:   start → sp → recover → end        (sp boundary error "E1" → recover)
//	Nested: inner-start → fork(pgw)
//	                       ├─ await[catch signal "resume", reminder Every 30m "nudge"]
//	                       └─ throw[errorEnd E1]
func catchReminderInterruptDef() *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "sp-nested", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			gateway.NewParallel("pgw"),
			event.NewIntermediateCatch("await",
				event.WithCatchSignal("resume"),
				event.WithCatchWaitReminder(schedule.Every(30*time.Minute), "nudge")),
			event.NewEnd("await-end"),
			event.NewErrorEnd("boom", "E1"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "fi1", Source: "inner-start", Target: "pgw"},
			{ID: "fi2", Source: "pgw", Target: "await"},
			{ID: "fi3", Source: "pgw", Target: "boom"},
			{ID: "fi4", Source: "await", Target: "await-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p-catch-reminder-interrupt", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sp", nested),
			event.NewBoundary("bnd-err", "sp", event.WithBoundaryErrorCode("E1")),
			activity.NewServiceTask("recover", activity.WithTaskAction("recover-action")),
			event.NewEnd("end"),
			event.NewEnd("end-ok"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start-sp", Source: "start", Target: "sp"},
			{ID: "f-sp-endok", Source: "sp", Target: "end-ok"},
			{ID: "f-bnd-recover", Source: "bnd-err", Target: "recover"},
			{ID: "f-recover-end", Source: "recover", Target: "end"},
		},
	}
}

// TestCatchReminderCancelledOnErrorInterrupt asserts that when a scope carrying a
// parked catch-with-reminder is interrupted (error boundary), the reminder's
// CancelTimer is emitted and no TimerInWait record survives — so it cannot
// re-fire after the interrupt.
func TestCatchReminderCancelledOnErrorInterrupt(t *testing.T) {
	def := catchReminderInterruptDef()
	startAt := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)

	// Start: forks inside sp → one branch parks at catch (arming the reminder),
	// the other throws E1 which the boundary catches, interrupting the scope.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(startAt, nil), engine.StepOptions{})
	require.NoError(t, err)

	// The reminder timer was armed for the catch token.
	var reminderID string
	for _, c := range r1.Commands {
		if st, ok := c.(engine.ScheduleTimer); ok && st.Kind == engine.TimerInWait {
			reminderID = st.TimerID
		}
	}
	require.NotEmpty(t, reminderID, "expected a TimerInWait ScheduleTimer for the parked catch; got: %v", r1.Commands)

	// The interrupt must have cancelled the reminder.
	var foundCancel bool
	for _, c := range r1.Commands {
		if ct, ok := c.(engine.CancelTimer); ok && ct.TimerID == reminderID {
			foundCancel = true
		}
	}
	assert.True(t, foundCancel, "error interrupt must emit CancelTimer for the catch reminder (id=%s); got: %v", reminderID, r1.Commands)

	// No TimerInWait record survives the interrupt.
	for _, tr := range r1.State.Timers {
		assert.NotEqual(t, reminderID, tr.TimerID, "reminder record must not survive the interrupt")
	}

	// A late reminder fire is a clean no-op (record gone, token consumed).
	r2, err := engine.Step(def, r1.State,
		engine.NewTimerFired(startAt.Add(30*time.Minute), reminderID), engine.StepOptions{})
	require.NoError(t, err)
	for _, c := range r2.Commands {
		if ia, ok := c.(engine.InvokeAction); ok {
			assert.NotEqual(t, "nudge", ia.Name, "no nudge may fire after the interrupt")
		}
	}
}
