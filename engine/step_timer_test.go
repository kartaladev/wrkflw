package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
)

// timerDef returns a linear definition:
//
//	Start → TimerCatch("1h") → ServiceTask(notify) → End
func timerDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-timer", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewCatch("wait1h", event.WithCatchTimer(schedule.AfterExpr(`"1h"`))),
			activity.NewServiceTask("notify", activity.WithActionName("send-notification")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait1h"},
			{ID: "f2", Source: "wait1h", Target: "notify"},
			{ID: "f3", Source: "notify", Target: "end"},
		},
	}
}

// TestTimerIntermediateSchedulesAndResumes verifies the full lifecycle of a timer
// intermediate catch event:
//
//  1. StartInstance drives into the timer node → emits exactly one ScheduleTimer
//     with Kind==TimerIntermediate, FireAt==start+1h, Token==parked-token-id, and
//     TimerID deterministic (<instanceID>-tm<seq>); the token is parked.
//  2. Feeding TimerFired with that TimerID advances the token past the catch event
//     into the service task, emitting InvokeAction.
func TestTimerIntermediateSchedulesAndResumes(t *testing.T) {
	def := timerDef()
	startAt := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	fireAt := startAt.Add(time.Hour)

	// ---- Step 1: StartInstance ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(startAt, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Exactly one command: ScheduleTimer.
	require.Len(t, r1.Commands, 1, "expected exactly one command after StartInstance into timer node")
	st, ok := r1.Commands[0].(engine.ScheduleTimer)
	require.True(t, ok, "expected ScheduleTimer command, got %T", r1.Commands[0])

	// TimerID must be deterministic: <instanceID>-tm<seq> where seq starts at 1.
	assert.Equal(t, "i1-tm1", st.TimerID)
	assert.Equal(t, engine.TimerIntermediate, st.Kind)
	// AfterExpr("1h") resolves to AfterDuration(1h); the timer carries the raw
	// duration trigger (no FireAt) and the scheduler owns the fire instant.
	d, ok := st.Trigger.Duration()
	require.True(t, ok, "timer trigger must reduce to a duration, got %+v", st.Trigger)
	assert.Equal(t, time.Hour, d)

	// Token is parked at the timer node.
	require.Len(t, r1.State.Tokens, 1)
	tok := r1.State.Tokens[0]
	assert.Equal(t, "wait1h", tok.NodeID)
	assert.Equal(t, engine.TokenWaitingCommand, tok.State)
	assert.Equal(t, "i1-tm1", tok.AwaitCommand)

	// ScheduleTimer.Token must reference the parked token ID.
	assert.Equal(t, tok.ID, st.Token)

	// Instance still running.
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// ---- Step 2: TimerFired ----
	r2, err := engine.Step(def, r1.State,
		engine.NewTimerFired(fireAt, "i1-tm1"), engine.StepOptions{})
	require.NoError(t, err)

	// Exactly one command: InvokeAction for the service task.
	require.Len(t, r2.Commands, 1, "expected InvokeAction after TimerFired")
	ia, ok := r2.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "expected InvokeAction command, got %T", r2.Commands[0])
	assert.Equal(t, "send-notification", ia.Name)

	// Token advanced past the timer node to the service task.
	require.Len(t, r2.State.Tokens, 1)
	assert.Equal(t, "notify", r2.State.Tokens[0].NodeID)
	assert.Equal(t, engine.TokenWaitingCommand, r2.State.Tokens[0].State)

	// Instance still running.
	assert.Equal(t, engine.StatusRunning, r2.State.Status)
}

