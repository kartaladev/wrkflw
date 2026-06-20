package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// timerDef returns a linear definition:
//
//	Start → TimerCatch("1h") → ServiceTask(notify) → End
func timerDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-timer", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "wait1h", Kind: model.KindIntermediateCatchEvent, TimerDuration: `"1h"`},
			{ID: "notify", Kind: model.KindServiceTask, Action: "send-notification"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
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
	assert.Equal(t, fireAt, st.FireAt)

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

// slaDef returns a definition with a user task that has a 3h SLA:
//
//	Start → userTask(SLADuration:"3h", SLAFlow:"escalate", SLAAction:"notify") → normalEnd
//	userTask → (escalate flow) → escalateNode
//
// "escalate" is the flow id from userTask to escalateNode (the alternative end event).
func slaDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-sla", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{
				ID:             "userTask",
				Kind:           model.KindUserTask,
				CandidateRoles: []string{"manager"},
				SLADuration:    `"3h"`,
				SLAFlow:        "escalate",
				SLAAction:      "notify",
			},
			{ID: "normalEnd", Kind: model.KindEndEvent},
			{ID: "escalateNode", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "userTask"},
			{ID: "f2", Source: "userTask", Target: "normalEnd"},
			{ID: "escalate", Source: "userTask", Target: "escalateNode"},
		},
	}
}

// TestUserTaskSLABreachTakesAlternativePath verifies the SLA breach path:
//  1. Entering the user-task node emits AwaitHuman AND ScheduleTimer(Kind=TimerSLA, FireAt=entry+3h).
//  2. The HumanTask.DueAt is set to FireAt.
//  3. Without completing the task, feeding the SLA TimerFired:
//     - emits InvokeAction("notify"),
//     - moves the token to the escalate node (CompleteInstance since it's an EndEvent),
//     - marks the task Cancelled (emits UpdateTask).
func TestUserTaskSLABreachTakesAlternativePath(t *testing.T) {
	def := slaDef()
	startAt := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	fireAt := startAt.Add(3 * time.Hour)

	// ---- Step 1: Start → parked at userTask ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(startAt, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Commands: AwaitHuman + ScheduleTimer (SLA).
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

	// SLA timer properties.
	assert.Equal(t, engine.TimerSLA, st.Kind)
	assert.Equal(t, fireAt, st.FireAt)
	assert.NotEmpty(t, st.TimerID)
	slaTimerID := st.TimerID

	// Token parked on the task (AwaitCommand == TaskToken).
	require.Len(t, r1.State.Tokens, 1)
	tok := r1.State.Tokens[0]
	assert.Equal(t, "userTask", tok.NodeID)
	assert.Equal(t, engine.TokenWaitingCommand, tok.State)
	assert.Equal(t, "i1-h1", tok.AwaitCommand)

	// Token referenced in ScheduleTimer matches the parked token.
	assert.Equal(t, tok.ID, st.Token)

	// HumanTask.DueAt is set to the SLA fire time.
	require.Len(t, r1.State.Tasks, 1)
	ht := r1.State.Tasks[0]
	require.NotNil(t, ht.DueAt, "HumanTask.DueAt must be set when SLADuration is defined")
	assert.Equal(t, fireAt, *ht.DueAt)
	assert.Equal(t, humantask.Unclaimed, ht.State)

	// Instance still running.
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// ---- Step 2: SLA fires (task NOT completed) → breach ----
	r2, err := engine.Step(def, r1.State,
		engine.NewTimerFired(fireAt, slaTimerID), engine.StepOptions{})
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

	// InvokeAction invokes the SLA action.
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

// TestUserTaskCompletedBeforeSLAIgnoresTimer verifies that if the task is
// completed before the SLA fires, the late SLA TimerFired is a clean no-op:
// no commands, no error, instance already advanced past the user task.
//
// Fix B: also asserts that the HumanCompleted step emitted a CancelTimer for
// the SLA timer, proving cancellation on task completion, not just late-fire no-op.
func TestUserTaskCompletedBeforeSLAIgnoresTimer(t *testing.T) {
	def := slaDef()
	startAt := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	fireAt := startAt.Add(3 * time.Hour)

	// Bring the instance to the parked user-task state.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(startAt, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Extract the SLA timer ID.
	var slaTimerID string
	for _, c := range r1.Commands {
		if st, ok := c.(engine.ScheduleTimer); ok && st.Kind == engine.TimerSLA {
			slaTimerID = st.TimerID
		}
	}
	require.NotEmpty(t, slaTimerID, "expected SLA timer to be scheduled")

	// Complete the task BEFORE the SLA fires.
	actor := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	completeAt := startAt.Add(time.Hour) // well before the 3h SLA
	r2, err := engine.Step(def, r1.State,
		engine.NewHumanCompleted(completeAt, "i1-h1", nil, actor), engine.StepOptions{})
	require.NoError(t, err)
	// Instance must have completed via the normal end path.
	assert.Equal(t, engine.StatusCompleted, r2.State.Status)

	// Fix B: HumanCompleted must emit a CancelTimer for the SLA timer, proving the
	// SLA is actively cancelled on task completion (not just a late-fire no-op).
	var foundCancel bool
	for _, c := range r2.Commands {
		if ct, ok := c.(engine.CancelTimer); ok && ct.TimerID == slaTimerID {
			foundCancel = true
		}
	}
	assert.True(t, foundCancel, "HumanCompleted must emit CancelTimer for the SLA timer (id=%s); got: %v", slaTimerID, r2.Commands)

	// Now the SLA fires late.
	r3, err := engine.Step(def, r2.State,
		engine.NewTimerFired(fireAt, slaTimerID), engine.StepOptions{})
	require.NoError(t, err, "late SLA TimerFired must not error")
	assert.Empty(t, r3.Commands, "late SLA TimerFired must emit no commands")
	// State unchanged (already completed).
	assert.Equal(t, engine.StatusCompleted, r3.State.Status)
}

// reminderDef returns a definition with a user task that has both a reminder
// (ReminderEvery:"1h", ReminderAction:"remind") and an SLA (SLADuration:"3h"):
//
//	Start → userTask → normalEnd
//	userTask → (escalate flow) → escalateEnd
func reminderDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-reminder", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{
				ID:             "userTask",
				Kind:           model.KindUserTask,
				CandidateRoles: []string{"manager"},
				SLADuration:    `"3h"`,
				SLAFlow:        "escalate",
				SLAAction:      "notify",
				ReminderEvery:  `"1h"`,
				ReminderAction: "remind",
			},
			{ID: "normalEnd", Kind: model.KindEndEvent},
			{ID: "escalateNode", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "userTask"},
			{ID: "f2", Source: "userTask", Target: "normalEnd"},
			{ID: "escalate", Source: "userTask", Target: "escalateNode"},
		},
	}
}

