package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
)

// receiveReminderDef returns a linear definition whose ReceiveTask parks awaiting
// the "PaymentReceived" message and carries a recurring in-wait reminder:
//
//	Start → receive[ReceiveTask "PaymentReceived", WithWaitAction(Every 30m, "nudge")] → End
func receiveReminderDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-recv-reminder", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewReceiveTask("receive", "PaymentReceived",
				activity.WithWaitAction(schedule.Every(30*time.Minute), "nudge")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "receive"},
			{ID: "f2", Source: "receive", Target: "end"},
		},
	}
}

// TestReceiveTaskReminderFiresAndCancelsOnMessage verifies that a ReceiveTask
// carrying an in-wait reminder arms it, fires the nudge while parked, and cancels
// it when the awaited message arrives.
func TestReceiveTaskReminderFiresAndCancelsOnMessage(t *testing.T) {
	def := receiveReminderDef()
	startAt := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)

	// ---- Step 1: Start → parked at receive, reminder armed ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(startAt, nil), engine.StepOptions{})
	require.NoError(t, err)

	var reminderST engine.ScheduleTimer
	for _, c := range r1.Commands {
		if st, ok := c.(engine.ScheduleTimer); ok && st.Kind == engine.TimerInWait {
			reminderST = st
		}
	}
	require.NotEmpty(t, reminderST.TimerID, "expected a TimerInWait ScheduleTimer on ReceiveTask entry; got: %v", r1.Commands)
	assert.True(t, reminderST.Trigger.Recurring(), "reminder trigger must be recurring")

	// Token parks awaiting the message.
	require.Len(t, r1.State.Tokens, 1)
	tok := r1.State.Tokens[0]
	assert.Equal(t, "receive", tok.NodeID)
	assert.Equal(t, engine.TokenWaitingCommand, tok.State)
	assert.Equal(t, "PaymentReceived", tok.AwaitMessage)

	reminderID := reminderST.TimerID

	// ---- Step 2: reminder fires while parked → nudge only, token stays ----
	fireAt := startAt.Add(30 * time.Minute)
	r2, err := engine.Step(def, r1.State,
		engine.NewTimerFired(fireAt, reminderID), engine.StepOptions{})
	require.NoError(t, err)

	var nudge engine.InvokeAction
	var foundNudge bool
	for _, c := range r2.Commands {
		switch v := c.(type) {
		case engine.InvokeAction:
			nudge = v
			foundNudge = true
		case engine.CancelTimer, engine.CompleteInstance:
			t.Errorf("unexpected command %T while parked (no cancel/complete on nudge): %v", c, c)
		}
	}
	require.True(t, foundNudge, "expected InvokeAction(nudge) after reminder fire; got: %v", r2.Commands)
	assert.Equal(t, "nudge", nudge.Name)
	assert.True(t, nudge.FireAndForget, "reminder nudge must be fire-and-forget")

	// Token still parked awaiting the message.
	require.Len(t, r2.State.Tokens, 1)
	assert.Equal(t, "PaymentReceived", r2.State.Tokens[0].AwaitMessage)
	// Reminder record still present.
	var stillArmed bool
	for _, tr := range r2.State.Timers {
		if tr.TimerID == reminderID {
			stillArmed = true
		}
	}
	assert.True(t, stillArmed, "reminder record must persist while parked")

	// ---- Step 3: message arrives → token advances + reminder cancelled ----
	msgAt := startAt.Add(45 * time.Minute)
	r3, err := engine.Step(def, r2.State,
		engine.NewMessageReceived(msgAt, "PaymentReceived", "", nil), engine.StepOptions{})
	require.NoError(t, err)

	var foundCancel bool
	for _, c := range r3.Commands {
		if ct, ok := c.(engine.CancelTimer); ok && ct.TimerID == reminderID {
			foundCancel = true
		}
	}
	assert.True(t, foundCancel, "message arrival must emit CancelTimer for the reminder (id=%s); got: %v", reminderID, r3.Commands)

	// No TimerInWait record remains.
	for _, tr := range r3.State.Timers {
		assert.NotEqual(t, reminderID, tr.TimerID, "reminder record must be removed on message")
	}
	// Instance completed (advanced past receive to end).
	assert.Equal(t, engine.StatusCompleted, r3.State.Status)
}