// TestTimerFiredStaleTokenIsNoop verifies that feeding a TimerFired trigger whose
// TimerID no longer corresponds to any parked token (stale/already-moved) is a
// clean no-op: no commands, no error, and the instance state is effectively unchanged.
//
// This is intentional: timers are inherently racy with other completion paths, and
// a stale TimerFired must never corrupt state or return an error (unlike
// HumanCompleted which fails fast on an unknown token — timers can arrive late).
func TestTimerFiredStaleTokenIsNoop(t *testing.T) {
	cases := []struct {
		name    string
		timerID string
	}{
		{
			name:    "completely unknown timerID",
			timerID: "i1-tm99",
		},
		{
			name:    "empty timerID",
			timerID: "",
		},
	}

	def := timerDef()
	startAt := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	// Bring the instance to the parked-at-timer state.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(startAt, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Advance past the timer (move the token) so the timer's AwaitCommand is gone.
	r2, err := engine.Step(def, r1.State,
		engine.NewTimerFired(startAt.Add(time.Hour), "i1-tm1"), engine.StepOptions{})
	require.NoError(t, err)
	// At this point r2.State has the token at "notify" (parked on a command ID), not on the timer.

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Feed a stale/unknown TimerFired.
			r, err := engine.Step(def, r2.State,
				engine.NewTimerFired(startAt.Add(2*time.Hour), tc.timerID), engine.StepOptions{})
			// Must be a clean no-op: no error, no commands.
			require.NoError(t, err, "stale TimerFired must not error")
			assert.Empty(t, r.Commands, "stale TimerFired must emit no commands")

			// State should be unchanged (token still at "notify", same status).
			require.Len(t, r.State.Tokens, 1)
			assert.Equal(t, "notify", r.State.Tokens[0].NodeID)
			assert.Equal(t, engine.StatusRunning, r.State.Status)
		})
	}
}

// deadlineDef returns a definition with a user task that has a 3h deadline:
//
//	Start → userTask(DeadlineDuration:"3h", DeadlineFlow:"escalate", DeadlineAction:"notify") → normalEnd
//	userTask → (escalate flow) → escalateNode
//
// "escalate" is the flow id from userTask to escalateNode (the alternative end event).
func deadlineDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-deadline", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("userTask", []string{"manager"}, activity.WithDeadline(schedule.AfterExpr(`"3h"`), "escalate", "notify")),
			event.NewEnd("normalEnd"),
			event.NewEnd("escalateNode"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "userTask"},
			{ID: "f2", Source: "userTask", Target: "normalEnd"},
			{ID: "escalate", Source: "userTask", Target: "escalateNode"},
		},
	}
}

