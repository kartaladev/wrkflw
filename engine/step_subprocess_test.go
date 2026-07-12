package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
)

// subProcessDef builds an outer definition:
//
//	outer-start → sub (KindSubProcess, Subprocess = inner) → outer-end
//
// inner definition:
//
//	inner-start → inner-svc (ServiceTask "inner-action") → inner-end
func subProcessDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "inner", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithTaskAction("inner-action")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			activity.NewSubProcess("sub", inner),
			event.NewEnd("outer-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "of1", Source: "outer-start", Target: "sub"},
			{ID: "of2", Source: "sub", Target: "outer-end"},
		},
	}
}

// TestEmbeddedSubProcessRunsAndContinues is the primary scenario test:
//
//  1. StartInstance drives: outer-start → sub (entry: opens scope, inner-start →
//     inner-svc fires InvokeAction for "inner-action").
//  2. ActionCompleted for inner-svc drives: inner-svc → inner-end (inner scope
//     drains, scope closed) → outer flow resumes: outer-end → CompleteInstance.
//
// Asserts:
//   - After StartInstance: exactly one InvokeAction for "inner-action".
//   - A scope was opened (len(Scopes)==1 after entry, ==0 after exit).
//   - The inner token carries the scope ID.
//   - After ActionCompleted: instance StatusCompleted, exactly one CompleteInstance.
func TestEmbeddedSubProcessRunsAndContinues(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := subProcessDef()

	// ---- Step 1: StartInstance ----
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// Exactly one InvokeAction for the inner service task.
	require.Len(t, r1.Commands, 1, "expected exactly one command after start")
	ia, ok := r1.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "expected InvokeAction, got %T", r1.Commands[0])
	assert.Equal(t, "inner-action", ia.Name)

	// One token: the inner-svc token, parked, in the sub-process scope.
	require.Len(t, r1.State.Tokens, 1)
	innerTok := r1.State.Tokens[0]
	assert.Equal(t, "inner-svc", innerTok.NodeID)
	assert.Equal(t, engine.TokenWaitingCommand, innerTok.State)
	assert.NotEmpty(t, innerTok.ScopeID, "inner token must carry a scope ID")

	// Exactly one scope is open.
	require.Len(t, r1.State.Scopes, 1, "expected one open scope after sub-process entry")
	scope := r1.State.Scopes[0]
	assert.Equal(t, "sub", scope.NodeID, "scope.NodeID must be the sub-process activity node")
	assert.Equal(t, "", scope.ParentID, "scope.ParentID must be empty (root parent)")
	assert.Equal(t, innerTok.ScopeID, scope.ID)

	// ---- Step 2: ActionCompleted for inner-svc ----
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(at.Add(time.Second), ia.CommandID, map[string]any{"result": "done"}),
		engine.StepOptions{})
	require.NoError(t, err)

	// Instance must be completed.
	assert.Equal(t, engine.StatusCompleted, r2.State.Status)
	assert.Empty(t, r2.State.Tokens, "all tokens must be consumed on completion")
	assert.Empty(t, r2.State.Scopes, "scope must be closed after sub-process exits")
	require.NotNil(t, r2.State.EndedAt)

	// Exactly one CompleteInstance command.
	require.Len(t, r2.Commands, 1, "expected exactly one command on completion")
	_, ok = r2.Commands[0].(engine.CompleteInstance)
	require.True(t, ok, "expected CompleteInstance, got %T", r2.Commands[0])
}

// TestEmbeddedSubProcessTokenTagging verifies that:
//   - Outer (root-scope) tokens carry an empty ScopeID.
//   - Inner tokens carry the sub-process scope ID.
//
// This is also covered by the main test above but verified explicitly here as a
// focused assertion.
func TestEmbeddedSubProcessTokenTagging(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := subProcessDef()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i2"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, r1.State.Tokens, 1)
	tok := r1.State.Tokens[0]

	require.Len(t, r1.State.Scopes, 1)
	scopeID := r1.State.Scopes[0].ID

	assert.NotEmpty(t, tok.ScopeID, "inner token ScopeID must not be empty")
	assert.Equal(t, scopeID, tok.ScopeID, "inner token ScopeID must match the open scope")
}

// TestEmbeddedSubProcessScopeIDFormat verifies the scope ID follows the
// deterministic "<instanceID>-s<N>" format established by openScope.
func TestEmbeddedSubProcessScopeIDFormat(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := subProcessDef()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "proc-42"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, r1.State.Scopes, 1)
	assert.Equal(t, "proc-42-s1", r1.State.Scopes[0].ID)
}

// parallelSubProcessDef builds an outer definition:
//
//	outer-start → sub (KindSubProcess, Subprocess = inner) → outer-end
//
// inner definition (parallel fork-join):
//
//	inner-start → pfork (parallel gateway, diverging) → inner-a, inner-b (ServiceTasks)
//	inner-a → pjoin (parallel gateway, converging)
//	inner-b → pjoin
//	pjoin → inner-end
func parallelSubProcessDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "inner-parallel", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			gateway.NewParallel("pfork"),
			activity.NewServiceTask("inner-a", activity.WithTaskAction("action-a")),
			activity.NewServiceTask("inner-b", activity.WithTaskAction("action-b")),
			gateway.NewParallel("pjoin"),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "pfork"},
			{ID: "if2", Source: "pfork", Target: "inner-a"},
			{ID: "if3", Source: "pfork", Target: "inner-b"},
			{ID: "if4", Source: "inner-a", Target: "pjoin"},
			{ID: "if5", Source: "inner-b", Target: "pjoin"},
			{ID: "if6", Source: "pjoin", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-parallel", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			activity.NewSubProcess("sub", inner),
			event.NewEnd("outer-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "of1", Source: "outer-start", Target: "sub"},
			{ID: "of2", Source: "sub", Target: "outer-end"},
		},
	}
}

