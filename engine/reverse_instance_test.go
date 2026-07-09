package engine_test

// reverse_instance_test.go — black-box tests for Task 3 (ADR-0109): the core
// engine change that turns a FULL compensation walk carrying a ReverseNode into
// a resume-at-start (StatusRunning, optional var reset) instead of terminating.
// The cancel/error terminate path (full walk with NO ReverseNode) must remain
// behaviorally identical — TestFullCompensation_WithoutReverse_StillTerminates
// is the regression guard for that.

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// findInvokeActionID scans cmds for an InvokeAction with the given Name and
// returns its CommandID, failing the test if none is found. Shared by the
// cycle/LIFO and completion-action reversibility tests below, which each
// drive several rounds of Step/ActionCompleted and need to pluck out the
// next command id to complete.
func findInvokeActionID(t *testing.T, cmds []engine.Command, name string) string {
	t.Helper()
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == name {
			return ia.CommandID
		}
	}
	require.Fail(t, "InvokeAction not found", "wanted action %q in %#v", name, cmds)
	return ""
}

// reverseSvcDef: start → svc(compensable) → park → end. "park" is a plain
// (non-compensable) ServiceTask whose InvokeAction is intentionally never
// completed by the tests below: driving the "do" action to completion
// auto-advances the token onto "park" and stops there, so the instance is
// StatusRunning (not Completed) — with svc's compensation already recorded —
// by the time a reverse is requested. This is the shape Fork A's terminal
// guard (ADR-0109 hardening) requires: reverse only ever needs to work
// against a Running instance.
func reverseSvcDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-rev", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithTaskAction("do"), activity.WithCompensateAction("undo")),
			activity.NewServiceTask("park", activity.WithTaskAction("park")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "park"},
			{ID: "f3", Source: "park", Target: "end"},
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
	// Precondition for the reverse below: the flow auto-drives svc -> park within
	// the same Step call, and park's InvokeAction is deliberately left uncompleted,
	// so the instance is RUNNING (parked on "park") — not completed — when reverse
	// is requested. This is the shape Fork A's terminal guard requires: reverse
	// must work against a Running instance that already carries compensation
	// records.
	require.Equal(t, engine.StatusRunning, r2.State.Status, "instance must be running (parked) before reverse")
	require.Nil(t, r2.State.EndedAt, "running instance must not have EndedAt stamped")

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
	assert.Nil(t, r4.State.EndedAt, "Running instance must have EndedAt nil")
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

// reverseLoopDef: start -> prep(compensable, once) -> svc(compensable) -> xor
// -{attempts < 3}-> back to svc ; -default-> park -> end.
//
// prep is a distinct compensable node BEFORE the loop, recorded exactly once.
// svc is the loop body, driven to complete 3 times (3 separate compensation
// records, same NodeID "svc"). "park" is a plain (non-compensable) ServiceTask
// whose InvokeAction is intentionally never completed: exiting the loop drives
// the token onto "park" and stops there, so the driven-to-completion state is
// StatusRunning (parked), not Completed — the shape Fork A's terminal guard
// requires. This shape lets a single such state serve two different reverse
// scenarios below: a FULL reverse (walks all 4 records: prep, svc, svc, svc)
// and a PARTIAL reverse targeting "prep" (walks only the 3 svc records,
// excluding — but retaining — prep's own).
func reverseLoopDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-rev-loop", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("prep", activity.WithTaskAction("prep"), activity.WithCompensateAction("unprep")),
			activity.NewServiceTask("svc", activity.WithTaskAction("work"), activity.WithCompensateAction("undo")),
			gateway.NewExclusive("xor"),
			activity.NewServiceTask("park", activity.WithTaskAction("park")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "prep"},
			{ID: "f2", Source: "prep", Target: "svc"},
			{ID: "f3", Source: "svc", Target: "xor"},
			{ID: "f4", Source: "xor", Target: "svc", Condition: "attempts < 3"},
			{ID: "f5", Source: "xor", Target: "park", IsDefault: true},
			{ID: "f6", Source: "park", Target: "end"},
		},
	}
}

