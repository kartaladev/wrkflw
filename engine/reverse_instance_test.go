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
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
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
			activity.NewUserTask("u1", activity.WithCandidateRoles("r"),
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

// TestCompensateRequested_DuringActiveWalk is the Fork C (ADR-0109 hardening)
// regression: stepCompensateRequested's mid-walk guard (StatusCompensating &&
// ActiveCmdID != "") today silently no-ops ANY CompensateRequested delivered
// while a compensation walk is already in flight — including a reverse
// trigger (ReverseNode != ""). The runtime facade admits a reverse against a
// Compensating instance, so a reverse arriving mid-walk is currently
// discarded with no error: the caller believes it succeeded when nothing
// happened. This must instead return a workflow-engine: error. A plain
// admin/partial CompensateRequested (ReverseNode == "") delivered during the
// same in-flight walk is UNCHANGED — it keeps the existing silent no-op,
// which is shared with admin/cancel/error callers that may legitimately
// re-deliver a trigger mid-walk.
func TestCompensateRequested_DuringActiveWalk(t *testing.T) {
	def := reverseSvcDef()
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)

	// Drive to an in-flight compensation walk: start -> complete "do" (records
	// svc's compensation, parks on "park") -> NewReverseToStart begins the
	// walk and emits the "undo" InvokeAction WITHOUT completing it, so the
	// cursor's ActiveCmdID is non-empty (mid-walk) at this state.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, map[string]any{"amount": 100}), engine.StepOptions{})
	require.NoError(t, err)
	cmdDo := findInvokeActionID(t, r1.Commands, "do")
	r2, err := engine.Step(def, r1.State, engine.NewActionCompleted(t0, cmdDo, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.State.RootCompensations, 1, "precondition: svc's compensation must be recorded")

	r3, err := engine.Step(def, r2.State, engine.NewReverseToStart(t0, "start"), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, r3.State.Status, "precondition: walk in flight")
	require.NotEmpty(t, r3.State.Compensating.ActiveCmdID, "precondition: walk is mid-flight (undo not yet completed)")
	midWalk := r3.State

	type testCase struct {
		name    string
		trigger engine.CompensateRequested
		assert  func(t *testing.T, before, result engine.InstanceState, err error)
	}

	cases := []testCase{
		{
			name:    "reverse trigger during active walk errors instead of silently discarding",
			trigger: engine.NewReverseToStart(t0, "start"),
			assert: func(t *testing.T, before, result engine.InstanceState, err error) {
				require.Error(t, err, "a reverse arriving mid-walk must not be silently discarded")
				assert.True(t, strings.HasPrefix(err.Error(), "workflow-engine:"), "error must carry the workflow-engine: sentinel prefix, got %q", err.Error())
				assert.Contains(t, err.Error(), "in flight", "error must explain the rejection is due to an in-flight compensation walk")
				// Step is pure (clones state internally); the caller's original state
				// value must be unaffected by the erroring call.
				assert.Equal(t, before.Status, midWalk.Status, "original state must be unchanged after a rejected reverse")
				assert.Equal(t, before.Compensating, midWalk.Compensating, "original cursor must be unchanged after a rejected reverse")
			},
		},
		{
			// FU#1 sharpened this: WithTargetNode(X) emits NewReverseToNode, which
			// sets RestoreTargetVars (not ReverseNode). Before this case's fix, the
			// mid-walk guard only recognized ReverseNode != "" as facade-originated
			// reverse intent, so a target reverse arriving mid-walk fell through to
			// the silent no-op below — the caller believed ReverseInstance(id,
			// WithTargetNode(X)) succeeded when nothing happened. Must error exactly
			// like the full-reverse case above.
			name:    "target reverse trigger during active walk errors instead of silently discarding",
			trigger: engine.NewReverseToNode(t0, "svc"),
			assert: func(t *testing.T, before, result engine.InstanceState, err error) {
				require.Error(t, err, "a target reverse arriving mid-walk must not be silently discarded")
				assert.True(t, strings.HasPrefix(err.Error(), "workflow-engine:"), "error must carry the workflow-engine: sentinel prefix, got %q", err.Error())
				assert.Contains(t, err.Error(), "in flight", "error must explain the rejection is due to an in-flight compensation walk")
				// Step is pure (clones state internally); the caller's original state
				// value must be unaffected by the erroring call.
				assert.Equal(t, before.Status, midWalk.Status, "original state must be unchanged after a rejected target reverse")
				assert.Equal(t, before.Compensating, midWalk.Compensating, "original cursor must be unchanged after a rejected target reverse")
			},
		},
		{
			name:    "admin/partial trigger during active walk still silently no-ops (unchanged)",
			trigger: engine.NewCompensateRequested(t0, "start"),
			assert: func(t *testing.T, before, result engine.InstanceState, err error) {
				require.NoError(t, err, "admin trigger mid-walk must keep the existing silent no-op")
				// Compare the fields the no-op guard is actually responsible for
				// leaving untouched, rather than full struct equality: a second
				// cloneState pass over an already-empty slice field (e.g. Tokens)
				// collapses "empty-but-non-nil" to nil, which is an unrelated
				// cloneState quirk (append onto nil with zero elements yields nil),
				// not a behavior this guard could ever affect.
				assert.Equal(t, before.Status, result.Status, "admin no-op must not change Status")
				assert.Equal(t, before.Compensating, result.Compensating, "admin no-op must not change the compensation cursor")
				assert.Equal(t, before.RootCompensations, result.RootCompensations, "admin no-op must not change RootCompensations")
				assert.Equal(t, before.Variables, result.Variables, "admin no-op must not change Variables")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := engine.Step(def, midWalk, tc.trigger, engine.StepOptions{})
			tc.assert(t, midWalk, r.State, err)
		})
	}
}

// TestReverseToStart_CancelMidWalk_TerminatesNotResumes is the Fork B (ADR-0109
// hardening, #2) regression: a CancelRequested delivered while a full REVERSE
// walk is mid-flight must PREEMPT the reverse — the instance terminates
// (StatusTerminated + FailInstance{"cancelled"}) instead of resuming Running at
// start.
//
// Today the cancel is silently swallowed: handleCancelRequested only defers
// (sets PendingCancel) when the in-flight walk is a THROW walk (ResumeNode !=
// ""); a reverse walk has ReverseNode set but ResumeNode == "", so the cancel
// hits the documented no-op and the reverse walk finishes into StatusRunning —
// the caller who cancelled is left with a live running instance.
//
// Cancel WINS over reverse (documented semantic call). The fix mirrors the
// existing throw-walk PendingCancel protocol EXACTLY: widen the defer condition
// to also cover reverse walks, and consume PendingCancel on the reverse resume
// path (clearing the already-compensated records so the cancel walk finds zero
// eligible records and terminates directly — no double-compensation).
func TestReverseToStart_CancelMidWalk_TerminatesNotResumes(t *testing.T) {
	def := reverseSvcDef()
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)

	// Drive to an in-flight REVERSE walk: start -> complete "do" (records svc's
	// compensation, parks on "park") -> NewReverseToStart emits the "undo"
	// InvokeAction WITHOUT completing it, so the cursor's ReverseNode is set and
	// ActiveCmdID != "" (mid-reverse-walk).
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, map[string]any{"amount": 100}), engine.StepOptions{})
	require.NoError(t, err)
	cmdDo := findInvokeActionID(t, r1.Commands, "do")

	r2, err := engine.Step(def, r1.State, engine.NewActionCompleted(t0, cmdDo, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.State.RootCompensations, 1, "precondition: svc's compensation recorded")

	r3, err := engine.Step(def, r2.State, engine.NewReverseToStart(t0, "start"), engine.StepOptions{})
	require.NoError(t, err)
	undoID := findInvokeActionID(t, r3.Commands, "undo")
	require.Equal(t, engine.StatusCompensating, r3.State.Status, "precondition: reverse walk in flight")
	require.NotEmpty(t, r3.State.Compensating.ReverseNode, "precondition: in-flight walk is a REVERSE walk (ReverseNode set)")
	require.Empty(t, r3.State.Compensating.ResumeNode, "precondition: a reverse walk has NO ResumeNode (distinguishes it from a throw walk)")
	require.NotEmpty(t, r3.State.Compensating.ActiveCmdID, "precondition: undo not yet completed (mid-walk)")

	// Deliver CancelRequested mid-reverse-walk: it must be DEFERRED (PendingCancel
	// set), not silently swallowed. The walk stays in flight (cancel does not
	// re-enter beginCompensation while the undo is outstanding).
	r4, err := engine.Step(def, r3.State, engine.NewCancelRequested(t0), engine.StepOptions{})
	require.NoError(t, err)
	assert.True(t, r4.State.PendingCancel, "cancel mid-reverse-walk must be DEFERRED (PendingCancel), not silently dropped")
	assert.Equal(t, engine.StatusCompensating, r4.State.Status, "the reverse walk is still in flight; cancel does not pre-empt it yet")

	// Complete the in-flight undo: the deferred cancel now PREEMPTS the reverse.
	// The instance must TERMINATE (StatusTerminated + FailInstance{"cancelled"})
	// instead of resuming Running at start.
	r5, err := engine.Step(def, r4.State, engine.NewActionCompleted(t0, undoID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusTerminated, r5.State.Status, "cancel preempts reverse: instance TERMINATES, NOT resumes Running")
	assert.NotNil(t, r5.State.EndedAt, "terminated instance must stamp EndedAt")
	assert.Empty(t, r5.State.RootCompensations, "records cleared on terminate")
	assert.False(t, r5.State.PendingCancel, "PendingCancel consumed by the preemption")
	assert.Empty(t, r5.State.Tokens, "terminated instance has no live tokens (no resume-at-start token was placed)")

	var failCancelled bool
	for _, c := range r5.Commands {
		if fi, ok := c.(engine.FailInstance); ok && fi.Err == "cancelled" {
			failCancelled = true
		}
	}
	assert.True(t, failCancelled, "cancel-preempted reverse must emit FailInstance{\"cancelled\"}")
}

// reverseWithRootESPDef is modeled on rootLevelESPDef (step_subprocess_test.go)
// but adds a compensable node + trailing park node (the reverse-fixture shape
// used throughout this file), so an instance can be driven into StatusRunning
// (parked, compensation recorded) with a root-level, TIMER-triggered event
// sub-process armed:
//
//	start → svc(compensable) → park → end
//	[KindEventSubProcess "root-esp"] triggered by timer "1h" (root scope, EnclosingScopeID=="")
//	  esp-start(timer "1h") → esp-svc("esp-action") → esp-end
//
// A timer trigger (rather than signal) lets the T4 regression assert a
// ScheduleTimer command is re-emitted on the reverse-resume Step, in addition
// to the EventSubprocesses arm entry itself.
func reverseWithRootESPDef() *model.ProcessDefinition {
	espInner := &model.ProcessDefinition{
		ID: "resp-inner", Version: 1,
		Nodes: []model.Node{
			event.NewStart("esp-start", event.WithStartTimer(schedule.AfterExpr(`"1h"`))),
			activity.NewServiceTask("esp-svc", activity.WithTaskAction("esp-action")),
			event.NewEnd("esp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "re1", Source: "esp-start", Target: "esp-svc"},
			{ID: "re2", Source: "esp-svc", Target: "esp-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p-rev-esp", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithTaskAction("do"), activity.WithCompensateAction("undo")),
			activity.NewServiceTask("park", activity.WithTaskAction("park")),
			event.NewEnd("end"),
			event.NewEventSubProcess("root-esp", espInner),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "park"},
			{ID: "f3", Source: "park", Target: "end"},
		},
	}
}

// TestReverseToStart_RearmsRootEventSubprocess is the Fork D (ADR-0109
// hardening, finding #1) regression: a full reverse (WithFullReverse) resets
// the instance to start and re-runs it, but the resume path never re-arms
// ROOT-scope event sub-processes the way a genuine handleStartInstance does
// (armEventSubprocesses(def, s, "", at, eval)) — so a root-level ESP's timer
// is never re-scheduled relative to the resume. This must be fixed on the
// FULL-REVERSE resume path ONLY (root scope): applyFinish must call
// armEventSubprocesses(def, s, "", at, eval) and prepend its ScheduleTimer
// commands to the drive commands.
func TestReverseToStart_RearmsRootEventSubprocess(t *testing.T) {
	def := reverseWithRootESPDef()
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)

	// ---- Step 1: StartInstance → root ESP arms (EnclosingScopeID == "") ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.EventSubprocesses, 1, "root ESP must arm on StartInstance")
	assert.Equal(t, "", r1.State.EventSubprocesses[0].EnclosingScopeID, "root ESP arm must have empty EnclosingScopeID")
	originalTimerID := r1.State.EventSubprocesses[0].TimerID
	require.NotEmpty(t, originalTimerID, "original (Start-time) root ESP arm must carry a TimerID")

	// ---- Step 2: complete "do" → svc's compensation recorded, token parks on "park" ----
	cmdDo := findInvokeActionID(t, r1.Commands, "do")
	r2, err := engine.Step(def, r1.State, engine.NewActionCompleted(t0, cmdDo, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r2.State.Status, "instance must be running (parked) before reverse")
	require.Len(t, r2.State.RootCompensations, 1, "svc must have recorded a compensation")

	// ---- Step 3: NewReverseToStart → "undo" fires ----
	r3, err := engine.Step(def, r2.State, engine.NewReverseToStart(t0, "start"), engine.StepOptions{})
	require.NoError(t, err)
	undoID := findInvokeActionID(t, r3.Commands, "undo")

	// ---- Step 4: complete "undo" → resume at start (re-arm must happen HERE) ----
	r4, err := engine.Step(def, r3.State, engine.NewActionCompleted(t0, undoID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r4.State.Status, "reverse resumes Running, NOT terminated")

	require.Len(t, r4.State.EventSubprocesses, 1, "root ESP must be re-armed after full reverse (found %d arms)", len(r4.State.EventSubprocesses))
	assert.Equal(t, "", r4.State.EventSubprocesses[0].EnclosingScopeID, "re-armed ESP entry must carry EnclosingScopeID == \"\" (root)")

	var schedTimer engine.ScheduleTimer
	foundTimer := false
	for _, cmd := range r4.Commands {
		if st, ok := cmd.(engine.ScheduleTimer); ok {
			schedTimer = st
			foundTimer = true
		}
	}
	assert.True(t, foundTimer, "resume must re-schedule the root ESP's timer (ScheduleTimer command)")
	assert.NotEmpty(t, schedTimer.TimerID, "re-scheduled timer must carry a TimerID")
	assert.Equal(t, r4.State.EventSubprocesses[0].TimerID, schedTimer.TimerID, "re-armed arm's TimerID must match the emitted ScheduleTimer")

	// The ORIGINAL (Start-time) root-ESP arm is never swept by the compensation
	// walk itself (cancelAllArmsAndBoundaries does not touch EventSubprocesses),
	// so it survives until applyFinish's sweep-before-rearm. That stale timer
	// must be explicitly cancelled — otherwise it leaks and can fire against the
	// reversed instance. Assert the sweep actually emits CancelTimer for it (not
	// just that a fresh timer appears), and that the re-armed arm carries a NEW,
	// different TimerID rather than reusing the stale one.
	assert.Contains(t, r4.Commands, engine.CancelTimer{TimerID: originalTimerID},
		"full-reverse re-arm must cancel the stale original root-ESP timer")
	assert.NotEqual(t, originalTimerID, r4.State.EventSubprocesses[0].TimerID,
		"re-armed root-ESP arm must carry a freshly minted TimerID, not the stale original")
}

// TestCompensateRequested_ResetVarsWithoutReverseNode is the T6 (ADR-0109
// hardening, finding #5) regression: CompensateRequested is a public,
// directly-constructible struct, so a caller can build one by hand expressing
// reverse intent (ResetVars: true) while leaving ReverseNode empty — e.g.
// engine.CompensateRequested{ResetVars: true} — instead of going through
// NewReverseToStart. Today stepCompensateRequested ignores ResetVars whenever
// ReverseNode == "" and falls through to the full-rollback TERMINATE branch,
// silently discarding the caller's intent to resume rather than terminate.
// This is the engine-level twin of the WithTargetNode("") footgun already
// guarded at the runtime facade (see runtime/processdriver_reverse.go). The
// combination must instead be rejected with a workflow-engine: error.
//
// A well-formed NewReverseToStart trigger (ReverseNode AND ResetVars both
// set) and a plain NewCompensateRequested (ResetVars left false) must both be
// completely unaffected by this guard — proven by the sibling tests in this
// file that continue to exercise those two shapes end-to-end.
func TestCompensateRequested_ResetVarsWithoutReverseNode(t *testing.T) {
	def := reverseSvcDef()
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)

	// Drive to a stable Running (parked) state carrying one compensation
	// record — the same precondition shape as the other reverse regressions
	// in this file, so the guard is proven against a realistic state rather
	// than a bare zero-value InstanceState.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, map[string]any{"amount": 100}), engine.StepOptions{})
	require.NoError(t, err)
	cmdDo := findInvokeActionID(t, r1.Commands, "do")
	r2, err := engine.Step(def, r1.State, engine.NewActionCompleted(t0, cmdDo, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r2.State.Status, "precondition: instance running (parked) before the malformed trigger")
	require.Len(t, r2.State.RootCompensations, 1, "precondition: svc's compensation recorded")
	before := r2.State

	// The malformed trigger under test: ResetVars set, ReverseNode left empty.
	// NewCompensateRequested only exposes the well-formed shapes, so the
	// footgun is built the same way an errant caller would build it — a raw
	// struct literal starting from the constructor's zero-ReverseNode result
	// and flipping the exported ResetVars field directly.
	malformed := engine.NewCompensateRequested(t0, "")
	malformed.ResetVars = true
	require.Empty(t, malformed.ReverseNode, "precondition: trigger under test has ResetVars set but ReverseNode empty")

	// Snapshot the caller's observable state BEFORE the erroring Step so the
	// no-mutation assertions below compare against an independent baseline
	// (before is the very value passed into Step, so comparing it to itself
	// afterwards would be tautological).
	wantStatus := before.Status
	wantRecords := append([]engine.CompensationRecord(nil), before.RootCompensations...)

	_, err = engine.Step(def, before, malformed, engine.StepOptions{})
	require.Error(t, err, "ResetVars without ReverseNode must be rejected, not silently terminate the instance")
	assert.True(t, strings.HasPrefix(err.Error(), "workflow-engine:"), "error must carry the workflow-engine: sentinel prefix, got %q", err.Error())
	assert.Contains(t, err.Error(), "ResetVars", "error must name the offending field")
	assert.Contains(t, err.Error(), "ReverseNode", "error must name the missing field")

	// Step is pure (clones state internally); the caller's original state value
	// must be unaffected by the erroring call — in particular it must NOT have
	// been silently terminated or had its records mutated in place.
	assert.Equal(t, wantStatus, before.Status, "caller's state must be unchanged (still Running) after a rejected trigger")
	assert.Equal(t, wantRecords, before.RootCompensations, "caller's compensation records must be unchanged after a rejected trigger")
}

// TestCompensateRequested_RestoreTargetVarsWithoutToNode is the F1.2 (FU#1)
// regression: CompensateRequested is a public, directly-constructible struct,
// so a caller can build one by hand expressing target-reverse intent
// (RestoreTargetVars: true) while leaving ToNode empty — e.g.
// engine.CompensateRequested{RestoreTargetVars: true} — instead of going
// through NewReverseToNode. RestoreTargetVars restores Variables to ToNode's
// own start-of-visit snapshot, so without a ToNode there is nothing to look
// up: today stepCompensateRequested ignores RestoreTargetVars whenever
// ToNode == "" and falls through to the full-rollback TERMINATE branch,
// silently discarding the caller's intent. This is the sibling footgun to the
// ResetVars-without-ReverseNode guard proven above; the combination must
// instead be rejected with a workflow-engine: error.
//
// A well-formed NewReverseToNode trigger (ToNode AND RestoreTargetVars both
// set) and a plain NewCompensateRequested (RestoreTargetVars left false) must
// both be completely unaffected by this guard.
func TestCompensateRequested_RestoreTargetVarsWithoutToNode(t *testing.T) {
	def := reverseSvcDef()
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)

	// Drive to a stable Running (parked) state carrying one compensation
	// record — same precondition shape as the ResetVars-without-ReverseNode
	// regression above.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, map[string]any{"amount": 100}), engine.StepOptions{})
	require.NoError(t, err)
	cmdDo := findInvokeActionID(t, r1.Commands, "do")
	r2, err := engine.Step(def, r1.State, engine.NewActionCompleted(t0, cmdDo, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r2.State.Status, "precondition: instance running (parked) before the malformed trigger")
	require.Len(t, r2.State.RootCompensations, 1, "precondition: svc's compensation recorded")
	before := r2.State

	// The malformed trigger under test: RestoreTargetVars set, ToNode left
	// empty. NewCompensateRequested only exposes the well-formed shapes, so
	// the footgun is built the same way an errant caller would build it — a
	// raw struct literal starting from the constructor's zero-ToNode result
	// and flipping the exported RestoreTargetVars field directly.
	malformed := engine.NewCompensateRequested(t0, "")
	malformed.RestoreTargetVars = true
	require.Empty(t, malformed.ToNode, "precondition: trigger under test has RestoreTargetVars set but ToNode empty")

	// Snapshot the caller's observable state BEFORE the erroring Step so the
	// no-mutation assertions below compare against an independent baseline.
	wantStatus := before.Status
	wantRecords := append([]engine.CompensationRecord(nil), before.RootCompensations...)

	_, err = engine.Step(def, before, malformed, engine.StepOptions{})
	require.Error(t, err, "RestoreTargetVars without ToNode must be rejected, not silently terminate the instance")
	assert.True(t, strings.HasPrefix(err.Error(), "workflow-engine:"), "error must carry the workflow-engine: sentinel prefix, got %q", err.Error())
	assert.Contains(t, err.Error(), "RestoreTargetVars", "error must name the offending field")
	assert.Contains(t, err.Error(), "ToNode", "error must name the missing field")

	// Step is pure (clones state internally); the caller's original state value
	// must be unaffected by the erroring call.
	assert.Equal(t, wantStatus, before.Status, "caller's state must be unchanged (still Running) after a rejected trigger")
	assert.Equal(t, wantRecords, before.RootCompensations, "caller's compensation records must be unchanged after a rejected trigger")
}

// TestCancelRequestedTerminate_CancelsRootEventSubprocessTimer is the FU#2 /
// Task F2.2 regression: a CancelRequested arriving while compensation records
// exist runs the compensation walk to a TERMINATE finish (applyTerminate,
// step_compensation.go), which sweeps s.Timers and s.ArmedEvents/s.Boundaries
// (cancelAllTimers / cancelAllArmsAndBoundaries) but — before this fix — never
// drained s.EventSubprocesses, so a root-level, timer-armed event sub-process
// (armed at StartInstance, never touched by the compensation walk itself)
// leaked its scheduled timer past instance termination. applyTerminate must
// also call s.removeAllEventSubprocessArms() and emit a CancelTimer for the
// surviving arm.
func TestCancelRequestedTerminate_CancelsRootEventSubprocessTimer(t *testing.T) {
	def := reverseWithRootESPDef()
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)

	// ---- Step 1: StartInstance → root ESP arms (EnclosingScopeID == "") ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.EventSubprocesses, 1, "root ESP must arm on StartInstance")
	espTimerID := r1.State.EventSubprocesses[0].TimerID
	require.NotEmpty(t, espTimerID, "root ESP arm must carry a TimerID")

	// ---- Step 2: complete "do" → svc's compensation recorded, token parks on "park" ----
	cmdDo := findInvokeActionID(t, r1.Commands, "do")
	r2, err := engine.Step(def, r1.State, engine.NewActionCompleted(t0, cmdDo, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r2.State.Status, "precondition: instance running (parked) before cancel")
	require.Len(t, r2.State.RootCompensations, 1, "precondition: svc's compensation recorded")

	// ---- Step 3: CancelRequested → records exist → compensation walk begins ("undo" fires) ----
	r3, err := engine.Step(def, r2.State, engine.NewCancelRequested(t0), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, r3.State.Status, "precondition: cancel with records enters the compensation walk")
	undoID := findInvokeActionID(t, r3.Commands, "undo")

	// ---- Step 4: complete "undo" → walk finishes → applyTerminate ----
	r4, err := engine.Step(def, r3.State, engine.NewActionCompleted(t0, undoID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusTerminated, r4.State.Status, "cancel-with-compensation walk still terminates")
	assert.Nil(t, r4.State.EventSubprocesses, "applyTerminate must drain ALL event-subprocess arms, not just the walk's own scope")
	assert.Contains(t, r4.Commands, engine.CancelTimer{TimerID: espTimerID},
		"applyTerminate must cancel the surviving root-ESP timer, not just the walk's own timers/arms")
}

// TestImmediateTerminatePaths_CancelRootEventSubprocessTimer is the FU#2 /
// Task F2.3 regression: the two "immediate" (no compensation records)
// terminal paths — handleCancelRequested's no-records branch (step_triggers.go)
// and propagateError's no-records unhandled-error branch (step_errors.go) —
// both sweep s.Timers and s.ArmedEvents/s.Boundaries but, before this fix,
// never drained s.EventSubprocesses, leaking a root-level timer-armed event
// sub-process's scheduled timer past instance termination/failure.
func TestImmediateTerminatePaths_CancelRootEventSubprocessTimer(t *testing.T) {
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name   string
		drive  func(t *testing.T, def *model.ProcessDefinition, r1 engine.StepResult) (engine.StepResult, error)
		assert func(t *testing.T, r engine.StepResult, espTimerID string)
	}

	cases := []testCase{
		{
			name: "immediate cancel with no compensation records",
			drive: func(t *testing.T, def *model.ProcessDefinition, r1 engine.StepResult) (engine.StepResult, error) {
				t.Helper()
				return engine.Step(def, r1.State, engine.NewCancelRequested(t0), engine.StepOptions{})
			},
			assert: func(t *testing.T, r engine.StepResult, espTimerID string) {
				assert.Equal(t, engine.StatusTerminated, r.State.Status, "immediate cancel (no records) still terminates")
				assert.Nil(t, r.State.EventSubprocesses, "immediate-cancel path must drain ALL event-subprocess arms")
				assert.Contains(t, r.Commands, engine.CancelTimer{TimerID: espTimerID},
					"immediate-cancel path must cancel the surviving root-ESP timer")
			},
		},
		{
			name: "immediate unhandled action failure with no compensation records",
			drive: func(t *testing.T, def *model.ProcessDefinition, r1 engine.StepResult) (engine.StepResult, error) {
				t.Helper()
				cmdDo := findInvokeActionID(t, r1.Commands, "do")
				return engine.Step(def, r1.State, engine.NewActionFailed(t0, cmdDo, "boom", false), engine.StepOptions{})
			},
			assert: func(t *testing.T, r engine.StepResult, espTimerID string) {
				assert.Equal(t, engine.StatusFailed, r.State.Status, "unhandled root-level failure (no records) still fails the instance")
				assert.Nil(t, r.State.EventSubprocesses, "immediate-fail path must drain ALL event-subprocess arms")
				assert.Contains(t, r.Commands, engine.CancelTimer{TimerID: espTimerID},
					"immediate-fail path must cancel the surviving root-ESP timer")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := reverseWithRootESPDef()

			// ---- StartInstance → root ESP arms; "do" left uncompleted so NO
			// compensation record exists yet — the precondition for both
			// "immediate" (no-records) branches under test. ----
			r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
				engine.NewStartInstance(t0, nil), engine.StepOptions{})
			require.NoError(t, err)
			require.Len(t, r1.State.EventSubprocesses, 1, "root ESP must arm on StartInstance")
			espTimerID := r1.State.EventSubprocesses[0].TimerID
			require.NotEmpty(t, espTimerID, "root ESP arm must carry a TimerID")
			require.Empty(t, r1.State.RootCompensations, "precondition: no compensation records yet")

			r2, err := tc.drive(t, def, r1)
			require.NoError(t, err)
			tc.assert(t, r2, espTimerID)
		})
	}
}

// rootESPWithCallActivityDef: start → call-activity → end, plus a root-scope
// event sub-process armed with a timer, so SubInstanceFailed can be driven
// while a root ESP timer is outstanding.
func rootESPWithCallActivityDef() *model.ProcessDefinition {
	espInner := &model.ProcessDefinition{
		ID: "resp-inner-ca", Version: 1,
		Nodes: []model.Node{
			event.NewStart("esp-start", event.WithStartTimer(schedule.AfterExpr(`"1h"`))),
			activity.NewServiceTask("esp-svc", activity.WithTaskAction("esp-action")),
			event.NewEnd("esp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "re1", Source: "esp-start", Target: "esp-svc"},
			{ID: "re2", Source: "esp-svc", Target: "esp-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p-ca-esp", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewCallActivity("call", model.Latest("child")),
			event.NewEnd("end"),
			event.NewEventSubProcess("root-esp", espInner),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "call"},
			{ID: "f2", Source: "call", Target: "end"},
		},
	}
}

// TestSubInstanceFailedTerminate_CancelsRootEventSubprocessTimer is the FU#2 /
// Task F2.4 regression (a 4th terminal site found by whole-branch review):
// handleSubInstanceFailed (a parent instance fails because a child
// call-activity's sub-instance failed) sweeps s.Timers and
// s.ArmedEvents/s.Boundaries via cancelAllTimers + cancelAllArmsAndBoundaries
// but, before this fix, never drained s.EventSubprocesses — leaking a
// root-level, timer-armed event sub-process's scheduled timer past this
// terminal path, the same class of leak FU#2 fixed at the other three sites.
func TestSubInstanceFailedTerminate_CancelsRootEventSubprocessTimer(t *testing.T) {
	t0 := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	def := rootESPWithCallActivityDef()

	// ---- StartInstance → root ESP arms; call-activity parks awaiting the child. ----
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.EventSubprocesses, 1, "root ESP must arm on StartInstance")
	espTimerID := r1.State.EventSubprocesses[0].TimerID
	require.NotEmpty(t, espTimerID, "root ESP arm must carry a TimerID")

	var ssiCmdID string
	for _, cmd := range r1.Commands {
		if ssi, ok := cmd.(engine.StartSubInstance); ok {
			ssiCmdID = ssi.CommandID
		}
	}
	require.NotEmpty(t, ssiCmdID, "expected StartSubInstance for the call-activity")

	// ---- SubInstanceFailed → parent fails; the root ESP timer must be cancelled too. ----
	r2, err := engine.Step(def, r1.State,
		engine.NewSubInstanceFailed(t0.Add(time.Second), ssiCmdID, "child blew up"), engine.StepOptions{})
	require.NoError(t, err)

	assert.Equal(t, engine.StatusFailed, r2.State.Status, "parent must fail on SubInstanceFailed")
	assert.Nil(t, r2.State.EventSubprocesses, "SubInstanceFailed terminate must drain ALL event-subprocess arms")
	assert.Contains(t, r2.Commands, engine.CancelTimer{TimerID: espTimerID},
		"SubInstanceFailed terminate must cancel the surviving root-ESP timer")
}

// TestReverseToNode_RestoresTargetStartOfVisitVariables (FU#1, Task F1.3) pins the
// CORE contract: a NewReverseToNode(at, X) — carrying RestoreTargetVars — restores
// s.Variables to X's OWN start-of-visit snapshot (the Input captured on X's
// compensation record, i.e. the variables as they stood when execution first
// arrived at X, before X ran) when the partial-rollback walk resumes at X. A raw
// admin NewCompensateRequested(at, X) leaves the current variables untouched.
//
// The run mutates variables on each compensable step's completion so every node's
// record Input is a DISTINCT snapshot:
//
//	step1.Input = {a:1}             (start vars, before step1's output merges)
//	step2.Input = {a:9,b:2}         (after step1 output {a:9,b:2})
//	step3.Input = {a:9,b:2,c:3}     (after step2 output {c:3})
//	parked vars = {a:9,b:2,c:3,d:4} (after step3 output {d:4})
func TestReverseToNode_RestoresTargetStartOfVisitVariables(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)

	// parked drives threeCompensableDef to a Running state parked at the user task
	// with three RootCompensations, each recording a distinct start-of-visit Input.
	parked := func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState) {
		t.Helper()
		def := threeCompensableDef()
		r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i"},
			engine.NewStartInstance(t0, map[string]any{"a": 1}), engine.StepOptions{})
		require.NoError(t, err)
		id1 := findInvokeActionID(t, r1.Commands, "a1")
		r2, err := engine.Step(def, r1.State,
			engine.NewActionCompleted(t0, id1, map[string]any{"a": 9, "b": 2}), engine.StepOptions{})
		require.NoError(t, err)
		id2 := findInvokeActionID(t, r2.Commands, "a2")
		r3, err := engine.Step(def, r2.State,
			engine.NewActionCompleted(t0, id2, map[string]any{"c": 3}), engine.StepOptions{})
		require.NoError(t, err)
		id3 := findInvokeActionID(t, r3.Commands, "a3")
		r4, err := engine.Step(def, r3.State,
			engine.NewActionCompleted(t0, id3, map[string]any{"d": 4}), engine.StepOptions{})
		require.NoError(t, err)
		require.Equal(t, engine.StatusRunning, r4.State.Status)
		require.Len(t, r4.State.RootCompensations, 3, "step1+step2+step3 recorded")
		require.Equal(t, map[string]any{"a": 1}, r4.State.RootCompensations[0].Input)
		require.Equal(t, map[string]any{"a": 9, "b": 2, "c": 3, "d": 4}, r4.State.Variables)
		return def, r4.State
	}

	// driveToFinish delivers trig then completes each one-at-a-time compensation
	// undo action until the walk finishes (Status leaves StatusCompensating).
	driveToFinish := func(t *testing.T, def *model.ProcessDefinition, state engine.InstanceState, trig engine.CompensateRequested) engine.InstanceState {
		t.Helper()
		r, err := engine.Step(def, state, trig, engine.StepOptions{})
		require.NoError(t, err)
		for r.State.Status == engine.StatusCompensating {
			var undoID string
			for _, c := range r.Commands {
				if ia, ok := c.(engine.InvokeAction); ok {
					undoID = ia.CommandID
				}
			}
			require.NotEmpty(t, undoID, "a compensation InvokeAction must be in flight while compensating")
			r, err = engine.Step(def, r.State,
				engine.NewActionCompleted(t0, undoID, nil), engine.StepOptions{})
			require.NoError(t, err)
		}
		return r.State
	}

	type testCase struct {
		name   string
		trig   engine.CompensateRequested
		assert func(t *testing.T, resumed engine.InstanceState)
	}

	cases := []testCase{
		{
			name: "reverse to step1 restores step1 start-of-visit vars",
			trig: engine.NewReverseToNode(t0, "step1"),
			assert: func(t *testing.T, resumed engine.InstanceState) {
				assert.Equal(t, engine.StatusRunning, resumed.Status)
				require.Len(t, resumed.Tokens, 1)
				assert.Equal(t, "step1", resumed.Tokens[0].NodeID, "resumes at target node")
				assert.Equal(t, map[string]any{"a": 1}, resumed.Variables,
					"vars restored to step1's start-of-visit Input, NOT the current {a:9,b:2,c:3,d:4}")
			},
		},
		{
			name: "reverse to step3 early-finish restores step3 start-of-visit vars",
			trig: engine.NewReverseToNode(t0, "step3"),
			assert: func(t *testing.T, resumed engine.InstanceState) {
				assert.Equal(t, engine.StatusRunning, resumed.Status)
				require.Len(t, resumed.Tokens, 1)
				assert.Equal(t, "step3", resumed.Tokens[0].NodeID, "resumes at target node")
				assert.Equal(t, map[string]any{"a": 9, "b": 2, "c": 3}, resumed.Variables,
					"early-finish (nothing above step3) still restores step3's Input; d dropped")
			},
		},
		{
			name: "admin partial rollback to step1 keeps current vars",
			trig: engine.NewCompensateRequested(t0, "step1"),
			assert: func(t *testing.T, resumed engine.InstanceState) {
				assert.Equal(t, engine.StatusRunning, resumed.Status)
				require.Len(t, resumed.Tokens, 1)
				assert.Equal(t, "step1", resumed.Tokens[0].NodeID)
				assert.Equal(t, map[string]any{"a": 9, "b": 2, "c": 3, "d": 4}, resumed.Variables,
					"admin partial rollback leaves current variables UNCHANGED")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			def, state := parked(t)
			resumed := driveToFinish(t, def, state, tc.trig)
			tc.assert(t, resumed)
		})
	}
}
