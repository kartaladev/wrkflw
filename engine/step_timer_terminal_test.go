package engine

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
)

// timerBoundaryTerminalDef is a user task carrying an interrupting timer
// boundary that routes to a service task, so a fired arm produces observable
// commands.
func timerBoundaryTerminalDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-timer-terminal", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("work"),
			event.NewBoundary("bnd", "work", event.WithBoundaryTimer(schedule.Every(time.Hour))),
			activity.NewServiceTask("esc", activity.WithTaskAction("esc-action")),
			event.NewEnd("end"),
			event.NewEnd("end2"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "work"},
			{ID: "f2", Source: "work", Target: "end"},
			{ID: "f3", Source: "bnd", Target: "esc"},
			{ID: "f4", Source: "esc", Target: "end2"},
		},
	}
}

// TestHandleTimerFired_TerminalInstanceNoOps verifies that a TimerFired against
// an already-terminal instance is a clean no-op. An unhandled error can fail an
// instance without sweeping sibling boundary arms, so a late timer for such an
// arm must not fire the boundary (and its escalation action) on a terminal
// instance.
func TestHandleTimerFired_TerminalInstanceNoOps(t *testing.T) {
	def := timerBoundaryTerminalDef()
	s := InstanceState{
		InstanceID: "i1",
		Status:     StatusFailed,
		Boundaries: []boundaryArm{
			{HostToken: "h1", HostNode: "work", BoundaryNode: "bnd", Flow: "f3", triggerMatch: triggerMatch{TimerID: "bt1"}},
		},
	}

	res, err := Step(t.Context(), def, s, NewTimerFired(time.Unix(1, 0), "bt1"), StepOptions{})
	require.NoError(t, err)
	assert.Empty(t, res.Commands, "a terminal instance must not fire the boundary")
	assert.Equal(t, StatusFailed, res.State.Status, "status unchanged")
	assert.Len(t, res.State.Boundaries, 1, "arm left intact")
}