// driveReverseLoopToCompletion drives reverseLoopDef through start -> prep ->
// svc (looped 3x via the xor reject/re-escalate condition) -> park, recording
// 4 compensation entries in completion order: prep, svc, svc, svc, and parking
// (StatusRunning, park's InvokeAction left uncompleted) rather than reaching
// "end". Returns the StepResult at that parked, Running state, ready to fork
// into either a full or a partial reverse.
func driveReverseLoopToCompletion(t *testing.T, def *model.ProcessDefinition, t0 time.Time) engine.StepResult {
	t.Helper()

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"}, engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)
	cmdPrep := findInvokeActionID(t, r1.Commands, "prep")

	r2, err := engine.Step(def, r1.State, engine.NewActionCompleted(t0, cmdPrep, nil), engine.StepOptions{})
	require.NoError(t, err)
	cmdSvc1 := findInvokeActionID(t, r2.Commands, "work")

	r3, err := engine.Step(def, r2.State, engine.NewActionCompleted(t0, cmdSvc1, map[string]any{"attempts": 1}), engine.StepOptions{})
	require.NoError(t, err)
	cmdSvc2 := findInvokeActionID(t, r3.Commands, "work")

	r4, err := engine.Step(def, r3.State, engine.NewActionCompleted(t0, cmdSvc2, map[string]any{"attempts": 2}), engine.StepOptions{})
	require.NoError(t, err)
	cmdSvc3 := findInvokeActionID(t, r4.Commands, "work")

	r5, err := engine.Step(def, r4.State, engine.NewActionCompleted(t0, cmdSvc3, map[string]any{"attempts": 3}), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r5.State.Status, "loop exits (attempts==3) and the instance parks at the trailing node")
	require.Len(t, r5.State.RootCompensations, 4, "prep + 3x svc compensation records")

	return r5
}

