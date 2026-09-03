package engine_test

// step_cancel_dropped_test.go — the DROPPED cancel site
// reports ErrCancelNotApplicable, while the DEFERRED site keeps returning nil.
//
// The two sites are semantically different — "will not terminate at all" vs
// "will terminate later" — and must not be collapsed. Measured on `main` before
// the sentinel: a cancel racing an admin partial rollback returned err=<nil> with
// zero commands, the state byte-identical to before it, and the "cancelled"
// instance then resumed running.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// droppedCancelSagaDef is the smallest definition that can hold an admin PARTIAL
// rollback in flight: two compensable service tasks, then a receive task that
// parks the instance.
//
//	start → a (undoA) → b (undoB) → wait (receive "go") → end
func droppedCancelSagaDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "dropped-cancel-saga", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("a", activity.WithTaskAction("doA"), activity.WithCompensateAction("undoA")),
			activity.NewServiceTask("b", activity.WithTaskAction("doB"), activity.WithCompensateAction("undoB")),
			activity.NewReceiveTask("wait", "go"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "a"},
			{ID: "f2", Source: "a", Target: "b"},
			{ID: "f3", Source: "b", Target: "wait"},
			{ID: "f4", Source: "wait", Target: "end"},
		},
	}
}

// partialRollbackInFlight drives droppedCancelSagaDef to an admin PARTIAL
// rollback (cursor.ToNode set) whose compensation action is still out with a
// worker. This is the state whose cancel is DROPPED.
func partialRollbackInFlight(t *testing.T) (engine.InstanceState, *model.ProcessDefinition) {
	t.Helper()

	def := droppedCancelSagaDef()
	at := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)

	res, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "dc1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	for i, name := range []string{"doA", "doB"} {
		cmd := invokeActionNamed(res.Commands, name)
		require.NotNil(t, cmd, "fixture: %q must be dispatched", name)
		res, err = engine.Step(t.Context(), def, res.State,
			engine.NewActionCompleted(at.Add(time.Duration(i+1)*time.Second), cmd.CommandID, nil), engine.StepOptions{})
		require.NoError(t, err)
	}
	require.Equal(t, engine.StatusRunning, res.State.Status, "fixture: the saga must park at the receive task")

	res, err = engine.Step(t.Context(), def, res.State,
		engine.NewCompensateRequested(at.Add(time.Minute), "a"), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, res.State.Status)
	require.NotEmpty(t, res.State.Compensating.ToNode,
		"fixture: an admin PARTIAL rollback must set the cursor's ToNode — a walk without it is a different site")
	require.NotEmpty(t, res.State.Compensating.ActiveCmdID,
		"fixture: the walk must be IN FLIGHT, or handleCancelRequested never reaches the dropped site")
	require.Empty(t, res.State.Compensating.ResumeNode, "fixture: a partial rollback must not look like a resuming walk")
	require.Empty(t, res.State.Compensating.ReverseNode, "fixture: a partial rollback must not look like a reverse walk")

	return res.State, def
}

// terminalCancelWalkInFlight drives droppedCancelSagaDef to a TERMINAL cancel
// walk (walkAdmin: none of ResumeNode/ToNode/ReverseNode set) with its first
// compensation action still out with a worker.
//
// It shares the dropped site with a partial rollback and must NOT share its
// answer: this walk ends the instance, so a redundant cancel is idempotently
// satisfied (post-acceptance idempotent re-cancel).
func terminalCancelWalkInFlight(t *testing.T) (engine.InstanceState, *model.ProcessDefinition) {
	t.Helper()

	def := droppedCancelSagaDef()
	at := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)

	res, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "dc2"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	for i, name := range []string{"doA", "doB"} {
		cmd := invokeActionNamed(res.Commands, name)
		require.NotNil(t, cmd, "fixture: %q must be dispatched", name)
		res, err = engine.Step(t.Context(), def, res.State,
			engine.NewActionCompleted(at.Add(time.Duration(i+1)*time.Second), cmd.CommandID, nil), engine.StepOptions{})
		require.NoError(t, err)
	}

	res, err = engine.Step(t.Context(), def, res.State,
		engine.NewCancelRequested(at.Add(time.Minute)), engine.StepOptions{})
	require.NoError(t, err, "fixture: the FIRST cancel starts the terminal walk and must succeed")
	require.Equal(t, engine.StatusCompensating, res.State.Status)
	require.NotEmpty(t, res.State.Compensating.ActiveCmdID,
		"fixture: the walk must be IN FLIGHT, or the second cancel reaches a different site")
	require.Empty(t, res.State.Compensating.ToNode,
		"fixture: a terminal cancel walk must NOT be a partial rollback — that is the other row")

	return res.State, def
}

// TestCancelOnAnInFlightWalkIsTruthful covers the two nil-returning cancel sites
// in one table (the project's table-test rule: same SUT call, differing inputs).
// The rows are the whole point — a single answer for both would be exactly the
// collapse this file forbids.
func TestCancelOnAnInFlightWalkIsTruthful(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		seed   func(t *testing.T) (engine.InstanceState, *model.ProcessDefinition)
		assert func(t *testing.T, before engine.InstanceState, res engine.StepResult, err error)
	}

	cases := []testCase{
		{
			name: "DROPPED against an admin partial rollback reports not-applicable",
			seed: partialRollbackInFlight,
			assert: func(t *testing.T, _ engine.InstanceState, res engine.StepResult, err error) {
				require.ErrorIs(t, err, engine.ErrCancelNotApplicable,
					"a cancel that will never terminate the instance must say so")
				assert.ErrorIs(t, err, engine.ErrInvalidTransition,
					"the sentinel must classify as a wrong-state transition so transports do not 500")
				assert.Empty(t, res.Commands, "the dropped site still does nothing — only the answer changes")
			},
		},
		{
			name: "REDUNDANT re-cancel of a terminal walk still returns nil",
			seed: func(t *testing.T) (engine.InstanceState, *model.ProcessDefinition) {
				st, def := terminalCancelWalkInFlight(t)
				return st, def
			},
			assert: func(t *testing.T, _ engine.InstanceState, res engine.StepResult, err error) {
				require.NoError(t, err,
					"a re-cancel of a walk that ALREADY terminates the instance is idempotent, not inapplicable")
				assert.Empty(t, res.Commands,
					"the redundant cancel must still emit nothing — no second compensation walk")
			},
		},
		{
			name: "DEFERRED behind a resuming walk still returns nil",
			seed: func(t *testing.T) (engine.InstanceState, *model.ProcessDefinition) {
				st := instanceWithArmedTimers(t, false)
				require.Equal(t, engine.StatusCompensating, st.Status)
				require.NotEmpty(t, st.Compensating.ResumeNode,
					"fixture: the walk must RESUME, or the cancel is not deferred")
				require.False(t, st.PendingCancel, "fixture: no cancel has been recorded yet")
				return st, dyingTimerDef()
			},
			assert: func(t *testing.T, _ engine.InstanceState, res engine.StepResult, err error) {
				require.NoError(t, err,
					"the deferred site recorded intent and the instance really will terminate — it must stay nil")
				assert.True(t, res.State.PendingCancel, "the deferred cancel must be recorded")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			before, def := tc.seed(t)
			res, err := engine.Step(t.Context(), def, before,
				engine.NewCancelRequested(time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)), engine.StepOptions{})
			tc.assert(t, before, res, err)
		})
	}
}