// TestParallelGatewayInsideSubProcess verifies that a parallel fork-join nested
// inside a sub-process keeps all forked tokens in the sub-process scope.
//
// Topology: outer-start → sub [inner-start → pfork → (inner-a ∥ inner-b) → pjoin → inner-end] → outer-end
//
// Expected RED (before fix): forked tokens have ScopeID="" → resolve against top def
// → wrong routing / premature scope-drain / error.
// Expected GREEN (after fix): forked tokens tagged with sub-process ScopeID; both
// service tasks invoke within scope; join fires within scope; scope drains; outer completes.
func TestParallelGatewayInsideSubProcess(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := parallelSubProcessDef()

	// ---- Step 1: StartInstance — drives outer-start → sub → inner-start → pfork → forks to (inner-a, inner-b) ----
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "pi1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// Exactly one scope must be open.
	require.Len(t, r1.State.Scopes, 1, "sub-process scope must be open")
	scopeID := r1.State.Scopes[0].ID
	assert.Equal(t, "sub", r1.State.Scopes[0].NodeID)

	// Exactly two tokens: one parked at inner-a, one parked at inner-b.
	require.Len(t, r1.State.Tokens, 2, "parallel fork must produce two tokens")

	nodeIDs := []string{r1.State.Tokens[0].NodeID, r1.State.Tokens[1].NodeID}
	assert.ElementsMatch(t, []string{"inner-a", "inner-b"}, nodeIDs, "forked tokens must land on inner-a and inner-b")

	// CRITICAL: both forked tokens must carry the sub-process ScopeID.
	for _, tok := range r1.State.Tokens {
		assert.Equal(t, scopeID, tok.ScopeID,
			"forked token at %q must carry sub-process ScopeID %q, got %q", tok.NodeID, scopeID, tok.ScopeID)
		assert.Equal(t, engine.TokenWaitingCommand, tok.State)
	}

	// Exactly two InvokeAction commands: one for action-a, one for action-b.
	require.Len(t, r1.Commands, 2, "expected two InvokeAction commands after parallel fork")
	cmdsByName := make(map[string]string) // action name → commandID
	for _, cmd := range r1.Commands {
		ia, ok := cmd.(engine.InvokeAction)
		require.True(t, ok, "expected InvokeAction, got %T", cmd)
		cmdsByName[ia.Name] = ia.CommandID
	}
	assert.Contains(t, cmdsByName, "action-a")
	assert.Contains(t, cmdsByName, "action-b")

	// ---- Step 2: Complete action-a ----
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(at.Add(time.Second), cmdsByName["action-a"], nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status, "instance still running after first branch completes")
	// scope still open; inner-b still parked.
	require.Len(t, r2.State.Scopes, 1, "scope must still be open after first branch completes")
	assert.Empty(t, r2.Commands, "no commands expected while waiting for inner-b")

	// ---- Step 3: Complete action-b — join fires, scope drains, outer resumes, instance completes ----
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), cmdsByName["action-b"], nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r3.State.Status, "instance must complete after join and scope drain")
	assert.Empty(t, r3.State.Tokens, "all tokens must be consumed on completion")
	assert.Empty(t, r3.State.Scopes, "scope must be closed after sub-process exits")
	require.NotNil(t, r3.State.EndedAt)

	require.Len(t, r3.Commands, 1, "expected exactly one CompleteInstance command")
	_, ok := r3.Commands[0].(engine.CompleteInstance)
	require.True(t, ok, "expected CompleteInstance, got %T", r3.Commands[0])
}

// ---- Call Activity tests ----

// callActivityDef builds a parent definition:
//
//	parent-start → call (KindCallActivity, DefRef:"child") → parent-end
//
// The child definition is referenced by DefRef only; the engine does not need
// the actual child definition (it just emits StartSubInstance with the DefRef).
func callActivityDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "parent", Version: 1,
		Nodes: []model.Node{
			event.NewStart("parent-start"),
			activity.NewCallActivity("call", model.Latest("child")),
			event.NewEnd("parent-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "pf1", Source: "parent-start", Target: "call"},
			{ID: "pf2", Source: "call", Target: "parent-end"},
		},
	}
}

