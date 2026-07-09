package engine_test

// step_fail_tasks_test.go — an instance reaching StatusFailed via an unhandled
// error reconciles its open human tasks, mirroring the cancel path (ADR-0088).
// A parallel branch parked at a UserTask must not be left Unclaimed in the
// TaskStore when another branch faults the whole instance.
//
// ADR: 0089 (extends 0088)
//
// findUpdateTasks is defined in step_cancel_tasks_test.go (same test package).

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

// TestUnhandledFailureReconcilesOpenTasks: a parallel fork parks a UserTask on
// one branch while the other branch's ServiceTask fails unhandled. The instance
// fails, and the parked task must be marked Cancelled (via UpdateTask).
func TestUnhandledFailureReconcilesOpenTasks(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC)

	// start → fork → (user[UserTask] | svc[Service "boom"]) → join → end
	def := &model.ProcessDefinition{
		ID: "f-unhandled", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewUserTask("user", activity.WithCandidateRoles("r")),
			activity.NewServiceTask("svc", activity.WithTaskAction("boom")),
			gateway.NewParallel("join"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f0", Source: "start", Target: "fork"},
			{ID: "f1", Source: "fork", Target: "user"},
			{ID: "f2", Source: "fork", Target: "svc"},
			{ID: "f3", Source: "user", Target: "join"},
			{ID: "f4", Source: "svc", Target: "join"},
			{ID: "f5", Source: "join", Target: "end"},
		},
	}

	// Start → fork splits: user task parks (task record), svc parks awaiting a command.
	r0, err := engine.Step(def, engine.InstanceState{InstanceID: "fu-1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r0.State.Tasks, 1, "setup: user task must be parked")

	var boomCmdID string
	for _, c := range r0.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "boom" {
			boomCmdID = ia.CommandID
			break
		}
	}
	require.NotEmpty(t, boomCmdID, "setup: svc InvokeAction must be emitted")

	// svc fails unhandled (retryable=false, no boundary/recovery) → StatusFailed.
	r1, err := engine.Step(def, r0.State,
		engine.NewActionFailed(at.Add(time.Second), boomCmdID, "boom", false), engine.StepOptions{})
	require.NoError(t, err)

	require.Equal(t, engine.StatusFailed, r1.State.Status)
	uts := findUpdateTasks(r1.Commands)
	require.Len(t, uts, 1, "the parked task must be cancelled when the instance fails")
	assert.Equal(t, humantask.Cancelled, uts[0].Task.State)

	task := r1.State.TaskByToken(uts[0].Task.TaskToken)
	require.NotNil(t, task)
	assert.Equal(t, humantask.Cancelled, task.State, "failed-instance state must reflect the cancelled task")
}

// TestFailureWithCompensationReconcilesOpenTasks: an unhandled failure that first
// runs a compensation walk (a completed compensable node precedes the fork) must
// still reconcile the parked UserTask when the walk finishes as StatusFailed.
func TestFailureWithCompensationReconcilesOpenTasks(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC)

	// start → charge[Service comp:refund] → fork → (user | svc "boom") → join → end
	def := &model.ProcessDefinition{
		ID: "f-comp", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("charge", activity.WithTaskAction("charge"), activity.WithCompensateAction("refund")),
			gateway.NewParallel("fork"),
			activity.NewUserTask("user", activity.WithCandidateRoles("r")),
			activity.NewServiceTask("svc", activity.WithTaskAction("boom")),
			gateway.NewParallel("join"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f0", Source: "start", Target: "charge"},
			{ID: "f1", Source: "charge", Target: "fork"},
			{ID: "f2", Source: "fork", Target: "user"},
			{ID: "f3", Source: "fork", Target: "svc"},
			{ID: "f4", Source: "user", Target: "join"},
			{ID: "f5", Source: "svc", Target: "join"},
			{ID: "f6", Source: "join", Target: "end"},
		},
	}

	// Step 1: start → charge parks.
	r0, err := engine.Step(def, engine.InstanceState{InstanceID: "fc-1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	chargeIA, ok := r0.Commands[0].(engine.InvokeAction)
	require.True(t, ok)
	require.Equal(t, "charge", chargeIA.Name)

	// Step 2: charge completes → fork → user parks (task) + svc parks; comp record exists.
	r1, err := engine.Step(def, r0.State,
		engine.NewActionCompleted(at.Add(time.Second), chargeIA.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.RootCompensations, 1, "setup: compensation record must exist")
	require.Len(t, r1.State.Tasks, 1, "setup: user task must be parked")
	var boomCmdID string
	for _, c := range r1.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "boom" {
			boomCmdID = ia.CommandID
			break
		}
	}
	require.NotEmpty(t, boomCmdID)

	// Step 3: svc fails unhandled → compensation walk begins (StatusCompensating).
	r2, err := engine.Step(def, r1.State,
		engine.NewActionFailed(at.Add(2*time.Second), boomCmdID, "boom", false), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, r2.State.Status)
	var refundCmdID string
	for _, c := range r2.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "refund" {
			refundCmdID = ia.CommandID
			break
		}
	}
	require.NotEmpty(t, refundCmdID, "compensation walk must emit refund")

	// Step 4: refund completes → walk finishes as StatusFailed; task reconciled.
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(3*time.Second), refundCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)

	require.Equal(t, engine.StatusFailed, r3.State.Status)
	uts := findUpdateTasks(r3.Commands)
	require.Len(t, uts, 1, "the parked task must be cancelled when compensation finishes as Failed")
	assert.Equal(t, humantask.Cancelled, uts[0].Task.State)
}

// TestSubInstanceFailureReconcilesOpenTasks: a failed child instance fails the
// parent; a UserTask parked on a sibling parallel branch of the parent must be
// reconciled to Cancelled.
func TestSubInstanceFailureReconcilesOpenTasks(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC)

	// start → fork → (user[UserTask] | call[CallActivity "child"]) → join → end
	def := &model.ProcessDefinition{
		ID: "f-sub", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewUserTask("user", activity.WithCandidateRoles("r")),
			activity.NewCallActivity("call", model.Latest("child")),
			gateway.NewParallel("join"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f0", Source: "start", Target: "fork"},
			{ID: "f1", Source: "fork", Target: "user"},
			{ID: "f2", Source: "fork", Target: "call"},
			{ID: "f3", Source: "user", Target: "join"},
			{ID: "f4", Source: "call", Target: "join"},
			{ID: "f5", Source: "join", Target: "end"},
		},
	}

	// Start → fork splits: user task parks (task record), call parks awaiting a sub-instance.
	r0, err := engine.Step(def, engine.InstanceState{InstanceID: "fs-1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r0.State.Tasks, 1, "setup: user task must be parked")
	var ssiCmdID string
	for _, c := range r0.Commands {
		if ssi, ok := c.(engine.StartSubInstance); ok {
			ssiCmdID = ssi.CommandID
			break
		}
	}
	require.NotEmpty(t, ssiCmdID, "setup: StartSubInstance must be emitted")

	// The child fails → parent fails.
	r1, err := engine.Step(def, r0.State,
		engine.NewSubInstanceFailed(at.Add(time.Second), ssiCmdID, "child blew up"), engine.StepOptions{})
	require.NoError(t, err)

	require.Equal(t, engine.StatusFailed, r1.State.Status)
	uts := findUpdateTasks(r1.Commands)
	require.Len(t, uts, 1, "the parked task must be cancelled when a child failure fails the parent")
	assert.Equal(t, humantask.Cancelled, uts[0].Task.State)
}
