package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
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