// TestCallActivityEmitsStartSubInstanceAndParks verifies that the engine:
//  1. On StartInstance: drives to the call-activity node, emits a StartSubInstance
//     command, and parks the token (TokenWaitingCommand, AwaitCommand == CommandID).
//  2. On SubInstanceCompleted: merges Output into vars, resumes the token past the
//     call-activity node, drives to parent-end → CompleteInstance.
func TestCallActivityEmitsStartSubInstanceAndParks(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := callActivityDef()

	// ---- Step 1: StartInstance ----
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "ca-i1"},
		engine.NewStartInstance(at, map[string]any{"x": 1}), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// Exactly one StartSubInstance command must have been emitted.
	require.Len(t, r1.Commands, 1, "expected exactly one command after start (StartSubInstance)")
	ssi, ok := r1.Commands[0].(engine.StartSubInstance)
	require.True(t, ok, "expected StartSubInstance, got %T", r1.Commands[0])
	assert.Equal(t, model.Latest("child"), ssi.DefRef)
	assert.NotEmpty(t, ssi.CommandID, "StartSubInstance.CommandID must be non-empty")

	// Input must be a copy of the parent variables.
	assert.Equal(t, map[string]any{"x": 1}, ssi.Input)

	// Token must be parked at the call-activity node with AwaitCommand == CommandID.
	require.Len(t, r1.State.Tokens, 1)
	tok := r1.State.Tokens[0]
	assert.Equal(t, "call", tok.NodeID)
	assert.Equal(t, engine.TokenWaitingCommand, tok.State)
	assert.Equal(t, ssi.CommandID, tok.AwaitCommand)

	// No scope opened (call-activity is a separate instance, not an embedded scope).
	assert.Empty(t, r1.State.Scopes, "call-activity must not open a scope")

	// ---- Step 2: SubInstanceCompleted ----
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSubInstanceCompleted(at.Add(time.Second), ssi.CommandID, map[string]any{"result": "done"}),
		engine.StepOptions{})
	require.NoError(t, err)

	// Instance must be completed.
	assert.Equal(t, engine.StatusCompleted, r2.State.Status)
	assert.Empty(t, r2.State.Tokens, "all tokens must be consumed on completion")
	require.NotNil(t, r2.State.EndedAt)

	// Child output must be merged into parent variables.
	assert.Equal(t, "done", r2.State.Variables["result"])
	assert.Equal(t, 1, r2.State.Variables["x"], "original parent vars must be retained")

	// Exactly one CompleteInstance command.
	require.Len(t, r2.Commands, 1)
	_, ok = r2.Commands[0].(engine.CompleteInstance)
	require.True(t, ok, "expected CompleteInstance, got %T", r2.Commands[0])
}

// TestCallActivitySubInstanceFailedFailsParent verifies that SubInstanceFailed
// (with a matching CommandID) transitions the parent instance to StatusFailed and
// emits FailInstance + cancellation commands.
func TestCallActivitySubInstanceFailedFailsParent(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := callActivityDef()

	// ---- Step 1: StartInstance → parks at call ----
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "ca-i2"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1)
	ssi, ok := r1.Commands[0].(engine.StartSubInstance)
	require.True(t, ok)

	// ---- Step 2: SubInstanceFailed ----
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSubInstanceFailed(at.Add(time.Second), ssi.CommandID, "child blew up"),
		engine.StepOptions{})
	require.NoError(t, err)

	// Parent must be failed.
	assert.Equal(t, engine.StatusFailed, r2.State.Status)
	require.NotNil(t, r2.State.EndedAt)

	// FailInstance must be the first command.
	require.NotEmpty(t, r2.Commands)
	fi, ok := r2.Commands[0].(engine.FailInstance)
	require.True(t, ok, "expected FailInstance, got %T", r2.Commands[0])
	assert.Contains(t, fi.Err, "child blew up")
}

// TestSubInstanceCompletedUnknownCommandID verifies that a SubInstanceCompleted
// with an unrecognised CommandID returns ErrTokenNotFound (mirrors ActionCompleted).
func TestSubInstanceCompletedUnknownCommandID(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := callActivityDef()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "ca-i3"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	_, err = engine.Step(t.Context(), def, r1.State,
		engine.NewSubInstanceCompleted(at.Add(time.Second), "nonexistent-cmd", nil),
		engine.StepOptions{})
	require.Error(t, err)
	require.ErrorIs(t, err, engine.ErrTokenNotFound)
}

// callActivityWithParallelUserTaskDef builds a definition where a parallel gateway
// splits into two concurrent branches:
//
//	Branch A: user-task with deadline "1h" → merge-join (adds a timerRecord to Timers)
//	Branch B: call-activity (DefRef "child") → merge-join
//	merge-join (parallel, converging) → end
//
// The deadline timer on the user-task records a timerRecord in state.Timers.
// When SubInstanceFailed arrives for the call-activity, cancelAllTimers must
// emit CancelTimer for the deadline timer — proving the cleanup path is complete.
func callActivityWithParallelUserTaskDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "ca-sla-parent", Version: 1,
		Nodes: []model.Node{
			event.NewStart("p-start"),
			gateway.NewParallel("p-fork"),
			activity.NewUserTask("p-user", activity.WithWaitDeadline(schedule.AfterExpr(`"1h"`), "")),
			activity.NewCallActivity("p-call", model.Latest("child")),
			gateway.NewParallel("p-join"),
			event.NewEnd("p-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "p-start", Target: "p-fork"},
			{ID: "f2", Source: "p-fork", Target: "p-user"},
			{ID: "f3", Source: "p-fork", Target: "p-call"},
			{ID: "f4", Source: "p-user", Target: "p-join"},
			{ID: "f5", Source: "p-call", Target: "p-join"},
			{ID: "f6", Source: "p-join", Target: "p-end"},
		},
	}
}