// TestInWaitReminderRepeatsUntilCompletion verifies the full reminder lifecycle:
//  1. Entering a user task with ReminderEvery emits AwaitHuman + ScheduleTimer(SLA) + ScheduleTimer(InWait).
//  2. Each TimerFired for the in-wait reminder emits InvokeAction("remind") + a fresh ScheduleTimer(InWait)
//     with a new timer id and FireAt == firedAt+1h.
//  3. Token does not move; task remains Unclaimed/Claimed.
//  4. Fire the reminder twice to confirm repeating with distinct timer ids.
//  5. HumanCompleted emits CancelTimer for the outstanding reminder timer.
//  6. A late reminder TimerFired after completion is a clean no-op.
func TestInWaitReminderRepeatsUntilCompletion(t *testing.T) {
	def := reminderDef()
	startAt := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	// ---- Step 1: Start → parked at userTask ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(startAt, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Expect 3 commands: AwaitHuman + ScheduleTimer(SLA) + ScheduleTimer(InWait).
	require.Len(t, r1.Commands, 3, "expected AwaitHuman + ScheduleTimer(SLA) + ScheduleTimer(InWait) on user-task entry; got: %v", r1.Commands)

	var ah engine.AwaitHuman
	var slaST, reminderST engine.ScheduleTimer
	var foundAH bool
	for _, c := range r1.Commands {
		switch v := c.(type) {
		case engine.AwaitHuman:
			ah = v
			foundAH = true
		case engine.ScheduleTimer:
			switch v.Kind {
			case engine.TimerSLA:
				slaST = v
			case engine.TimerInWait:
				reminderST = v
			}
		}
	}
	require.True(t, foundAH, "AwaitHuman not found in entry commands")
	require.NotEmpty(t, slaST.TimerID, "SLA ScheduleTimer not found in entry commands")
	require.NotEmpty(t, reminderST.TimerID, "InWait ScheduleTimer not found in entry commands")

	// TaskToken is deterministic.
	assert.Equal(t, "i1-h1", ah.TaskToken)

	// SLA: 3h from start.
	assert.Equal(t, engine.TimerSLA, slaST.Kind)
	assert.Equal(t, startAt.Add(3*time.Hour), slaST.FireAt)
	slaTimerID := slaST.TimerID

	// Reminder: 1h from start.
	assert.Equal(t, engine.TimerInWait, reminderST.Kind)
	assert.Equal(t, startAt.Add(time.Hour), reminderST.FireAt, "first reminder should fire at start+1h")

	// Timer ids are distinct.
	assert.NotEqual(t, slaTimerID, reminderST.TimerID, "SLA and reminder timer ids must differ")

	// Token parked at the user task (not moved).
	require.Len(t, r1.State.Tokens, 1)
	tok := r1.State.Tokens[0]
	assert.Equal(t, "userTask", tok.NodeID)
	assert.Equal(t, engine.TokenWaitingCommand, tok.State)
	assert.Equal(t, "i1-h1", tok.AwaitCommand, "token must remain parked on the task token")

	// Task state is Unclaimed.
	require.Len(t, r1.State.Tasks, 1)
	assert.Equal(t, humantask.Unclaimed, r1.State.Tasks[0].State)

	// ---- Step 2: first reminder fires ----
	reminder1ID := reminderST.TimerID
	fire1At := startAt.Add(time.Hour)
	r2, err := engine.Step(def, r1.State,
		engine.NewTimerFired(fire1At, reminder1ID), engine.StepOptions{})
	require.NoError(t, err)

	// Expect: InvokeAction("remind") + ScheduleTimer(InWait, FireAt=fire1At+1h).
	// No CancelTimer, no UpdateTask, no CompleteInstance.
	var ia1 engine.InvokeAction
	var nextST1 engine.ScheduleTimer
	var foundIA1, foundNextST1 bool
	for _, c := range r2.Commands {
		switch v := c.(type) {
		case engine.InvokeAction:
			ia1 = v
			foundIA1 = true
		case engine.ScheduleTimer:
			if v.Kind == engine.TimerInWait {
				nextST1 = v
				foundNextST1 = true
			}
		case engine.CancelTimer, engine.UpdateTask, engine.CompleteInstance:
			t.Errorf("unexpected command %T after first reminder fire: %v", c, c)
		}
	}
	require.True(t, foundIA1, "InvokeAction not found after first reminder; got: %v", r2.Commands)
	assert.Equal(t, "remind", ia1.Name, "InvokeAction name must be the ReminderAction")
	require.True(t, foundNextST1, "re-schedule ScheduleTimer(InWait) not found after first reminder; got: %v", r2.Commands)
	assert.Equal(t, engine.TimerInWait, nextST1.Kind)
	assert.Equal(t, fire1At.Add(time.Hour), nextST1.FireAt, "next reminder must fire at firedAt+1h")
	assert.NotEqual(t, reminder1ID, nextST1.TimerID, "re-scheduled reminder must have a new timer id")

	// Token must NOT move — still parked at userTask.
	require.Len(t, r2.State.Tokens, 1)
	assert.Equal(t, "userTask", r2.State.Tokens[0].NodeID)
	assert.Equal(t, engine.TokenWaitingCommand, r2.State.Tokens[0].State)
	assert.Equal(t, "i1-h1", r2.State.Tokens[0].AwaitCommand)

	// Task still Unclaimed.
	require.Len(t, r2.State.Tasks, 1)
	assert.Equal(t, humantask.Unclaimed, r2.State.Tasks[0].State)

	// ---- Step 3: second reminder fires (proves repeating with distinct timer ids) ----
	reminder2ID := nextST1.TimerID
	fire2At := fire1At.Add(time.Hour)
	r3, err := engine.Step(def, r2.State,
		engine.NewTimerFired(fire2At, reminder2ID), engine.StepOptions{})
	require.NoError(t, err)

	var nextST2 engine.ScheduleTimer
	var foundIA2, foundNextST2 bool
	for _, c := range r3.Commands {
		switch v := c.(type) {
		case engine.InvokeAction:
			if v.Name == "remind" {
				foundIA2 = true
			}
		case engine.ScheduleTimer:
			if v.Kind == engine.TimerInWait {
				nextST2 = v
				foundNextST2 = true
			}
		case engine.CancelTimer, engine.UpdateTask, engine.CompleteInstance:
			t.Errorf("unexpected command %T after second reminder fire: %v", c, c)
		}
	}
	require.True(t, foundIA2, "InvokeAction('remind') not found after second reminder; got: %v", r3.Commands)
	require.True(t, foundNextST2, "re-schedule ScheduleTimer(InWait) not found after second reminder; got: %v", r3.Commands)
	assert.Equal(t, fire2At.Add(time.Hour), nextST2.FireAt, "third reminder must fire at fire2At+1h")
	assert.NotEqual(t, reminder1ID, nextST2.TimerID, "third reminder id must differ from first")
	assert.NotEqual(t, reminder2ID, nextST2.TimerID, "third reminder id must differ from second")

	// ---- Step 4: complete the task → CancelTimer for outstanding reminder ----
	reminder3ID := nextST2.TimerID
	actor := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	completeAt := startAt.Add(3 * time.Hour / 2) // 1.5h into the process, before SLA
	r4, err := engine.Step(def, r3.State,
		engine.NewHumanCompleted(completeAt, "i1-h1", nil, actor), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r4.State.Status, "instance should complete on HumanCompleted")

	// HumanCompleted must cancel the outstanding reminder timer AND the SLA timer.
	var foundCancelReminder, foundCancelSLA bool
	for _, c := range r4.Commands {
		if ct, ok := c.(engine.CancelTimer); ok {
			if ct.TimerID == reminder3ID {
				foundCancelReminder = true
			}
			if ct.TimerID == slaTimerID {
				foundCancelSLA = true
			}
		}
	}
	assert.True(t, foundCancelReminder, "HumanCompleted must cancel the outstanding reminder timer (id=%s); got: %v", reminder3ID, r4.Commands)
	assert.True(t, foundCancelSLA, "HumanCompleted must cancel the SLA timer (id=%s); got: %v", slaTimerID, r4.Commands)

	// ---- Step 5: late reminder fires after task completed → clean no-op ----
	r5, err := engine.Step(def, r4.State,
		engine.NewTimerFired(startAt.Add(3*time.Hour), reminder3ID), engine.StepOptions{})
	require.NoError(t, err, "late reminder TimerFired must not error")
	assert.Empty(t, r5.Commands, "late reminder TimerFired must emit no commands; got: %v", r5.Commands)
	assert.Equal(t, engine.StatusCompleted, r5.State.Status, "instance must remain completed after late reminder")
}