// TestReverseCycleLIFO locks in the reverse-compensation walk's LIFO ordering
// across a gateway reject/re-escalate loop that completes the same
// compensable node (svc) 3 times. Both the FULL reverse-to-start walk and a
// PARTIAL (WithTargetNode-style) reverse via CompensateRequested must fire
// exactly the 3 "undo" InvokeActions, newest-completed-first.
func TestReverseCycleLIFO(t *testing.T) {
	def := reverseLoopDef()
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	base := driveReverseLoopToCompletion(t, def, t0)

	t.Run("full reverse: 3 undo fire newest-first, resumes running at start", func(t *testing.T) {
		// The walk is FIFO-driven one ActionCompleted at a time; each step's
		// InvokeAction is the record immediately after the one just compensated.
		// Action name alone ("undo") cannot distinguish the 3 same-node svc
		// records from one another, so a whole-walk reversal bug (e.g. the 3 svc
		// records fired oldest-first, or interleaved with prep) would slip past
		// name-only assertions. recordCompensation snapshots s.Variables BEFORE
		// merging each completion's output (see handleActionCompleted), and the
		// fixture's loop feeds distinct "attempts" values per svc completion —
		// so each undo's InvokeAction.Input carries a distinct, ORDER-REVEALING
		// fingerprint. Empirically confirmed (driveReverseLoopToCompletion feeds
		// nil, {attempts:1}, {attempts:2} across the 3 "work" completions, and
		// each is recorded pre-merge): completion order is
		//   svc#1 input=nil -> svc#2 input={attempts:1} -> svc#3 input={attempts:2}
		// so the newest-first walk must fire svc#3, svc#2, svc#1 — i.e.
		// attempts 2, then 1, then absent. Asserting Input at each hop (not just
		// the action name) proves the LIFO order among the indistinguishable-by-
		// name records; a same-node reordering regression now fails here instead
		// of silently passing.
		r6, err := engine.Step(def, base.State, engine.NewReverseToStart(t0, "start"), engine.StepOptions{})
		require.NoError(t, err)
		undo1, ok := findInvokeAction(r6.Commands, "undo") // svc (3rd/newest completion)
		require.True(t, ok, "reverse must invoke the compensate action")
		assert.Equal(t, 2, undo1.Input["attempts"], "hop 1 compensates the 3rd/newest svc completion (pre-merge attempts=2)")

		r7, err := engine.Step(def, r6.State, engine.NewActionCompleted(t0, undo1.CommandID, nil), engine.StepOptions{})
		require.NoError(t, err)
		undo2, ok := findInvokeAction(r7.Commands, "undo") // svc (2nd completion)
		require.True(t, ok, "reverse must invoke the compensate action")
		assert.Equal(t, 1, undo2.Input["attempts"], "hop 2 compensates the 2nd svc completion (pre-merge attempts=1)")

		r8, err := engine.Step(def, r7.State, engine.NewActionCompleted(t0, undo2.CommandID, nil), engine.StepOptions{})
		require.NoError(t, err)
		undo3, ok := findInvokeAction(r8.Commands, "undo") // svc (1st completion)
		require.True(t, ok, "reverse must invoke the compensate action")
		assert.NotContains(t, undo3.Input, "attempts", "hop 3 compensates the 1st svc completion (pre-merge, attempts not yet set)")

		r9, err := engine.Step(def, r8.State, engine.NewActionCompleted(t0, undo3.CommandID, nil), engine.StepOptions{})
		require.NoError(t, err)
		// Exactly 3 undo hops: the record immediately after undo3 is prep's own
		// (oldest, distinct action name "unprep") — findInvokeActionID fails the
		// test outright if an "undo" showed up instead, proving no 4th undo fires.
		unprepID := findInvokeActionID(t, r9.Commands, "unprep")

		r10, err := engine.Step(def, r9.State, engine.NewActionCompleted(t0, unprepID, nil), engine.StepOptions{})
		require.NoError(t, err)
		assert.Equal(t, engine.StatusRunning, r10.State.Status, "full reverse resumes Running, NOT terminated")
		assert.Empty(t, r10.State.RootCompensations, "records cleared after full reverse")
		require.Len(t, r10.State.Tokens, 1)
		assert.Equal(t, "prep", r10.State.Tokens[0].NodeID, "resumed from start and advanced to prep")
	})

	t.Run("partial reverse to prep: same 3 undo fire newest-first, resumes running at prep", func(t *testing.T) {
		p1, err := engine.Step(def, base.State, engine.NewCompensateRequested(t0, "prep"), engine.StepOptions{})
		require.NoError(t, err)
		undo1 := findInvokeActionID(t, p1.Commands, "undo") // svc (3rd/newest completion)

		p2, err := engine.Step(def, p1.State, engine.NewActionCompleted(t0, undo1, nil), engine.StepOptions{})
		require.NoError(t, err)
		undo2 := findInvokeActionID(t, p2.Commands, "undo") // svc (2nd completion)

		p3, err := engine.Step(def, p2.State, engine.NewActionCompleted(t0, undo2, nil), engine.StepOptions{})
		require.NoError(t, err)
		undo3 := findInvokeActionID(t, p3.Commands, "undo") // svc (1st completion)

		// The walk must stop at the "prep" boundary WITHOUT compensating prep's own
		// record (it is the rollback target, excluded — not re-run), and resume
		// Running with a token placed back at "prep" (re-invoking its own action).
		p4, err := engine.Step(def, p3.State, engine.NewActionCompleted(t0, undo3, nil), engine.StepOptions{})
		require.NoError(t, err)
		assert.Equal(t, engine.StatusRunning, p4.State.Status, "partial reverse resumes Running at the target node")
		require.Len(t, p4.State.Tokens, 1)
		assert.Equal(t, "prep", p4.State.Tokens[0].NodeID, "resumed at target node and re-invoked")
		reinvokedPrep := findInvokeActionID(t, p4.Commands, "prep")
		assert.NotEmpty(t, reinvokedPrep, "resuming at the target node re-invokes its own action")
		// Partial rollback intentionally RETAINS all records (documented behavior:
		// the instance keeps running and a later full walk must still see them).
		assert.Len(t, p4.State.RootCompensations, 4, "partial rollback retains all records, unlike full reverse")
	})
}