// TestCallActivitySubInstanceFailedCancelsOutstandingTimers (Fix 3 assertion):
//
// A parent definition has both a parallel user-task (with deadline timer) and a
// call-activity branch. When StartInstance drives, both branches start:
//   - A ScheduleTimer (Deadline) is emitted for the user-task → timerRecord in state.Timers.
//   - A StartSubInstance is emitted for the call-activity → token parks.
//
// When SubInstanceFailed arrives, the engine must:
//  1. Emit FailInstance (transition to StatusFailed).
//  2. Emit CancelTimer for the deadline timer, proving cancelAllTimers runs on the
//     SubInstanceFailed path and cleans up all outstanding timer records.
func TestCallActivitySubInstanceFailedCancelsOutstandingTimers(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := callActivityWithParallelUserTaskDef()

	// ---- Step 1: StartInstance → parallel fork → deadline timer + call-activity starts ----
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "ca-sla-i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// Find the ScheduleTimer (Deadline) and StartSubInstance commands.
	var deadlineTimerID string
	var ssiCmdID string
	for _, cmd := range r1.Commands {
		switch c := cmd.(type) {
		case engine.ScheduleTimer:
			if c.Kind == engine.TimerDeadline {
				deadlineTimerID = c.TimerID
			}
		case engine.StartSubInstance:
			ssiCmdID = c.CommandID
		}
	}
	require.NotEmpty(t, deadlineTimerID, "expected ScheduleTimer (Deadline) for the user-task branch")
	require.NotEmpty(t, ssiCmdID, "expected StartSubInstance for the call-activity branch")

	// Deadline timer must be recorded in state.Timers.
	require.NotEmpty(t, r1.State.Timers, "deadline timerRecord must be recorded in Timers")

	// ---- Step 2: SubInstanceFailed → parent must fail AND cancel the deadline timer ----
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSubInstanceFailed(at.Add(time.Second), ssiCmdID, "child blew up"),
		engine.StepOptions{})
	require.NoError(t, err)

	// Parent must be failed.
	assert.Equal(t, engine.StatusFailed, r2.State.Status, "parent must be StatusFailed")
	require.NotNil(t, r2.State.EndedAt)

	// FailInstance must be present.
	failInstFound := false
	cancelTimerFound := false
	for _, cmd := range r2.Commands {
		switch c := cmd.(type) {
		case engine.FailInstance:
			failInstFound = true
			assert.Contains(t, c.Err, "child blew up")
		case engine.CancelTimer:
			if c.TimerID == deadlineTimerID {
				cancelTimerFound = true
			}
		}
	}
	assert.True(t, failInstFound, "FailInstance must be emitted on SubInstanceFailed")
	assert.True(t, cancelTimerFound,
		"CancelTimer for deadline timer %q must be emitted on SubInstanceFailed (cancelAllTimers path)", deadlineTimerID)

	// Timer must be gone from state after cleanup.
	assert.Empty(t, r2.State.Timers, "Timers must be empty after cancelAllTimers on failure path")
}

// TestSubInstanceFailedUnknownCommandID verifies that a SubInstanceFailed with
// an unrecognised CommandID returns ErrTokenNotFound.
func TestSubInstanceFailedUnknownCommandID(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := callActivityDef()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "ca-i4"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	_, err = engine.Step(t.Context(), def, r1.State,
		engine.NewSubInstanceFailed(at.Add(time.Second), "nonexistent-cmd", "err"),
		engine.StepOptions{})
	require.Error(t, err)
	require.ErrorIs(t, err, engine.ErrTokenNotFound)
}

// callActivityWithBoundaryDef builds:
//
//	Root: start → call (CallActivity, DefRef "child") → end
//	      call has a boundary error event → recover → end-recover
//
// boundaryErrorCode is the boundary's ErrorCode ("" == catch-all). Used to verify
// that SubInstanceFailed routes to a parent error boundary attached to the
// call-activity node when the child's error code matches (ADR-0128), instead of
// unconditionally failing the parent.
func callActivityWithBoundaryDef(boundaryErrorCode string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-call-bnd", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewCallActivity("call", model.Latest("child")),
			event.NewBoundary("bnd-call-err", "call", event.WithBoundaryErrorCode(boundaryErrorCode)),
			activity.NewServiceTask("recover", activity.WithTaskAction("recover-action")),
			event.NewEnd("end"),
			event.NewEnd("end-recover"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start-call", Source: "start", Target: "call"},
			{ID: "f-call-end", Source: "call", Target: "end"},
			{ID: "f-bnd-recover", Source: "bnd-call-err", Target: "recover"},
			{ID: "f-recover-end", Source: "recover", Target: "end-recover"},
		},
	}
}

// TestSubInstanceFailedRoutesToParentBoundary is the ADR-0128 regression test: a
// SubInstanceFailed is semantically an error thrown at the call-activity node that
// spawned the child. When the call-activity node carries a boundary error event
// whose ErrorCode matches the child's error, the engine must route to it (like
// propagateError's direct-boundary path) instead of unconditionally failing the
// parent. When no boundary matches, the parent still FailInstances (unchanged
// fallback behavior).
func TestSubInstanceFailedRoutesToParentBoundary(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name              string
		boundaryErrorCode string
		childErr          string
		assert            func(t *testing.T, r2 engine.StepResult)
	}

	cases := []testCase{
		{
			name:              "specific error code match routes to boundary",
			boundaryErrorCode: "E1",
			childErr:          "E1",
			assert: func(t *testing.T, r2 engine.StepResult) {
				assert.Equal(t, engine.StatusRunning, r2.State.Status,
					"parent must stay running — the boundary caught the child failure")
				assert.Nil(t, r2.State.EndedAt, "EndedAt must NOT be set when the boundary catches the error")

				for _, c := range r2.Commands {
					if _, ok := c.(engine.FailInstance); ok {
						t.Fatal("FailInstance must NOT be emitted when the boundary catches the child error")
					}
				}

				var recoverIA *engine.InvokeAction
				for _, c := range r2.Commands {
					if v, ok := c.(engine.InvokeAction); ok {
						vv := v
						recoverIA = &vv
					}
				}
				require.NotNil(t, recoverIA, "expected InvokeAction for recover-action")
				assert.Equal(t, "recover-action", recoverIA.Name)

				require.Len(t, r2.State.Tokens, 1, "exactly one token must remain (at recover)")
				assert.Equal(t, "recover", r2.State.Tokens[0].NodeID)
			},
		},
		{
			name:              "catch-all boundary routes any child error",
			boundaryErrorCode: "",
			childErr:          "anything",
			assert: func(t *testing.T, r2 engine.StepResult) {
				assert.Equal(t, engine.StatusRunning, r2.State.Status,
					"catch-all boundary must catch any child error code")
				for _, c := range r2.Commands {
					if _, ok := c.(engine.FailInstance); ok {
						t.Fatal("FailInstance must NOT be emitted when the catch-all boundary catches the error")
					}
				}
			},
		},
		{
			name:              "error code mismatch falls back to FailInstance",
			boundaryErrorCode: "E1",
			childErr:          "OTHER",
			assert: func(t *testing.T, r2 engine.StepResult) {
				assert.Equal(t, engine.StatusFailed, r2.State.Status,
					"parent must fail when no boundary matches the child's error code")
				require.NotNil(t, r2.State.EndedAt)

				require.NotEmpty(t, r2.Commands)
				fi, ok := r2.Commands[0].(engine.FailInstance)
				require.True(t, ok, "expected FailInstance, got %T", r2.Commands[0])
				assert.Contains(t, fi.Err, "OTHER")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := callActivityWithBoundaryDef(tc.boundaryErrorCode)

			r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "ca-bnd-" + tc.name},
				engine.NewStartInstance(at, nil), engine.StepOptions{})
			require.NoError(t, err)
			require.Len(t, r1.Commands, 1)
			ssi, ok := r1.Commands[0].(engine.StartSubInstance)
			require.True(t, ok)

			r2, err := engine.Step(t.Context(), def, r1.State,
				engine.NewSubInstanceFailed(at.Add(time.Second), ssi.CommandID, tc.childErr),
				engine.StepOptions{})
			require.NoError(t, err)

			tc.assert(t, r2)
		})
	}
}