// TestUserTaskDeadlineBreachTakesAlternativePath verifies the deadline breach path:
//  1. Entering the user-task node emits AwaitHuman AND ScheduleTimer(Kind=TimerDeadline, FireAt=entry+3h).
//  2. The HumanTask.DueAt is set to FireAt.
//  3. Without completing the task, feeding the deadline TimerFired:
//     - emits InvokeAction("notify"),
//     - moves the token to the escalate node (CompleteInstance since it's an EndEvent),
//     - marks the task Cancelled (emits UpdateTask).
func TestUserTaskDeadlineBreachTakesAlternativePath(t *testing.T) {
	def := deadlineDef()
	startAt := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	fireAt := startAt.Add(3 * time.Hour)

	// ---- Step 1: Start → parked at userTask ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(startAt, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Commands: AwaitHuman + ScheduleTimer (deadline).
	require.Len(t, r1.Commands, 2, "expected AwaitHuman + ScheduleTimer on user-task entry")

	var ah engine.AwaitHuman
	var st engine.ScheduleTimer
	var foundAH, foundST bool
	for _, c := range r1.Commands {
		switch v := c.(type) {
		case engine.AwaitHuman:
			ah = v
			foundAH = true
		case engine.ScheduleTimer:
			st = v
			foundST = true
		}
	}
	require.True(t, foundAH, "AwaitHuman not found in commands")
	require.True(t, foundST, "ScheduleTimer not found in commands")

	// TaskToken deterministic.
	assert.Equal(t, "i1-h1", ah.TaskToken)
	assert.Equal(t, []string{"manager"}, ah.Eligibility.Roles)

	// Deadline timer properties. AfterExpr("3h") resolves to AfterDuration(3h);
	// the command carries the raw duration trigger (no FireAt).
	assert.Equal(t, engine.TimerDeadline, st.Kind)
	dd, ok := st.Trigger.Duration()
	require.True(t, ok, "deadline trigger must reduce to a duration, got %+v", st.Trigger)
	assert.Equal(t, 3*time.Hour, dd)
	assert.NotEmpty(t, st.TimerID)
	deadlineTimerID := st.TimerID

	// Token parked on the task (AwaitCommand == TaskToken).
	require.Len(t, r1.State.Tokens, 1)
	tok := r1.State.Tokens[0]
	assert.Equal(t, "userTask", tok.NodeID)
	assert.Equal(t, engine.TokenWaitingCommand, tok.State)
	assert.Equal(t, "i1-h1", tok.AwaitCommand)

	// Token referenced in ScheduleTimer matches the parked token.
	assert.Equal(t, tok.ID, st.Token)

	// HumanTask.DueAt is set to the deadline fire time.
	require.Len(t, r1.State.Tasks, 1)
	ht := r1.State.Tasks[0]
	require.NotNil(t, ht.DueAt, "HumanTask.DueAt must be set when DeadlineDuration is defined")
	assert.Equal(t, fireAt, *ht.DueAt)
	assert.Equal(t, humantask.Unclaimed, ht.State)

	// Instance still running.
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// ---- Step 2: deadline fires (task NOT completed) → breach ----
	r2, err := engine.Step(def, r1.State,
		engine.NewTimerFired(fireAt, deadlineTimerID), engine.StepOptions{})
	require.NoError(t, err)

	// Expected commands: InvokeAction("notify") + UpdateTask(Cancelled) + CompleteInstance
	// (because escalateNode is an EndEvent).
	var ia engine.InvokeAction
	var ut engine.UpdateTask
	var ci engine.CompleteInstance
	var foundIA, foundUT, foundCI bool
	for _, c := range r2.Commands {
		switch v := c.(type) {
		case engine.InvokeAction:
			ia = v
			foundIA = true
		case engine.UpdateTask:
			ut = v
			foundUT = true
		case engine.CompleteInstance:
			ci = v
			foundCI = true
		}
	}
	require.True(t, foundIA, "InvokeAction not found in breach commands; got: %v", r2.Commands)
	require.True(t, foundUT, "UpdateTask not found in breach commands; got: %v", r2.Commands)
	require.True(t, foundCI, "CompleteInstance not found in breach commands (escalateNode is EndEvent); got: %v", r2.Commands)

	// InvokeAction invokes the deadline action.
	assert.Equal(t, "notify", ia.Name)

	// UpdateTask marks the task Cancelled.
	assert.Equal(t, "i1-h1", ut.Task.TaskToken)
	assert.Equal(t, humantask.Cancelled, ut.Task.State)

	// CompleteInstance confirms the process reached the escalate end.
	_ = ci // just verify it's present

	// Task in state is Cancelled.
	require.Len(t, r2.State.Tasks, 1)
	assert.Equal(t, humantask.Cancelled, r2.State.Tasks[0].State)

	// Instance is completed (token consumed at the end event).
	assert.Equal(t, engine.StatusCompleted, r2.State.Status)
	assert.Empty(t, r2.State.Tokens)
}