// userTaskCompletionReversibleDef: a UserTask carrying BOTH a CompletionAction
// ("record") and a CompensateAction ("unrecord") — the combination the Item-4
// Build guard (ErrCompensateActionWithoutForwardAction) requires: a
// UserTask/ReceiveTask's CompensateAction is only valid when it also has a
// CompletionAction (the completion action IS the forward action for these
// kinds). "park" is a plain (non-compensable) ServiceTask whose InvokeAction
// is intentionally never completed: the completion action's round-trip
// auto-advances the token onto "park" and stops there, so the instance is
// StatusRunning (not Completed) — with u1's compensation already recorded —
// by the time a reverse is requested.
func userTaskCompletionReversibleDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-rev-uc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("u1", []string{"r"},
				activity.WithCompletionAction("record"),
				activity.WithCompensateAction("unrecord"),
			),
			activity.NewServiceTask("park", activity.WithTaskAction("park")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "u1"},
			{ID: "f2", Source: "u1", Target: "park"},
			{ID: "f3", Source: "park", Target: "end"},
		},
	}
}

// TestReverseCompletionActionReversibility proves that the Item-4
// completion-action round-trip (WithCompletionAction) creates a genuinely
// reversible compensation record: completing the human task parks on the
// completion action's InvokeAction/ActionCompleted round-trip (per
// completion_action_test.go), which is exactly the code path that records a
// CompensationRecord (handleActionCompleted records compensation for ANY
// completed action whose node carries a CompensateAction — the completion
// action's ActionCompleted is no exception). A subsequent full reverse must
// then fire the paired "unrecord" compensate action and resume at start.
func TestReverseCompletionActionReversibility(t *testing.T) {
	def := userTaskCompletionReversibleDef()
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"}, engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.Tasks, 1)
	taskToken := r1.State.Tasks[0].TaskToken

	// Complete the human task: the completion action ("record") fires as an
	// InvokeAction and the token parks on it — the instance must not complete yet.
	r2, err := engine.Step(def, r1.State,
		engine.NewHumanCompleted(t0, taskToken, map[string]any{"approved": true}, authz.Actor{ID: "alice"}),
		engine.StepOptions{})
	require.NoError(t, err)
	recordCmdID := findInvokeActionID(t, r2.Commands, "record")
	assert.NotEqual(t, engine.StatusCompleted, r2.State.Status, "must not complete before the completion action returns")

	// The completion action returns: this ActionCompleted is the round-trip that
	// records the compensation entry (Fix under test), then the token auto-advances
	// onto "park" and stops there (instance stays Running).
	r3, err := engine.Step(def, r2.State, engine.NewActionCompleted(t0, recordCmdID, map[string]any{"recorded": true}), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r3.State.Status, "instance auto-advances onto park and stops there, not completed")
	require.Len(t, r3.State.RootCompensations, 1, "the completion-action round-trip must record a compensation entry")
	assert.Equal(t, "u1", r3.State.RootCompensations[0].NodeID)
	assert.Equal(t, "unrecord", r3.State.RootCompensations[0].Action)

	// Reverse to start: the paired compensate action ("unrecord") must fire.
	r4, err := engine.Step(def, r3.State, engine.NewReverseToStart(t0, "start"), engine.StepOptions{})
	require.NoError(t, err)
	unrecordCmdID := findInvokeActionID(t, r4.Commands, "unrecord")

	r5, err := engine.Step(def, r4.State, engine.NewActionCompleted(t0, unrecordCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r5.State.Status, "reverse resumes Running, NOT terminated")
	assert.Empty(t, r5.State.RootCompensations, "records cleared after full reverse")
	require.Len(t, r5.State.Tokens, 1)
	assert.Equal(t, "u1", r5.State.Tokens[0].NodeID, "resumed from start and advanced back to the human task")
}

// reverseCompletableDef: start -> svc -> end, with NO compensate action, so
// completing "do" auto-drives the token all the way to "end" within the same
// Step call — the smallest def that reaches StatusCompleted synchronously.
// Used only by TestReverseToStart_RejectsTerminalInstance to reach a genuine
// StatusCompleted instance via normal drive (the Completed case in the table).
func reverseCompletableDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-rev-completed", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithTaskAction("do")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
		},
	}
}