// ---- Inner-scope topology tests (Task 6) ----

// boundaryTimerInsideSubProcessDef builds:
//
// outer: outer-start → sub (KindSubProcess) → outer-end
//
// sub's inner def:
//
//	inner-start → inner-svc (ServiceTask "inner-action") → inner-end
//	[KindBoundaryEvent "bnd-timer"] attached to inner-svc, interrupting, timer "2h"
//	  bnd-timer → bnd-target (ServiceTask "escalate-action") → bnd-end
func boundaryTimerInsideSubProcessDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "inner-bnd-timer", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithTaskAction("inner-action")),
			event.NewBoundary("bnd-timer", "inner-svc", event.WithBoundaryTimer(schedule.AfterExpr(`"2h"`))),
			activity.NewServiceTask("bnd-target", activity.WithTaskAction("escalate-action")),
			event.NewEnd("inner-end"),
			event.NewEnd("bnd-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
			{ID: "if3", Source: "bnd-timer", Target: "bnd-target"},
			{ID: "if4", Source: "bnd-target", Target: "bnd-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-bnd-timer", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			activity.NewSubProcess("sub", inner),
			event.NewEnd("outer-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "of1", Source: "outer-start", Target: "sub"},
			{ID: "of2", Source: "sub", Target: "outer-end"},
		},
	}
}

// TestBoundaryTimerInsideSubProcess verifies that an interrupting boundary timer
// attached to an activity nested inside a sub-process:
//
//  1. Arms the boundary timer (ScheduleTimer) within the child scope on scope entry.
//  2. When the timer fires, the host token (inner-svc) is cancelled, a new token
//     lands on bnd-target (InvokeAction for "escalate-action") — all within the
//     child scope.
//  3. Completing the escalation action drains the inner scope → outer-end →
//     StatusCompleted (sub-process exits cleanly to the parent).
func TestBoundaryTimerInsideSubProcess(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := boundaryTimerInsideSubProcessDef()

	// ---- Step 1: StartInstance → sub enters → inner-svc parks + boundary timer armed ----
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "bnd-sub-i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// Sub-process scope must be open.
	require.Len(t, r1.State.Scopes, 1, "sub-process scope must be open")
	scopeID := r1.State.Scopes[0].ID

	// InvokeAction for inner-svc + ScheduleTimer for boundary.
	var innerCmdID string
	var bndTimerID string
	for _, cmd := range r1.Commands {
		switch c := cmd.(type) {
		case engine.InvokeAction:
			if c.Name == "inner-action" {
				innerCmdID = c.CommandID
			}
		case engine.ScheduleTimer:
			bndTimerID = c.TimerID
		}
	}
	require.NotEmpty(t, innerCmdID, "expected InvokeAction for inner-action")
	require.NotEmpty(t, bndTimerID, "expected ScheduleTimer for boundary timer")

	// One token parked at inner-svc, in the sub-process scope.
	require.Len(t, r1.State.Tokens, 1)
	assert.Equal(t, "inner-svc", r1.State.Tokens[0].NodeID)
	assert.Equal(t, scopeID, r1.State.Tokens[0].ScopeID,
		"inner-svc token must carry the sub-process scope ID")

	// Boundary arm must be recorded.
	require.Len(t, r1.State.Boundaries, 1, "boundary arm must be recorded in state")

	// ---- Step 2: Boundary timer fires → host cancelled, escalation path runs ----
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewTimerFired(at.Add(2*time.Hour), bndTimerID), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)

	// inner-svc token must be gone (interrupting boundary cancelled it).
	for _, tok := range r2.State.Tokens {
		assert.NotEqual(t, "inner-svc", tok.NodeID,
			"inner-svc token must be cancelled by interrupting boundary timer")
	}

	// InvokeAction for "escalate-action" must have been emitted.
	var escalateCmdID string
	for _, cmd := range r2.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok && ia.Name == "escalate-action" {
			escalateCmdID = ia.CommandID
		}
	}
	require.NotEmpty(t, escalateCmdID, "expected InvokeAction for escalate-action")

	// bnd-target token must be within the same scope.
	require.Len(t, r2.State.Tokens, 1)
	assert.Equal(t, "bnd-target", r2.State.Tokens[0].NodeID)
	assert.Equal(t, scopeID, r2.State.Tokens[0].ScopeID,
		"bnd-target token must remain in the sub-process scope")

	// Scope still open (boundary path not yet drained).
	require.Len(t, r2.State.Scopes, 1, "scope must still be open after boundary fires")

	// ---- Step 3: Complete escalation → inner scope drains → outer-end → StatusCompleted ----
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Hour+time.Second), escalateCmdID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r3.State.Status,
		"instance must complete after boundary path drains the sub-process scope")
	assert.Empty(t, r3.State.Tokens, "all tokens must be consumed on completion")
	assert.Empty(t, r3.State.Scopes, "sub-process scope must be closed on completion")
	require.NotNil(t, r3.State.EndedAt)

	found := false
	for _, cmd := range r3.Commands {
		if _, ok := cmd.(engine.CompleteInstance); ok {
			found = true
			break
		}
	}
	assert.True(t, found, "expected CompleteInstance after sub-process exits via boundary path")
}

