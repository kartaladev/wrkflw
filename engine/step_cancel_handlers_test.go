package engine_test

// step_cancel_handlers_test.go — Task 1: per-node cancel handlers
// (Node.CancelAction) emitted as InvokeCancelAction on CancelRequested.
//
// Design:  docs/specs/2026-06-23-cancel-handlers-design.md
// ADR:     0035
//
// Cases:
//  (a) one active node with CancelAction → InvokeCancelAction emitted alongside def.CancelActions
//  (b) two parallel active tokens, one node has handler one doesn't → exactly ONE node-cancel cmd
//  (c) active node inside a sub-process scope with handler → emitted via defForScope
//  (d) cancel-with-compensation + node handler → both node-cancel InvokeCancelAction AND comp
//      walk's first InvokeAction present; node-cancel ordered before the comp walk command
//  (e) no CancelAction set anywhere → identical command set to today (no extra InvokeCancelAction)
//  (f) determinism: same (def, state) ⇒ same commands

import (
	"reflect"
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
)

// ── helpers ──────────────────────────────────────────────────────────────────

// findInvokeCancelAction returns all InvokeCancelAction commands in cmds.
func findInvokeCancelActions(cmds []engine.Command) []engine.InvokeCancelAction {
	var out []engine.InvokeCancelAction
	for _, c := range cmds {
		if ica, ok := c.(engine.InvokeCancelAction); ok {
			out = append(out, ica)
		}
	}
	return out
}

// indexOfInvokeCancelAction returns the slice index of the first
// InvokeCancelAction with the given name, or -1 if not found.
func indexOfInvokeCancelAction(cmds []engine.Command, name string) int {
	for i, c := range cmds {
		if ica, ok := c.(engine.InvokeCancelAction); ok && ica.Name == name {
			return i
		}
	}
	return -1
}

// indexOfInvokeAction returns the slice index of the first InvokeAction with
// the given name, or -1 if not found.
func indexOfInvokeAction(cmds []engine.Command, name string) int {
	for i, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == name {
			return i
		}
	}
	return -1
}

// ── (a) one active node with CancelAction ───────────────────────────────────

// TestCancelHandler_SingleActiveNode verifies that when CancelRequested fires
// while exactly one node is active (a UserTask parked) and that node has a
// CancelAction, an InvokeCancelAction for the handler is emitted alongside
// any def.CancelActions.
func TestCancelHandler_SingleActiveNode(t *testing.T) {
	at := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)

	def := &model.ProcessDefinition{
		ID: "ch-single", Version: 1,
		CancelActions: []string{"global-cleanup"},
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("user", nil, activity.WithCancelAction("release-hold")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "user"},
			{ID: "f2", Source: "user", Target: "end"},
		},
	}

	// Drive: start → user task parked.
	st := engine.InstanceState{InstanceID: "ch-single-1"}
	r0, err := engine.Step(def, st, engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r0.State.Status)

	// CancelRequested while user task is parked.
	r1, err := engine.Step(def, r0.State,
		engine.NewCancelRequested(at.Add(time.Second)), engine.StepOptions{})
	require.NoError(t, err)

	icas := findInvokeCancelActions(r1.Commands)
	names := make([]string, len(icas))
	for i, ica := range icas {
		names[i] = ica.Name
	}

	// Must include the def-level action.
	assert.Contains(t, names, "global-cleanup", "def.CancelActions must be emitted")
	// Must include the per-node handler.
	assert.Contains(t, names, "release-hold", "node CancelAction must be emitted as InvokeCancelAction")
}

// ── (b) two parallel tokens, one with handler, one without ──────────────────