// TestReverseToStart_RejectsTerminalInstance is the Fork A (ADR-0109 hardening)
// regression: a reverse trigger (NewReverseToStart, i.e. CompensateRequested
// with ReverseNode != "") against an already-terminal instance (Completed,
// Failed, or Terminated) must return a workflow-engine error instead of
// silently resurrecting the instance. This closes the TOCTOU race between the
// runtime facade's pre-check Load and the engine's own ApplyTrigger Load.
//
// Scope is strictly the reverse intent (t.ReverseNode != ""); a plain
// NewCompensateRequested (admin/partial rollback) on a terminal instance keeps
// today's behavior and is NOT covered here — see
// TestFullCompensation_WithoutReverse_StillTerminates and the existing partial
// admin-rollback tests, which are unaffected by this guard.
func TestReverseToStart_RejectsTerminalInstance(t *testing.T) {
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name   string
		state  func(t *testing.T) (def *model.ProcessDefinition, s engine.InstanceState)
		assert func(t *testing.T, err error)
	}

	// rejectsTerminal is the shared assertion: every terminal status must be
	// rejected identically, so each case carries the same assert closure.
	rejectsTerminal := func(t *testing.T, err error) {
		t.Helper()
		require.Error(t, err, "reversing a terminal instance must be rejected")
		assert.True(t, strings.HasPrefix(err.Error(), "workflow-engine:"), "error must carry the workflow-engine: sentinel prefix, got %q", err.Error())
		assert.Contains(t, err.Error(), "terminal", "error must explain the rejection is due to terminal status")
	}

	cases := []testCase{
		{
			name: "completed instance (driven via normal execution)",
			state: func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState) {
				t.Helper()
				def := reverseCompletableDef()
				r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
					engine.NewStartInstance(t0, nil), engine.StepOptions{})
				require.NoError(t, err)
				cmdID := findInvokeActionID(t, r1.Commands, "do")
				r2, err := engine.Step(def, r1.State, engine.NewActionCompleted(t0, cmdID, nil), engine.StepOptions{})
				require.NoError(t, err)
				require.Equal(t, engine.StatusCompleted, r2.State.Status, "precondition: instance must have completed")
				return def, r2.State
			},
			assert: rejectsTerminal,
		},
		{
			name: "failed instance (hand-crafted terminal state)",
			state: func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState) {
				t.Helper()
				def := reverseSvcDef()
				return def, engine.InstanceState{
					InstanceID: "i1",
					Status:     engine.StatusFailed,
				}
			},
			assert: rejectsTerminal,
		},
		{
			name: "terminated instance (hand-crafted terminal state)",
			state: func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState) {
				t.Helper()
				def := reverseSvcDef()
				return def, engine.InstanceState{
					InstanceID: "i1",
					Status:     engine.StatusTerminated,
				}
			},
			assert: rejectsTerminal,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def, before := tc.state(t)
			beforeStatus := before.Status

			_, err := engine.Step(def, before, engine.NewReverseToStart(t0, "start"), engine.StepOptions{})

			tc.assert(t, err)
			// Step is pure (clones state internally); the caller's original state
			// value must be unaffected by the erroring call.
			assert.Equal(t, beforeStatus, before.Status, "original state must be unchanged after a rejected reverse")
		})
	}
}