// eventBasedGatewayInsideSubProcessDef builds:
//
// outer: outer-start → sub (KindSubProcess) → outer-end
//
// sub's inner def:
//
//	inner-start → evtgw (KindEventBasedGateway)
//	  → timer-catch (IntermediateCatchEvent timer "1h") → svc-timer → inner-end
//	  → signal-catch (IntermediateCatchEvent signal "approved") → svc-signal → inner-end2
func eventBasedGatewayInsideSubProcessDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "inner-evtgw", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			gateway.NewEventBased("evtgw"),
			event.NewIntermediateCatch("timer-catch", event.WithCatchTimer(schedule.AfterExpr(`"1h"`))),
			event.NewIntermediateCatch("signal-catch", event.WithSignalName("approved")),
			activity.NewServiceTask("svc-timer", activity.WithTaskAction("timer-action")),
			activity.NewServiceTask("svc-signal", activity.WithTaskAction("signal-action")),
			event.NewEnd("inner-end"),
			event.NewEnd("inner-end2"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "evtgw"},
			{ID: "if2", Source: "evtgw", Target: "timer-catch"},
			{ID: "if3", Source: "evtgw", Target: "signal-catch"},
			{ID: "if4", Source: "timer-catch", Target: "svc-timer"},
			{ID: "if5", Source: "signal-catch", Target: "svc-signal"},
			{ID: "if6", Source: "svc-timer", Target: "inner-end"},
			{ID: "if7", Source: "svc-signal", Target: "inner-end2"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-evtgw", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			activity.NewSubProcess("sub", inner),
			event.NewEnd("outer-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "of1", Source: "outer-start", Target: "sub"},
			{ID: "of2", Source: "sub", Target: "outer-end"},
		},
	}
}

// TestEventBasedGatewayInsideSubProcess verifies that an event-based gateway
// nested inside a sub-process races its arms correctly within the child scope,
// and the sub-process exits cleanly when the winning branch completes.
//
// Scenario: signal wins over the timer.
//  1. Start → sub enters → event gateway arms (timer + signal) in child scope.
//  2. SignalReceived("approved") → first-event-wins: signal branch proceeds
//     (InvokeAction for signal-action); timer arm cancelled (CancelTimer emitted).
//  3. Complete signal-action → inner scope drains → outer-end → StatusCompleted.
func TestEventBasedGatewayInsideSubProcess(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := eventBasedGatewayInsideSubProcessDef()

	// ---- Step 1: StartInstance → sub enters → event gateway parks with arms ----
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "evtgw-sub-i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// Sub-process scope must be open.
	require.Len(t, r1.State.Scopes, 1, "sub-process scope must be open")
	scopeID := r1.State.Scopes[0].ID

	// One token: the event gateway parked.
	require.Len(t, r1.State.Tokens, 1)
	assert.Equal(t, "evtgw", r1.State.Tokens[0].NodeID)
	assert.Equal(t, scopeID, r1.State.Tokens[0].ScopeID,
		"gateway token must carry the sub-process scope ID")

	// Two armed events: timer arm + signal arm.
	assert.Len(t, r1.State.ArmedEvents, 2, "both gateway arms must be recorded in ArmedEvents")

	// ScheduleTimer for timer-catch arm.
	var timerID string
	for _, cmd := range r1.Commands {
		if st, ok := cmd.(engine.ScheduleTimer); ok {
			timerID = st.TimerID
		}
	}
	require.NotEmpty(t, timerID, "expected ScheduleTimer for timer-catch arm")

	// ---- Step 2: SignalReceived("approved") → signal wins; timer arm cancelled ----
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(at.Add(30*time.Minute), "approved", nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)

	// InvokeAction for signal-action must be emitted.
	var signalCmdID string
	for _, cmd := range r2.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok && ia.Name == "signal-action" {
			signalCmdID = ia.CommandID
		}
	}
	require.NotEmpty(t, signalCmdID, "expected InvokeAction for signal-action")

	// CancelTimer for the loser timer arm must be emitted.
	cancelFound := false
	for _, cmd := range r2.Commands {
		if ct, ok := cmd.(engine.CancelTimer); ok && ct.TimerID == timerID {
			cancelFound = true
		}
	}
	assert.True(t, cancelFound, "expected CancelTimer for loser timer arm")

	// All armed events must be cleared (gateway resolved).
	assert.Empty(t, r2.State.ArmedEvents, "ArmedEvents must be empty after gateway resolves")

	// Token at svc-signal within the scope.
	require.Len(t, r2.State.Tokens, 1)
	assert.Equal(t, "svc-signal", r2.State.Tokens[0].NodeID)
	assert.Equal(t, scopeID, r2.State.Tokens[0].ScopeID,
		"svc-signal token must remain in the sub-process scope")

	// ---- Step 3: Complete signal-action → inner scope drains → outer-end → StatusCompleted ----
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewActionCompleted(at.Add(time.Hour), signalCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r3.State.Status,
		"instance must complete after event-gateway signal branch drains sub-process scope")
	assert.Empty(t, r3.State.Tokens, "all tokens must be consumed on completion")
	assert.Empty(t, r3.State.Scopes, "sub-process scope must be closed on completion")
	require.NotNil(t, r3.State.EndedAt)

	found := false
	for _, cmd := range r3.Commands {
		if _, ok := cmd.(engine.CompleteInstance); ok {
			found = true
			break
		}
	}
	assert.True(t, found, "expected CompleteInstance after event-gateway sub-process exits")
}