// TestCancelHandler_TwoParallelTokens verifies that with two parallel active
// tokens only the one whose node has a CancelAction emits an
// InvokeCancelAction; the other contributes nothing.
func TestCancelHandler_TwoParallelTokens(t *testing.T) {
	at := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)

	// start → parallel-fork → (svc-with-handler | svc-no-handler)
	def := &model.ProcessDefinition{
		ID: "ch-parallel", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewServiceTask("svc-a", activity.WithTaskAction("do-a"), activity.WithCancelAction("cleanup-a")),
			activity.NewServiceTask("svc-b", activity.WithTaskAction("do-b")),
			gateway.NewParallel("join"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f0", Source: "start", Target: "fork"},
			{ID: "f1", Source: "fork", Target: "svc-a"},
			{ID: "f2", Source: "fork", Target: "svc-b"},
			{ID: "f3", Source: "svc-a", Target: "join"},
			{ID: "f4", Source: "svc-b", Target: "join"},
			{ID: "f5", Source: "join", Target: "end"},
		},
	}

	// Start — drives to fork → both svc-a and svc-b parked.
	st := engine.InstanceState{InstanceID: "ch-par-1"}
	r0, err := engine.Step(def, st, engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r0.State.Tokens, 2, "setup: both parallel service tasks must be parked before cancel")

	// CancelRequested while both service tasks are parked.
	r1, err := engine.Step(def, r0.State,
		engine.NewCancelRequested(at.Add(time.Second)), engine.StepOptions{})
	require.NoError(t, err)

	icas := findInvokeCancelActions(r1.Commands)

	// Exactly ONE InvokeCancelAction (for svc-a's handler; svc-b has none).
	require.Len(t, icas, 1, "exactly one node-cancel InvokeCancelAction expected")
	assert.Equal(t, "cleanup-a", icas[0].Name)
}

// ── (c) active node inside a sub-process scope ───────────────────────────────

// TestCancelHandler_SubProcessScope verifies that an active node inside a
// sub-process scope with a CancelAction is resolved via defForScope and its
// InvokeCancelAction is emitted.
func TestCancelHandler_SubProcessScope(t *testing.T) {
	at := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)

	// Nested definition: start → inner-svc (with handler) → end
	innerDef := &model.ProcessDefinition{
		ID: "inner", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithTaskAction("inner-action"), activity.WithCancelAction("inner-cleanup")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
		},
	}

	// Outer definition: start → sub-process → end
	def := &model.ProcessDefinition{
		ID: "ch-sub", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", innerDef),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "end"},
		},
	}

	// Start → drives into sub-process → inner-svc parked.
	st := engine.InstanceState{InstanceID: "ch-sub-1"}
	r0, err := engine.Step(def, st, engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r0.State.Status)

	// Verify inner-svc is actually awaiting a command.
	var innerCmdID string
	for _, c := range r0.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "inner-action" {
			innerCmdID = ia.CommandID
			break
		}
	}
	require.NotEmpty(t, innerCmdID, "inner-action InvokeAction must be emitted")

	// CancelRequested while inner-svc is parked inside the sub-process scope.
	r1, err := engine.Step(def, r0.State,
		engine.NewCancelRequested(at.Add(time.Second)), engine.StepOptions{})
	require.NoError(t, err)

	icas := findInvokeCancelActions(r1.Commands)
	names := make([]string, len(icas))
	for i, ica := range icas {
		names[i] = ica.Name
	}

	require.Len(t, icas, 1, "exactly one node-cancel InvokeCancelAction (the inner active node) expected")
	assert.Contains(t, names, "inner-cleanup",
		"CancelAction on node in sub-process scope must be emitted via defForScope")
}

// ── (d) cancel-with-compensation + node handler ──────────────────────────────

