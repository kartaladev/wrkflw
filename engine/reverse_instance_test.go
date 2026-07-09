package engine_test

// reverse_instance_test.go — black-box tests for Task 3 (ADR-0109): the core
// engine change that turns a FULL compensation walk carrying a ReverseNode into
// a resume-at-start (StatusRunning, optional var reset) instead of terminating.
// The cancel/error terminate path (full walk with NO ReverseNode) must remain
// behaviorally identical — TestFullCompensation_WithoutReverse_StillTerminates
// is the regression guard for that.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// reverseSvcDef: start → svc(compensable) → end.
func reverseSvcDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-rev", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithTaskAction("do"), activity.WithCompensateAction("undo")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
		},
	}
}

func TestReverseToStart_ResumesAtStartWithResetVars(t *testing.T) {
	def := reverseSvcDef()
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	// Start with amount=100; drive the service action to completion (records compensation).
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, map[string]any{"amount": 100}), engine.StepOptions{})
	require.NoError(t, err)
	// Find the svc InvokeAction command id, complete it (mutating a var along the way).
	var cmdID string
	for _, c := range r1.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "do" {
			cmdID = ia.CommandID
		}
	}
	require.NotEmpty(t, cmdID)
	r2, err := engine.Step(def, r1.State, engine.NewActionCompleted(t0, cmdID, map[string]any{"amount": 500}), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.State.RootCompensations, 1, "svc must have recorded a compensation")
	// Sanity precondition for the EndedAt regression below: the flow auto-drives
	// svc -> end within the same Step call, so the instance is already COMPLETED
	// (with EndedAt stamped) before reverse is ever requested. This is the primary
	// use case the reverse-to-start branch must handle correctly.
	require.Equal(t, engine.StatusCompleted, r2.State.Status, "instance must have reached completion before reverse")
	require.NotNil(t, r2.State.EndedAt, "completed instance must have EndedAt stamped")

	// Reverse to start: expect the "undo" compensation to fire, then resume at start with reset vars.
	r3, err := engine.Step(def, r2.State, engine.NewReverseToStart(t0, "start"), engine.StepOptions{})
	require.NoError(t, err)
	var undoID string
	for _, c := range r3.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "undo" {
			undoID = ia.CommandID
		}
	}
	require.NotEmpty(t, undoID, "reverse must invoke the compensate action")
	r4, err := engine.Step(def, r3.State, engine.NewActionCompleted(t0, undoID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r4.State.Status, "reverse resumes Running, NOT terminated")
	assert.Nil(t, r4.State.EndedAt, "Running instance must have EndedAt cleared (was stamped at prior completion)")
	assert.Equal(t, 100, r4.State.Variables["amount"], "vars reset to StartVariables")
	assert.Empty(t, r4.State.RootCompensations, "records cleared after full reverse")
	// The finish places a token at the start node and drives forward, so execution
	// re-runs from the start: the token has advanced to svc and the "do" action is
	// re-invoked (with the reset vars). This is the "resume and re-run from start"
	// behaviour — the token does not sit on the start event because drive advances it.
	require.Len(t, r4.State.Tokens, 1)
	assert.Equal(t, "svc", r4.State.Tokens[0].NodeID, "execution resumed from start and advanced to svc")
	var redoID string
	for _, c := range r4.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "do" {
			redoID = ia.CommandID
		}
	}
	assert.NotEmpty(t, redoID, "resuming from start re-invokes the svc action")
}

// Regression: the cancel/error terminate path (full compensation with NO ReverseNode) still terminates.
func TestFullCompensation_WithoutReverse_StillTerminates(t *testing.T) {
	def := reverseSvcDef()
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	r1, _ := engine.Step(def, engine.InstanceState{InstanceID: "i1"}, engine.NewStartInstance(t0, map[string]any{"amount": 100}), engine.StepOptions{})
	var cmdID string
	for _, c := range r1.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "do" {
			cmdID = ia.CommandID
		}
	}
	r2, _ := engine.Step(def, r1.State, engine.NewActionCompleted(t0, cmdID, nil), engine.StepOptions{})
	r3, err := engine.Step(def, r2.State, engine.NewCompensateRequested(t0, ""), engine.StepOptions{}) // full, no reverse
	require.NoError(t, err)
	var undoID string
	for _, c := range r3.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "undo" {
			undoID = ia.CommandID
		}
	}
	r4, _ := engine.Step(def, r3.State, engine.NewActionCompleted(t0, undoID, nil), engine.StepOptions{})
	assert.Equal(t, engine.StatusTerminated, r4.State.Status, "full compensation without ReverseNode still terminates")
}

// TestReverseToStart_ZeroRecords_StillResumesAtStart guards the early-finish
// path: a full reverse with NO eligible compensation records must still resume
// at the start node (StatusRunning) rather than terminating.
func TestReverseToStart_ZeroRecords_StillResumesAtStart(t *testing.T) {
	def := reverseSvcDef()
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	// Start but do NOT complete svc — no compensation records recorded yet.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, map[string]any{"amount": 100}), engine.StepOptions{})
	require.NoError(t, err)
	require.Empty(t, r1.State.RootCompensations, "no compensation records yet")

	r2, err := engine.Step(def, r1.State, engine.NewReverseToStart(t0, "start"), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status, "zero-record reverse resumes Running, NOT terminated")
	assert.Equal(t, 100, r2.State.Variables["amount"], "vars reset to StartVariables")
	// Resumed from start and drove forward: the token advanced to svc (re-execution),
	// proving the early-finish path honours the reverse intent instead of terminating.
	require.Len(t, r2.State.Tokens, 1)
	assert.Equal(t, "svc", r2.State.Tokens[0].NodeID, "execution resumed from start and advanced to svc")
}