// inclusiveGatewayInsideSubProcessDef builds:
//
// outer: outer-start → sub (KindSubProcess) → outer-end
//
// sub's inner def (OR-fork + OR-join diamond):
//
//	inner-start → orsplit (KindInclusiveGateway) -{a>0}-> ta ; -{b>0}-> tb
//	ta, tb → orjoin (KindInclusiveGateway) → post (ServiceTask "post-action") → inner-end
func inclusiveGatewayInsideSubProcessDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "inner-or", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			gateway.NewInclusive("orsplit"),
			activity.NewServiceTask("ta", activity.WithTaskAction("action-a")),
			activity.NewServiceTask("tb", activity.WithTaskAction("action-b")),
			gateway.NewInclusive("orjoin"),
			activity.NewServiceTask("post", activity.WithTaskAction("post-action")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "orsplit"},
			{ID: "if2", Source: "orsplit", Target: "ta", Condition: "a > 0"},
			{ID: "if3", Source: "orsplit", Target: "tb", Condition: "b > 0"},
			{ID: "if4", Source: "ta", Target: "orjoin"},
			{ID: "if5", Source: "tb", Target: "orjoin"},
			{ID: "if6", Source: "orjoin", Target: "post"},
			{ID: "if7", Source: "post", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-or", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			activity.NewSubProcess("sub", inner),
			event.NewEnd("outer-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "of1", Source: "outer-start", Target: "sub"},
			{ID: "of2", Source: "sub", Target: "outer-end"},
		},
	}
}

// TestInclusiveGatewayInsideSubProcess verifies that an inclusive (OR) gateway
// fork+join nested inside a sub-process correctly activates multiple branches,
// joins them within the child scope, and the sub-process exits cleanly.
//
// Variables: {a:1, b:1} → both branches taken.
//  1. Start → sub enters → orsplit forks to ta AND tb (both conditions true).
//  2. Complete ta → OR-join waits (tb still reachable).
//  3. Complete tb → OR-join fires (both arrived) → post-action invoked.
//  4. Complete post-action → inner-end drains scope → outer-end → StatusCompleted.
func TestInclusiveGatewayInsideSubProcess(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := inclusiveGatewayInsideSubProcessDef()

	// ---- Step 1: StartInstance → sub enters → orsplit forks to ta AND tb ----
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "or-sub-i1"},
		engine.NewStartInstance(at, map[string]any{"a": 1, "b": 1}), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// Sub-process scope must be open.
	require.Len(t, r1.State.Scopes, 1, "sub-process scope must be open")
	scopeID := r1.State.Scopes[0].ID

	// Two tokens: ta and tb, both in the sub-process scope.
	require.Len(t, r1.State.Tokens, 2, "OR-fork must produce two tokens for a>0 and b>0")
	nodeIDs := []string{r1.State.Tokens[0].NodeID, r1.State.Tokens[1].NodeID}
	assert.ElementsMatch(t, []string{"ta", "tb"}, nodeIDs, "forked tokens must land on ta and tb")
	for _, tok := range r1.State.Tokens {
		assert.Equal(t, scopeID, tok.ScopeID,
			"forked token at %q must carry sub-process scope ID", tok.NodeID)
	}

	// Two InvokeAction commands: action-a and action-b.
	require.Len(t, r1.Commands, 2, "expected two InvokeAction commands after OR-fork")
	cmdsByName := make(map[string]string)
	for _, cmd := range r1.Commands {
		ia, ok := cmd.(engine.InvokeAction)
		require.True(t, ok, "expected InvokeAction, got %T", cmd)
		cmdsByName[ia.Name] = ia.CommandID
	}
	assert.Contains(t, cmdsByName, "action-a")
	assert.Contains(t, cmdsByName, "action-b")

	// ---- Step 2: Complete action-a → OR-join must wait (tb still reachable) ----
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(at.Add(time.Second), cmdsByName["action-a"], nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)
	// OR-join must NOT fire yet (tb can still deliver).
	assert.Empty(t, r2.Commands, "OR-join must not fire while tb can still reach it")
	// Scope still open.
	require.Len(t, r2.State.Scopes, 1, "scope must still be open after first branch completes")

	// ---- Step 3: Complete action-b → OR-join fires → post-action invoked ----
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), cmdsByName["action-b"], nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r3.State.Status, "instance still running (post-action pending)")

	// Exactly one InvokeAction for post-action (join fired once, not twice).
	require.Len(t, r3.Commands, 1, "OR-join must fire exactly once")
	postCmd, ok := r3.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "expected InvokeAction for post-action")
	assert.Equal(t, "post-action", postCmd.Name)

	// ---- Step 4: Complete post-action → inner-end drains scope → outer-end → StatusCompleted ----
	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewActionCompleted(at.Add(3*time.Second), postCmd.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r4.State.Status,
		"instance must complete after inclusive gateway join drains sub-process scope")
	assert.Empty(t, r4.State.Tokens, "all tokens must be consumed on completion")
	assert.Empty(t, r4.State.Scopes, "sub-process scope must be closed on completion")
	require.NotNil(t, r4.State.EndedAt)

	found := false
	for _, cmd := range r4.Commands {
		if _, ok := cmd.(engine.CompleteInstance); ok {
			found = true
			break
		}
	}
	assert.True(t, found, "expected CompleteInstance after inclusive-gateway sub-process exits")
}