// TestCancelHandler_WithCompensateAction verifies that on cancel, when both
// compensation records exist (completed compensable nodes) AND an active node
// has a CancelAction:
//   - the per-node InvokeCancelAction is emitted
//   - the compensation walk's first InvokeAction is emitted
//   - the per-node command appears before the compensation walk command
func TestCancelHandler_WithCompensateAction(t *testing.T) {
	at := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)

	// start → compensable-svc → user-task (with CancelAction) → end
	def := &model.ProcessDefinition{
		ID: "ch-comp", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithTaskAction("charge"), activity.WithCompensateAction("refund")),
			activity.NewUserTask("user", nil, activity.WithCancelAction("release-hold")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "user"},
			{ID: "f3", Source: "user", Target: "end"},
		},
	}

	// Step 1: start → svc parked.
	st := engine.InstanceState{InstanceID: "ch-comp-1"}
	r0, err := engine.Step(def, st, engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	ia0, ok := r0.Commands[0].(engine.InvokeAction)
	require.True(t, ok)
	require.Equal(t, "charge", ia0.Name)

	// Step 2: svc completes → user task parked (compensation record created).
	r1, err := engine.Step(def, r0.State,
		engine.NewActionCompleted(at.Add(time.Second), ia0.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.RootCompensations, 1)

	// Step 3: CancelRequested while user task is parked.
	r2, err := engine.Step(def, r1.State,
		engine.NewCancelRequested(at.Add(2*time.Second)), engine.StepOptions{})
	require.NoError(t, err)

	// Assert engine is now compensating (compensation walk triggered).
	assert.Equal(t, engine.StatusCompensating, r2.State.Status)

	// Assert: per-node InvokeCancelAction{Name:"release-hold"} is present.
	idxCancel := indexOfInvokeCancelAction(r2.Commands, "release-hold")
	assert.GreaterOrEqual(t, idxCancel, 0, "InvokeCancelAction{Name:\"release-hold\"} must be emitted")

	// Assert: compensation walk's InvokeAction{Name:"refund"} is present.
	idxComp := indexOfInvokeAction(r2.Commands, "refund")
	assert.GreaterOrEqual(t, idxComp, 0, "InvokeAction{Name:\"refund\"} for compensation must be emitted")

	// Assert ordering: per-node cancel action BEFORE the compensation walk action.
	assert.Less(t, idxCancel, idxComp,
		"node CancelAction InvokeCancelAction must appear before the compensation InvokeAction")
}

// ── (e) no CancelAction set anywhere → identical to today ──────────────────

// TestCancelHandler_NoneSet verifies that when no node sets CancelAction the
// command set produced by CancelRequested is byte-for-behaviour identical to
// the pre-change baseline (i.e. no extra InvokeCancelAction beyond
// def.CancelActions).
func TestCancelHandler_NoneSet(t *testing.T) {
	at := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)

	def := &model.ProcessDefinition{
		ID: "ch-none", Version: 1,
		CancelActions: []string{"global-cleanup"},
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("user", nil), // no CancelAction
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "user"},
			{ID: "f2", Source: "user", Target: "end"},
		},
	}

	st := engine.InstanceState{InstanceID: "ch-none-1"}
	r0, err := engine.Step(def, st, engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	r1, err := engine.Step(def, r0.State,
		engine.NewCancelRequested(at.Add(time.Second)), engine.StepOptions{})
	require.NoError(t, err)

	icas := findInvokeCancelActions(r1.Commands)

	// Only the def-level global-cleanup InvokeCancelAction should be present.
	require.Len(t, icas, 1, "only def.CancelActions InvokeCancelAction must be present when no node sets CancelAction")
	assert.Equal(t, "global-cleanup", icas[0].Name)
}

// ── (f) determinism ──────────────────────────────────────────────────────────

// TestCancelHandler_Determinism verifies that calling Step with the same
// (def, state) pair twice produces identical command slices.
func TestCancelHandler_Determinism(t *testing.T) {
	at := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)

	def := &model.ProcessDefinition{
		ID: "ch-det", Version: 1,
		CancelActions: []string{"def-action"},
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithTaskAction("work"), activity.WithCancelAction("node-cleanup")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
		},
	}

	st := engine.InstanceState{InstanceID: "ch-det-1"}
	r0, err := engine.Step(def, st, engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	cancelAt := at.Add(time.Second)

	r1a, err := engine.Step(def, r0.State, engine.NewCancelRequested(cancelAt), engine.StepOptions{})
	require.NoError(t, err)
	r1b, err := engine.Step(def, r0.State, engine.NewCancelRequested(cancelAt), engine.StepOptions{})
	require.NoError(t, err)

	assert.True(t, reflect.DeepEqual(r1a.Commands, r1b.Commands),
		"same (def, state) must produce identical command slices")
}