// TestUserTaskCompletedBeforeDeadlineIgnoresTimer verifies that if the task is
// completed before the deadline fires, the late deadline TimerFired is a clean no-op:
// no commands, no error, instance already advanced past the user task.
//
// Fix B: also asserts that the HumanCompleted step emitted a CancelTimer for
// the deadline timer, proving cancellation on task completion, not just late-fire no-op.
func TestUserTaskCompletedBeforeDeadlineIgnoresTimer(t *testing.T) {
	def := deadlineDef()
	startAt := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	fireAt := startAt.Add(3 * time.Hour)

	// Bring the instance to the parked user-task state.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(startAt, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Extract the deadline timer ID.
	var deadlineTimerID string
	for _, c := range r1.Commands {
		if st, ok := c.(engine.ScheduleTimer); ok && st.Kind == engine.TimerDeadline {
			deadlineTimerID = st.TimerID
		}
	}
	require.NotEmpty(t, deadlineTimerID, "expected deadline timer to be scheduled")

	// Complete the task BEFORE the deadline fires.
	actor := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	completeAt := startAt.Add(time.Hour) // well before the 3h deadline
	r2, err := engine.Step(def, r1.State,
		engine.NewHumanCompleted(completeAt, "i1-h1", nil, actor), engine.StepOptions{})
	require.NoError(t, err)
	// Instance must have completed via the normal end path.
	assert.Equal(t, engine.StatusCompleted, r2.State.Status)

	// Fix B: HumanCompleted must emit a CancelTimer for the deadline timer, proving the
	// deadline is actively cancelled on task completion (not just a late-fire no-op).
	var foundCancel bool
	for _, c := range r2.Commands {
		if ct, ok := c.(engine.CancelTimer); ok && ct.TimerID == deadlineTimerID {
			foundCancel = true
		}
	}
	assert.True(t, foundCancel, "HumanCompleted must emit CancelTimer for the deadline timer (id=%s); got: %v", deadlineTimerID, r2.Commands)

	// Now the deadline fires late.
	r3, err := engine.Step(def, r2.State,
		engine.NewTimerFired(fireAt, deadlineTimerID), engine.StepOptions{})
	require.NoError(t, err, "late deadline TimerFired must not error")
	assert.Empty(t, r3.Commands, "late deadline TimerFired must emit no commands")
	// State unchanged (already completed).
	assert.Equal(t, engine.StatusCompleted, r3.State.Status)
}

// reminderDef returns a definition with a user task that has both a reminder
// (ReminderEvery:"1h", ReminderAction:"remind") and a deadline (DeadlineDuration:"3h"):
//
//	Start → userTask → normalEnd
//	userTask → (escalate flow) → escalateEnd
func reminderDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-reminder", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("userTask", []string{"manager"}, activity.WithDeadline(schedule.AfterExpr(`"3h"`), "escalate", "notify"), activity.WithWaitReminder(schedule.EveryExpr(`"1h"`), "remind")),
			event.NewEnd("normalEnd"),
			event.NewEnd("escalateNode"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "userTask"},
			{ID: "f2", Source: "userTask", Target: "normalEnd"},
			{ID: "escalate", Source: "userTask", Target: "escalateNode"},
		},
	}
}

