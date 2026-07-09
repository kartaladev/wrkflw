package engine_test

// step_cancel_tasks_test.go — cancelling an instance reconciles the human-task
// projection: every OPEN task (Unclaimed/Claimed) is marked Cancelled and an
// UpdateTask command is emitted so the TaskStore no longer surfaces it in an
// inbox query for a terminated instance.
//
// ADR: 0088
//
// The compensation-first branch is covered by a separate standalone test below
// because its setup shape (a completed compensable node before cancel) diverges
// from the homogeneous "park → cancel" table cases.

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
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
)

// findUpdateTasks returns all UpdateTask commands in cmds.
func findUpdateTasks(cmds []engine.Command) []engine.UpdateTask {
	var out []engine.UpdateTask
	for _, c := range cmds {
		if ut, ok := c.(engine.UpdateTask); ok {
			out = append(out, ut)
		}
	}
	return out
}

// TestCancelReconcilesOpenTasks verifies that CancelRequested marks every open
// human task Cancelled (in both the returned state and via UpdateTask commands),
// and emits none when there is no open human task.
func TestCancelReconcilesOpenTasks(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name string
		// setup drives an instance to the point of cancellation and returns the
		// definition plus the parked state to cancel.
		setup  func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState)
		assert func(t *testing.T, res engine.StepResult)
	}

	// startParked runs NewStartInstance and returns the resulting state.
	startParked := func(t *testing.T, def *model.ProcessDefinition, id string) engine.InstanceState {
		t.Helper()
		r0, err := engine.Step(def, engine.InstanceState{InstanceID: id},
			engine.NewStartInstance(at, nil), engine.StepOptions{})
		require.NoError(t, err)
		return r0.State
	}

	cases := []testCase{
		{
			name: "single open user task is cancelled",
			setup: func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState) {
				def := &model.ProcessDefinition{
					ID: "c-single", Version: 1,
					Nodes: []model.Node{
						event.NewStart("start"),
						activity.NewUserTask("user", []string{"r"}),
						event.NewEnd("end"),
					},
					Flows: []flow.SequenceFlow{
						{ID: "f1", Source: "start", Target: "user"},
						{ID: "f2", Source: "user", Target: "end"},
					},
				}
				st := startParked(t, def, "c-single-1")
				require.Len(t, st.Tasks, 1)
				require.True(t, st.Tasks[0].IsOpen(), "setup: task must be open before cancel")
				return def, st
			},
			assert: func(t *testing.T, res engine.StepResult) {
				require.Equal(t, engine.StatusTerminated, res.State.Status)
				require.Len(t, res.State.Tasks, 1)
				assert.Equal(t, humantask.Cancelled, res.State.Tasks[0].State,
					"open task must be marked Cancelled in the terminated state")

				uts := findUpdateTasks(res.Commands)
				require.Len(t, uts, 1, "exactly one UpdateTask must be emitted for the open task")
				assert.Equal(t, humantask.Cancelled, uts[0].Task.State)
			},
		},
		{
			name: "two parallel open user tasks are both cancelled",
			setup: func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState) {
				def := &model.ProcessDefinition{
					ID: "c-par", Version: 1,
					Nodes: []model.Node{
						event.NewStart("start"),
						gateway.NewParallel("fork"),
						activity.NewUserTask("ua", []string{"r"}),
						activity.NewUserTask("ub", []string{"r"}),
						gateway.NewParallel("join"),
						event.NewEnd("end"),
					},
					Flows: []flow.SequenceFlow{
						{ID: "f0", Source: "start", Target: "fork"},
						{ID: "f1", Source: "fork", Target: "ua"},
						{ID: "f2", Source: "fork", Target: "ub"},
						{ID: "f3", Source: "ua", Target: "join"},
						{ID: "f4", Source: "ub", Target: "join"},
						{ID: "f5", Source: "join", Target: "end"},
					},
				}
				st := startParked(t, def, "c-par-1")
				require.Len(t, st.Tasks, 2, "setup: both parallel user tasks must be parked")
				return def, st
			},
			assert: func(t *testing.T, res engine.StepResult) {
				require.Equal(t, engine.StatusTerminated, res.State.Status)
				uts := findUpdateTasks(res.Commands)
				require.Len(t, uts, 2, "both open tasks must be cancelled")
				for _, ut := range uts {
					assert.Equal(t, humantask.Cancelled, ut.Task.State)
				}
			},
		},
		{
			name: "no human task emits no UpdateTask",
			setup: func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState) {
				def := &model.ProcessDefinition{
					ID: "c-svc", Version: 1,
					Nodes: []model.Node{
						event.NewStart("start"),
						activity.NewServiceTask("svc", activity.WithActionName("work")),
						event.NewEnd("end"),
					},
					Flows: []flow.SequenceFlow{
						{ID: "f1", Source: "start", Target: "svc"},
						{ID: "f2", Source: "svc", Target: "end"},
					},
				}
				st := startParked(t, def, "c-svc-1")
				require.Empty(t, st.Tasks, "setup: a service task creates no human-task record")
				return def, st
			},
			assert: func(t *testing.T, res engine.StepResult) {
				require.Equal(t, engine.StatusTerminated, res.State.Status)
				assert.Empty(t, findUpdateTasks(res.Commands),
					"no open human task ⇒ no UpdateTask")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def, st := tc.setup(t)
			res, err := engine.Step(def, st, engine.NewCancelRequested(at.Add(time.Second)), engine.StepOptions{})
			require.NoError(t, err)
			tc.assert(t, res)
		})
	}
}

// TestCancelWithCompensationReconcilesOpenTasks verifies that cancelling an
// instance that has BOTH a compensation record (a completed compensable node)
// AND a parked user task marks that task Cancelled (via UpdateTask) even though
// termination is deferred to the compensation walk. Setup shape diverges from the
// table above (a completed compensable node precedes the parked task), so this is
// a standalone test.
func TestCancelWithCompensationReconcilesOpenTasks(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)

	// start → compensable-svc → user-task → end
	def := &model.ProcessDefinition{
		ID: "cc-comp", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithActionName("charge"), activity.WithCompensateAction("refund")),
			activity.NewUserTask("user", []string{"r"}),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "user"},
			{ID: "f3", Source: "user", Target: "end"},
		},
	}

	// Drive: start → svc parked.
	r0, err := engine.Step(def, engine.InstanceState{InstanceID: "cc-comp-1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	ia0, ok := r0.Commands[0].(engine.InvokeAction)
	require.True(t, ok)

	// svc completes → user task parked; a compensation record now exists.
	r1, err := engine.Step(def, r0.State,
		engine.NewActionCompleted(at.Add(time.Second), ia0.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.RootCompensations, 1, "setup: compensation record must exist")
	require.Len(t, r1.State.Tasks, 1, "setup: user task must be parked")

	// Cancel: enters the compensation-first branch (StatusCompensating), but the
	// open user task must still be reconciled to Cancelled.
	r2, err := engine.Step(def, r1.State,
		engine.NewCancelRequested(at.Add(2*time.Second)), engine.StepOptions{})
	require.NoError(t, err)

	require.Equal(t, engine.StatusCompensating, r2.State.Status)
	uts := findUpdateTasks(r2.Commands)
	require.Len(t, uts, 1, "the parked task must be cancelled on cancel-with-compensation")
	assert.Equal(t, humantask.Cancelled, uts[0].Task.State)

	task := r2.State.TaskByToken(uts[0].Task.TaskToken)
	require.NotNil(t, task)
	assert.Equal(t, humantask.Cancelled, task.State, "state must reflect the cancelled task")
}
