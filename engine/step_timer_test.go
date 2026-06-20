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
//	userTask → escalate → escalateEnd
//
// "escalate" is the flow id from userTask to the escalate node.
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

	// Now the SLA fires late.
	r3, err := engine.Step(def, r2.State,
		engine.NewTimerFired(fireAt, slaTimerID), engine.StepOptions{})
	require.NoError(t, err, "late SLA TimerFired must not error")
	assert.Empty(t, r3.Commands, "late SLA TimerFired must emit no commands")
	// State unchanged (already completed).
	assert.Equal(t, engine.StatusCompleted, r3.State.Status)
}