// TestInWaitReminderRepeatsUntilCompletion verifies the full reminder lifecycle
// AFTER Plan 2 Task 1 moved recurrence to the scheduler (native recurring trigger,
// no engine reschedule):
//  1. Entering a user task with ReminderEvery emits AwaitHuman + ScheduleTimer(Deadline) + ScheduleTimer(InWait).
//     The in-wait timer carries a RECURRING Every(1h) trigger — armed ONCE.
//  2. Each TimerFired for the in-wait reminder emits InvokeAction("remind") ONLY,
//     and NO new ScheduleTimer: native scheduler recurrence re-delivers TimerFired
//     on the same timer id (repeated-fire behavior is the scheduler's job, covered
//     in Plan 2 Tasks 2–4 scheduler tests — not the engine's anymore).
//  3. Token does not move; task remains Unclaimed/Claimed; the reminder record persists.
//  4. Fire the reminder twice to confirm the SAME id keeps re-delivering without re-arm.
//  5. HumanCompleted emits CancelTimer for the (single) reminder timer + the deadline.
//  6. A late reminder TimerFired after completion is a clean no-op.
func TestInWaitReminderRepeatsUntilCompletion(t *testing.T) {
	def := reminderDef()
	startAt := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	// ---- Step 1: Start → parked at userTask ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(startAt, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Expect 3 commands: AwaitHuman + ScheduleTimer(Deadline) + ScheduleTimer(InWait).
	require.Len(t, r1.Commands, 3, "expected AwaitHuman + ScheduleTimer(Deadline) + ScheduleTimer(InWait) on user-task entry; got: %v", r1.Commands)

	var ah engine.AwaitHuman
	var deadlineST, reminderST engine.ScheduleTimer
	var foundAH bool
	for _, c := range r1.Commands {
		switch v := c.(type) {
		case engine.AwaitHuman:
			ah = v
			foundAH = true
		case engine.ScheduleTimer:
			switch v.Kind {
			case engine.TimerDeadline:
				deadlineST = v
			case engine.TimerInWait:
				reminderST = v
			}
		}
	}
	require.True(t, foundAH, "AwaitHuman not found in entry commands")
	require.NotEmpty(t, deadlineST.TimerID, "Deadline ScheduleTimer not found in entry commands")
	require.NotEmpty(t, reminderST.TimerID, "InWait ScheduleTimer not found in entry commands")

	// TaskToken is deterministic.
	assert.Equal(t, "i1-h1", ah.TaskToken)

	// Deadline: AfterExpr("3h") → AfterDuration(3h) (one-shot).
	assert.Equal(t, engine.TimerDeadline, deadlineST.Kind)
	dd, ok := deadlineST.Trigger.Duration()
	require.True(t, ok, "deadline trigger must reduce to a duration, got %+v", deadlineST.Trigger)
	assert.Equal(t, 3*time.Hour, dd)
	assert.False(t, deadlineST.Trigger.Recurring(), "deadline is one-shot")
	deadlineTimerID := deadlineST.TimerID

	// Reminder: EveryExpr("1h") → Every(1h) — a RECURRING trigger armed once.
	assert.Equal(t, engine.TimerInWait, reminderST.Kind)
	rd, ok := reminderST.Trigger.Duration()
	require.True(t, ok, "reminder trigger must expose its interval, got %+v", reminderST.Trigger)
	assert.Equal(t, time.Hour, rd, "reminder interval must be 1h")
	assert.True(t, reminderST.Trigger.Recurring(), "reminder trigger must be recurring (native scheduler owns re-fire)")

	// Timer ids are distinct.
	assert.NotEqual(t, deadlineTimerID, reminderST.TimerID, "deadline and reminder timer ids must differ")

	// Token parked at the user task (not moved).
	require.Len(t, r1.State.Tokens, 1)
	tok := r1.State.Tokens[0]
	assert.Equal(t, "userTask", tok.NodeID)
	assert.Equal(t, engine.TokenWaitingCommand, tok.State)
	assert.Equal(t, "i1-h1", tok.AwaitCommand, "token must remain parked on the task token")

	// Task state is Unclaimed.
	require.Len(t, r1.State.Tasks, 1)
	assert.Equal(t, humantask.Unclaimed, r1.State.Tasks[0].State)

	// The reminder timer id is stable across fires: native recurrence re-delivers
	// TimerFired on this SAME id, so the engine no longer allocates a new one.
	reminderID := reminderST.TimerID

	// ---- Step 2: first reminder fires ----
	fire1At := startAt.Add(time.Hour)
	r2, err := engine.Step(def, r1.State,
		engine.NewTimerFired(fire1At, reminderID), engine.StepOptions{})
	require.NoError(t, err)

	// Expect: InvokeAction("remind") ONLY. NO new ScheduleTimer (recurrence is native
	// now), no CancelTimer, no UpdateTask, no CompleteInstance.
	var ia1 engine.InvokeAction
	var foundIA1 bool
	for _, c := range r2.Commands {
		switch v := c.(type) {
		case engine.InvokeAction:
			ia1 = v
			foundIA1 = true
		case engine.ScheduleTimer, engine.CancelTimer, engine.UpdateTask, engine.CompleteInstance:
			t.Errorf("unexpected command %T after first reminder fire (engine must NOT reschedule): %v", c, c)
		}
	}
	require.True(t, foundIA1, "InvokeAction not found after first reminder; got: %v", r2.Commands)
	assert.Equal(t, "remind", ia1.Name, "InvokeAction name must be the ReminderAction")

	// Token must NOT move — still parked at userTask; reminder record persists.
	require.Len(t, r2.State.Tokens, 1)
	assert.Equal(t, "userTask", r2.State.Tokens[0].NodeID)
	assert.Equal(t, engine.TokenWaitingCommand, r2.State.Tokens[0].State)
	assert.Equal(t, "i1-h1", r2.State.Tokens[0].AwaitCommand)

	// Task still Unclaimed.
	require.Len(t, r2.State.Tasks, 1)
	assert.Equal(t, humantask.Unclaimed, r2.State.Tasks[0].State)

	// ---- Step 3: second reminder fires on the SAME id (proves native re-delivery) ----
	fire2At := fire1At.Add(time.Hour)
	r3, err := engine.Step(def, r2.State,
		engine.NewTimerFired(fire2At, reminderID), engine.StepOptions{})
	require.NoError(t, err)

	var foundIA2 bool
	for _, c := range r3.Commands {
		switch v := c.(type) {
		case engine.InvokeAction:
			if v.Name == "remind" {
				foundIA2 = true
			}
		case engine.ScheduleTimer, engine.CancelTimer, engine.UpdateTask, engine.CompleteInstance:
			t.Errorf("unexpected command %T after second reminder fire (engine must NOT reschedule): %v", c, c)
		}
	}
	require.True(t, foundIA2, "InvokeAction('remind') not found after second reminder; got: %v", r3.Commands)

	// ---- Step 4: complete the task → CancelTimer for outstanding reminder ----
	actor := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	completeAt := startAt.Add(3 * time.Hour / 2) // 1.5h into the process, before deadline
	r4, err := engine.Step(def, r3.State,
		engine.NewHumanCompleted(completeAt, "i1-h1", nil, actor), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r4.State.Status, "instance should complete on HumanCompleted")

	// HumanCompleted must cancel the outstanding reminder timer AND the deadline timer.
	var foundCancelReminder, foundCancelDeadline bool
	for _, c := range r4.Commands {
		if ct, ok := c.(engine.CancelTimer); ok {
			if ct.TimerID == reminderID {
				foundCancelReminder = true
			}
			if ct.TimerID == deadlineTimerID {
				foundCancelDeadline = true
			}
		}
	}
	assert.True(t, foundCancelReminder, "HumanCompleted must cancel the outstanding reminder timer (id=%s); got: %v", reminderID, r4.Commands)
	assert.True(t, foundCancelDeadline, "HumanCompleted must cancel the deadline timer (id=%s); got: %v", deadlineTimerID, r4.Commands)

	// ---- Step 5: late reminder fires after task completed → clean no-op ----
	r5, err := engine.Step(def, r4.State,
		engine.NewTimerFired(startAt.Add(3*time.Hour), reminderID), engine.StepOptions{})
	require.NoError(t, err, "late reminder TimerFired must not error")
	assert.Empty(t, r5.Commands, "late reminder TimerFired must emit no commands; got: %v", r5.Commands)
	assert.Equal(t, engine.StatusCompleted, r5.State.Status, "instance must remain completed after late reminder")
}

