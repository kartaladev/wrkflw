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

// receiveTaskDef: start → recv(msg "m") → end.
func receiveTaskDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-recv", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewReceiveTask("recv", "m"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "recv"},
			{ID: "f2", Source: "recv", Target: "end"},
		},
	}
}

// receiveTaskBoundaryDef: start → recv(msg "m") → end ; boundary on recv → escalate → end2.
func receiveTaskBoundaryDef(boundary model.Node) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-recv-bnd", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewReceiveTask("recv", "m"),
			boundary,
			activity.NewServiceTask("escalate", activity.WithTaskAction("esc")),
			event.NewEnd("end"),
			event.NewEnd("end2"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "recv"},
			{ID: "f2", Source: "recv", Target: "end"},
			{ID: "f3", Source: "bnd", Target: "escalate"},
			{ID: "f4", Source: "escalate", Target: "end2"},
		},
	}
}

// TestReceiveTaskResumesOnMessage asserts a ReceiveTask actually awaits its
// message and resumes to completion on delivery (it was previously an
// unimplemented park-only fall-through that no message could resume).
func TestReceiveTaskResumesOnMessage(t *testing.T) {
	def := receiveTaskDef()
	t0 := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.Tokens, 1)
	assert.Equal(t, "recv", r1.State.Tokens[0].NodeID)

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewMessageReceived(t0, "m", "", nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r2.State.Status,
		"ReceiveTask must resume on its message and complete")
}

// TestReceiveTaskBadCorrelationKey asserts that a ReceiveTask whose correlation
// key is an invalid expr expression surfaces a wrapped error on entry rather
// than parking silently.
func TestReceiveTaskBadCorrelationKey(t *testing.T) {
	t0 := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	def := &model.ProcessDefinition{
		ID: "p-recv-bad", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewReceiveTask("recv", "m", activity.WithCorrelationKey("this is not valid expr ++")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "recv"},
			{ID: "f2", Source: "recv", Target: "end"},
		},
	}

	_, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "correlation key")
}

// TestReceiveTaskBoundaryInterruptsHost asserts boundaries attached to a
// ReceiveTask host are armed and, when they fire before the awaited message,
// interrupt the host and route onto the boundary flow.
func TestReceiveTaskBoundaryInterruptsHost(t *testing.T) {
	t0 := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name     string
		boundary model.Node
		fire     func(r1 engine.StepResult) engine.Trigger
	}
	cases := []testCase{
		{
			name:     "timer boundary",
			boundary: event.NewBoundary("bnd", "recv", event.WithBoundaryTimer(schedule.AfterExpr(`"60s"`))),
			fire: func(r1 engine.StepResult) engine.Trigger {
				for _, c := range r1.Commands {
					if st, ok := c.(engine.ScheduleTimer); ok {
						return engine.NewTimerFired(t0, st.TimerID)
					}
				}
				return nil
			},
		},
		{
			name:     "message boundary",
			boundary: event.NewBoundary("bnd", "recv", event.WithMessageCorrelator("cancel", "")),
			fire: func(engine.StepResult) engine.Trigger {
				return engine.NewMessageReceived(t0, "cancel", "", nil)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := receiveTaskBoundaryDef(tc.boundary)
			r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
				engine.NewStartInstance(t0, nil), engine.StepOptions{})
			require.NoError(t, err)
			require.Len(t, r1.State.Boundaries, 1, "boundary must be armed on the ReceiveTask host")

			trg := tc.fire(r1)
			require.NotNil(t, trg, "fire trigger must be derivable (boundary armed)")
			r2, err := engine.Step(t.Context(), def, r1.State, trg, engine.StepOptions{})
			require.NoError(t, err)

			require.Len(t, r2.State.Tokens, 1, "interrupting boundary: host consumed, one token at escalate")
			assert.Equal(t, "escalate", r2.State.Tokens[0].NodeID)
			assert.Empty(t, r2.State.Boundaries, "boundary arms cleared after interrupting fire")
		})
	}
}

// TestReceiveTaskMessageResumeDisarmsBoundary asserts that when the awaited
// message arrives BEFORE the boundary, the ReceiveTask resumes normally AND the
// boundary is disarmed (CancelTimer emitted, no leftover arm).
func TestReceiveTaskMessageResumeDisarmsBoundary(t *testing.T) {
	t0 := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	def := receiveTaskBoundaryDef(event.NewBoundary("bnd", "recv", event.WithBoundaryTimer(schedule.AfterExpr(`"60s"`))))

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.Boundaries, 1)
	var timerID string
	for _, c := range r1.Commands {
		if st, ok := c.(engine.ScheduleTimer); ok {
			timerID = st.TimerID
		}
	}
	require.NotEmpty(t, timerID, "ReceiveTask timer boundary must arm a timer")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewMessageReceived(t0, "m", "", nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Empty(t, r2.State.Boundaries, "boundary arm must be removed when the ReceiveTask resumes")
	cancelled := false
	for _, c := range r2.Commands {
		if ct, ok := c.(engine.CancelTimer); ok && ct.TimerID == timerID {
			cancelled = true
		}
	}
	assert.True(t, cancelled, "the boundary timer must be cancelled when the host resumes on its message")
	assert.Equal(t, engine.StatusCompleted, r2.State.Status)
}