// deadlineUserTaskInsideSubProcessDef builds:
//
// outer: outer-start → sub (KindSubProcess) → outer-end
//
// sub's inner def:
//
//	inner-start → inner-user (KindUserTask, DeadlineDuration "30m", DeadlineFlow "inner-escalate",
//	              DeadlineAction "notify-action") → inner-end
//	inner-user → (inner-escalate flow) → escalate-node (KindEndEvent)
func deadlineUserTaskInsideSubProcessDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "inner-sla", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewUserTask("inner-user", activity.WithEligibleRoles("reviewer"),
				activity.WithWaitDeadline(schedule.AfterExpr(`"30m"`), "inner-escalate"), activity.WithDeadlineAction("notify-action")),
			event.NewEnd("inner-end"),
			event.NewEnd("escalate-node"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-user"},
			{ID: "if2", Source: "inner-user", Target: "inner-end"},
			{ID: "inner-escalate", Source: "inner-user", Target: "escalate-node"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-sla", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			activity.NewSubProcess("sub", inner),
			event.NewEnd("outer-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "of1", Source: "outer-start", Target: "sub"},
			{ID: "of2", Source: "sub", Target: "outer-end"},
		},
	}
}

// TestDeadlineUserTaskInsideSubProcess verifies that a deadline timer on a user task
// nested inside a sub-process:
//
//  1. Arms the deadline timer (ScheduleTimer) and parks the user task within the child scope.
//  2. When the deadline timer fires (task NOT completed), the escalation path runs within
//     the child scope (InvokeAction for "notify-action"), the task is cancelled
//     (UpdateTask), and the token moves to the escalation end node.
//  3. The escalation end drains the inner scope → outer-end → StatusCompleted
//     (sub-process exits cleanly to the parent).
func TestDeadlineUserTaskInsideSubProcess(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := deadlineUserTaskInsideSubProcessDef()

	// ---- Step 1: StartInstance → sub enters → user-task parks + deadline armed ----
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "sla-sub-i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)

	// Sub-process scope must be open.
	require.Len(t, r1.State.Scopes, 1, "sub-process scope must be open")
	scopeID := r1.State.Scopes[0].ID

	// AwaitHuman + ScheduleTimer(Deadline) emitted.
	var deadlineTimerID string
	var taskToken string
	for _, cmd := range r1.Commands {
		switch c := cmd.(type) {
		case engine.AwaitHuman:
			taskToken = c.TaskToken
		case engine.ScheduleTimer:
			if c.Kind == engine.TimerDeadline {
				deadlineTimerID = c.TimerID
			}
		}
	}
	require.NotEmpty(t, taskToken, "expected AwaitHuman for inner-user task")
	require.NotEmpty(t, deadlineTimerID, "expected ScheduleTimer(Deadline) for inner-user task")

	// One token parked at inner-user, in the sub-process scope.
	require.Len(t, r1.State.Tokens, 1)
	assert.Equal(t, "inner-user", r1.State.Tokens[0].NodeID)
	assert.Equal(t, scopeID, r1.State.Tokens[0].ScopeID,
		"inner-user token must carry the sub-process scope ID")

	// ---- Step 2: deadline fires (task NOT completed) → escalation path inside scope ----
	fireAt := at.Add(30 * time.Minute)
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewTimerFired(fireAt, deadlineTimerID), engine.StepOptions{})
	require.NoError(t, err)

	// InvokeAction for notify-action, UpdateTask (cancelled), and CompleteInstance
	// (because escalate-node is an EndEvent, inner scope drains → outer completes).
	var foundNotify bool
	var foundUpdateTask bool
	var foundComplete bool
	for _, cmd := range r2.Commands {
		switch c := cmd.(type) {
		case engine.InvokeAction:
			if c.Name == "notify-action" {
				foundNotify = true
			}
		case engine.UpdateTask:
			if c.Task.TaskToken == taskToken {
				foundUpdateTask = true
			}
		case engine.CompleteInstance:
			foundComplete = true
		}
	}
	assert.True(t, foundNotify, "expected InvokeAction for notify-action on deadline breach")
	assert.True(t, foundUpdateTask, "expected UpdateTask (task cancelled) on deadline breach")
	assert.True(t, foundComplete,
		"expected CompleteInstance: escalation end drains inner scope → outer-end reached")

	// Instance must be completed (escalate-node is EndEvent → inner scope drains → outer-end).
	assert.Equal(t, engine.StatusCompleted, r2.State.Status,
		"instance must complete after deadline breach escalation path drains sub-process scope")
	assert.Empty(t, r2.State.Tokens, "all tokens must be consumed on completion")
	assert.Empty(t, r2.State.Scopes, "sub-process scope must be closed on completion")
	require.NotNil(t, r2.State.EndedAt)
}