// TestInWaitReminderNoActionEmitsNothingOnFire verifies that when ReminderEvery is
// set but ReminderAction is empty, a reminder fire emits NO InvokeAction (no action
// configured) AND — after Plan 2 Task 1 — NO new ScheduleTimer (recurrence is native
// to the scheduler; the engine armed the recurring timer once at entry and does not
// reschedule per fire). This proves the action field is genuinely optional and that
// the engine no longer owns re-arming.
func TestInWaitReminderNoActionEmitsNothingOnFire(t *testing.T) {
	// Use a definition with ReminderEvery but no ReminderAction.
	def := &model.ProcessDefinition{
		ID:      "p-reminder-noaction",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("userTask", []string{"manager"}, activity.WithWaitReminder(schedule.EveryExpr(`"1h"`), "")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "userTask"},
			{ID: "f2", Source: "userTask", Target: "end"},
		},
	}

	startAt := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	// Start → parked at userTask.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(startAt, nil), engine.StepOptions{})
	require.NoError(t, err)

	// The entry command must arm a single recurring in-wait reminder.
	var reminderID string
	for _, c := range r1.Commands {
		if st, ok := c.(engine.ScheduleTimer); ok && st.Kind == engine.TimerInWait {
			reminderID = st.TimerID
			assert.True(t, st.Trigger.Recurring(), "reminder trigger must be recurring")
		}
	}
	require.NotEmpty(t, reminderID, "ScheduleTimer(InWait) must be emitted on entry")

	// Fire the reminder.
	fire1At := startAt.Add(time.Hour)
	r2, err := engine.Step(def, r1.State,
		engine.NewTimerFired(fire1At, reminderID), engine.StepOptions{})
	require.NoError(t, err)

	// A no-action reminder fire must emit NOTHING: no InvokeAction (no action) and
	// no ScheduleTimer (the scheduler re-delivers natively, engine does not re-arm).
	assert.Empty(t, r2.Commands,
		"no-action reminder fire must emit no commands (no action, no reschedule); got: %v", r2.Commands)
}