// TestInWaitReminderCancelledBySLA verifies that when the SLA fires on a user task
// with a reminder, the SLA breach cancels the outstanding reminder timer.
func TestInWaitReminderCancelledBySLA(t *testing.T) {
	def := reminderDef()
	startAt := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	// Start → parked at userTask.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(startAt, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Extract timer ids.
	var slaTimerID, reminderTimerID string
	for _, c := range r1.Commands {
		if st, ok := c.(engine.ScheduleTimer); ok {
			switch st.Kind {
			case engine.TimerSLA:
				slaTimerID = st.TimerID
			case engine.TimerInWait:
				reminderTimerID = st.TimerID
			}
		}
	}
	require.NotEmpty(t, slaTimerID, "SLA timer must be scheduled")
	require.NotEmpty(t, reminderTimerID, "reminder timer must be scheduled")

	// Fire the first reminder so there's an outstanding re-scheduled reminder,
	// then fire the SLA to confirm it cancels the re-scheduled one.
	fire1At := startAt.Add(time.Hour)
	r2, err := engine.Step(def, r1.State,
		engine.NewTimerFired(fire1At, reminderTimerID), engine.StepOptions{})
	require.NoError(t, err)

	// Get the re-scheduled reminder id.
	var reminder2ID string
	for _, c := range r2.Commands {
		if st, ok := c.(engine.ScheduleTimer); ok && st.Kind == engine.TimerInWait {
			reminder2ID = st.TimerID
		}
	}
	require.NotEmpty(t, reminder2ID, "second reminder must be scheduled after first fires")

	// SLA fires while the second reminder is outstanding.
	slaFireAt := startAt.Add(3 * time.Hour)
	r3, err := engine.Step(def, r2.State,
		engine.NewTimerFired(slaFireAt, slaTimerID), engine.StepOptions{})
	require.NoError(t, err)

	// SLA breach must cancel the outstanding reminder timer.
	var foundCancelReminder bool
	for _, c := range r3.Commands {
		if ct, ok := c.(engine.CancelTimer); ok && ct.TimerID == reminder2ID {
			foundCancelReminder = true
		}
	}
	assert.True(t, foundCancelReminder, "SLA breach must emit CancelTimer for the outstanding reminder (id=%s); got: %v", reminder2ID, r3.Commands)

	// Instance completes via the escalate path.
	assert.Equal(t, engine.StatusCompleted, r3.State.Status)
}