// TestInWaitReminderCancelledByDeadline verifies that when the deadline fires on a user task
// with a reminder, the deadline breach cancels the outstanding reminder timer.
func TestInWaitReminderCancelledByDeadline(t *testing.T) {
	def := reminderDef()
	startAt := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	// Start → parked at userTask.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(startAt, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Extract timer ids.
	var deadlineTimerID, reminderTimerID string
	for _, c := range r1.Commands {
		if st, ok := c.(engine.ScheduleTimer); ok {
			switch st.Kind {
			case engine.TimerDeadline:
				deadlineTimerID = st.TimerID
			case engine.TimerInWait:
				reminderTimerID = st.TimerID
			}
		}
	}
	require.NotEmpty(t, deadlineTimerID, "deadline timer must be scheduled")
	require.NotEmpty(t, reminderTimerID, "reminder timer must be scheduled")

	// Fire the first reminder. Recurrence is native now, so the SAME reminder id
	// stays outstanding (the engine does not re-schedule a new one). Fire the
	// deadline next to confirm it cancels that outstanding reminder timer.
	fire1At := startAt.Add(time.Hour)
	r2, err := engine.Step(def, r1.State,
		engine.NewTimerFired(fire1At, reminderTimerID), engine.StepOptions{})
	require.NoError(t, err)

	// The engine must NOT re-schedule a reminder on fire (native recurrence).
	for _, c := range r2.Commands {
		if st, ok := c.(engine.ScheduleTimer); ok && st.Kind == engine.TimerInWait {
			t.Errorf("engine must not re-schedule a reminder on fire; got: %v", st)
		}
	}

	// deadline fires while the (single) reminder is still outstanding.
	deadlineFireAt := startAt.Add(3 * time.Hour)
	r3, err := engine.Step(def, r2.State,
		engine.NewTimerFired(deadlineFireAt, deadlineTimerID), engine.StepOptions{})
	require.NoError(t, err)

	// deadline breach must cancel the outstanding reminder timer.
	var foundCancelReminder bool
	for _, c := range r3.Commands {
		if ct, ok := c.(engine.CancelTimer); ok && ct.TimerID == reminderTimerID {
			foundCancelReminder = true
		}
	}
	assert.True(t, foundCancelReminder, "deadline breach must emit CancelTimer for the outstanding reminder (id=%s); got: %v", reminderTimerID, r3.Commands)

	// Instance completes via the escalate path.
	assert.Equal(t, engine.StatusCompleted, r3.State.Status)
}

// TestActionFailedCancelsOutstandingTimers verifies that when ActionFailed
// transitions the instance to StatusFailed, every outstanding timer record in
// s.Timers receives a CancelTimer command and s.Timers is cleared.
//
// Scenario: a process has a user-task node (with a deadline timer outstanding) AND
// a service-task node awaiting a command.  ActionFailed on the service-task
// command must emit FailInstance AND a CancelTimer for the deadline timer that was
// guarding the user task.
func TestActionFailedCancelsOutstandingTimers(t *testing.T) {
	// parallelDeadlineDef returns a process that:
	//   Start → parallelFork → userTask (deadline 3h, deadlineFlow:"esc", deadlineAction:"notify")
	//                       → serviceTask("work")
	//   Both userTask and serviceTask converge at a join → end
	//   We only need the fork: two tokens are live at the same time.
	//
	// For simplicity, use a definition where both branches lead to an end event
	// directly (no join) — the important invariant is that two tokens exist
	// simultaneously: one at userTask (WaitingCommand on taskToken, deadline timer in
	// s.Timers) and one at serviceTask (WaitingCommand on commandID).
	def := &model.ProcessDefinition{
		ID:      "p-parallel-deadline",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewUserTask("userTask", []string{"manager"}, activity.WithDeadline(schedule.AfterExpr(`"3h"`), "esc", "notify")),
			activity.NewServiceTask("svcTask", activity.WithActionName("work")),
			event.NewEnd("endA"),
			event.NewEnd("endB"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fork"},
			{ID: "f2", Source: "fork", Target: "userTask"},
			{ID: "f3", Source: "fork", Target: "svcTask"},
			{ID: "f4", Source: "userTask", Target: "endA"},
			{ID: "esc", Source: "userTask", Target: "endA"},
			{ID: "f5", Source: "svcTask", Target: "endB"},
		},
	}

	startAt := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	// Start: drives into parallelFork → both branches resolve.
	// userTask parks with AwaitHuman + ScheduleTimer(Deadline).
	// svcTask parks with InvokeAction.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(startAt, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r1.State.Status)

	// Locate the deadline timer ID and the service-task command ID.
	var deadlineTimerID string
	var svcCmdID string
	for _, c := range r1.Commands {
		switch v := c.(type) {
		case engine.ScheduleTimer:
			if v.Kind == engine.TimerDeadline {
				deadlineTimerID = v.TimerID
			}
		case engine.InvokeAction:
			svcCmdID = v.CommandID
		}
	}
	require.NotEmpty(t, deadlineTimerID, "deadline timer must be scheduled after Start")
	require.NotEmpty(t, svcCmdID, "InvokeAction for service task must be emitted after Start")

	// Verify the deadline timer is in s.Timers.
	require.Len(t, r1.State.Timers, 1, "exactly one timer record (deadline) must be in s.Timers")
	assert.Equal(t, deadlineTimerID, r1.State.Timers[0].TimerID)

	// Now feed ActionFailed for the service-task command.
	failAt := startAt.Add(30 * time.Minute)
	r2, err := engine.Step(def, r1.State,
		engine.NewActionFailed(failAt, svcCmdID, "work exploded", false), engine.StepOptions{})
	require.NoError(t, err)

	// Instance must be StatusFailed.
	assert.Equal(t, engine.StatusFailed, r2.State.Status)
	assert.NotNil(t, r2.State.EndedAt)

	// Commands must include FailInstance AND CancelTimer for the outstanding deadline timer.
	var foundFail bool
	var foundCancelDeadline bool
	for _, c := range r2.Commands {
		switch v := c.(type) {
		case engine.FailInstance:
			foundFail = true
		case engine.CancelTimer:
			if v.TimerID == deadlineTimerID {
				foundCancelDeadline = true
			}
		}
	}
	assert.True(t, foundFail, "ActionFailed must emit FailInstance; got: %v", r2.Commands)
	assert.True(t, foundCancelDeadline, "ActionFailed must emit CancelTimer for outstanding deadline timer (id=%s); got: %v", deadlineTimerID, r2.Commands)

	// s.Timers must be cleared after ActionFailed.
	assert.Empty(t, r2.State.Timers, "s.Timers must be empty after ActionFailed cancels all outstanding timers")
}
