package engine_test

// step_compensation_test.go — black-box tests for Plan 8 Task 1:
// recording CompensationRecord entries when a compensable activity completes.
//
// Compensable activity = a node with a non-empty CompensateAction field.
// On completion (ActionCompleted for ServiceTask; sub-process exit), the engine
// appends a CompensationRecord to the enclosing scope's Compensations list.

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

// compensableDef returns a minimal process:
//
//	start → svc(CompensateAction:"refund") → end
//
// The service task is compensable: when it completes, the engine should append
// a CompensationRecord into its enclosing scope's Compensations list.
func compensableDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "comp-proc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithTaskAction("charge"), activity.WithCompensateAction("refund")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
		},
	}
}

// nonCompensableDef returns a process with a service task that has NO CompensateAction.
func nonCompensableDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "plain-proc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithTaskAction("charge")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
		},
	}
}

// compensableSubProcessDef returns a process with a sub-process containing a
// compensable service task. After the sub-process exits, the compensation record
// should be in the sub-process scope's Compensations, NOT the root scope.
func compensableSubProcessDef() *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "nested", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithTaskAction("book"), activity.WithCompensateAction("cancel-booking")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-proc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", nested),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "end"},
		},
	}
}

// TestCompletedActivityRecordsCompensation asserts that when a compensable
// ServiceTask completes (ActionCompleted), the engine appends a CompensationRecord
// with the correct NodeID, Action, CompletedAt, and Input into the root scope's
// RootCompensations (the compensation list for top-level activities).
func TestCompletedActivityRecordsCompensation(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	completedAt := at.Add(5 * time.Second)

	def := compensableDef()

	// Step 1: start the instance (vars carry the activity's input).
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "comp-inst-1"},
		engine.NewStartInstance(at, map[string]any{"amount": 100, "currency": "USD"}),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1)
	cmdID := r1.Commands[0].(engine.InvokeAction).CommandID

	// Step 2: complete the service task. Output is merged into variables.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(completedAt, cmdID, map[string]any{"txID": "tx-42"}),
		engine.StepOptions{})
	require.NoError(t, err)

	// The instance should have completed (start → svc → end).
	assert.Equal(t, engine.StatusCompleted, r2.State.Status)

	// Root-level compensations are stored on InstanceState.RootCompensations
	// (not in s.Scopes, to keep the scope list clean for existing tests).
	require.Len(t, r2.State.RootCompensations, 1,
		"one compensation record expected for the completed compensable service task")

	rec := r2.State.RootCompensations[0]
	assert.Equal(t, "svc", rec.NodeID, "NodeID must identify the completed activity")
	assert.Equal(t, "refund", rec.Action, "Action must be the node's CompensateAction")
	assert.Equal(t, completedAt, rec.CompletedAt, "CompletedAt must be the trigger's OccurredAt")
	// Input is a snapshot of the instance variables AT completion time
	// (before merging the action's output). The snapshot contains the vars that
	// were passed to InvokeAction.
	require.NotNil(t, rec.Input, "Input snapshot must not be nil")
	assert.Equal(t, 100, rec.Input["amount"], "Input must contain vars from instance at invoke time")
}

// TestNonCompensableActivityDoesNotRecord asserts that a service task WITHOUT
// a CompensateAction does not append any record.
func TestNonCompensableActivityDoesNotRecord(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	completedAt := at.Add(5 * time.Second)

	def := nonCompensableDef()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "plain-inst-1"},
		engine.NewStartInstance(at, map[string]any{"x": 1}),
		engine.StepOptions{})
	require.NoError(t, err)
	cmdID := r1.Commands[0].(engine.InvokeAction).CommandID

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(completedAt, cmdID, nil),
		engine.StepOptions{})
	require.NoError(t, err)

	assert.Equal(t, engine.StatusCompleted, r2.State.Status)
	assert.Empty(t, r2.State.RootCompensations,
		"non-compensable task must not append any record")
}

// TestCompensableActivityInsideSubProcessIsArchivedOnClose asserts that
// when a sub-process scope closes normally, its accumulated CompensationRecords
// are moved into ArchivedCompensations keyed by the sub-process node ID (ADR-0039
// archive-by-scope). Records are NOT hoisted to RootCompensations directly; instead
// consolidateArchiveIntoRoot merges them into RootCompensations when the compensation
// walk begins (CompensateRequested / error / cancel with compensation).
//
// Pre-ADR-0039: records were hoisted to RootCompensations (ADR-0013 hoist).
// Post-ADR-0039: records are archived by scope, preserving scope identity.
func TestCompensableActivityInsideSubProcessIsArchivedOnClose(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)

	def := compensableSubProcessDef()

	// Start the instance.
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "sub-comp-inst-1"},
		engine.NewStartInstance(at, map[string]any{"seat": "12A"}),
		engine.StepOptions{})
	require.NoError(t, err)
	// The inner service task should be invoked immediately (start → sub → inner-start → inner-svc).
	require.Len(t, r1.Commands, 1)
	ia, ok := r1.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "expected InvokeAction for the inner service task")
	assert.Equal(t, "book", ia.Name)

	// Verify the sub-process scope is open in r1.
	require.Len(t, r1.State.Scopes, 1, "sub-process scope must be open")
	subScopeID := r1.State.Scopes[0].ID
	assert.NotEmpty(t, subScopeID, "sub-process scope must have a non-empty ID")

	completedAt := at.Add(3 * time.Second)

	// Complete the inner service task. After this, inner-svc → inner-end → scope
	// drains → archiveCompensations called → scope closes → outer-end → instance completes.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(completedAt, ia.CommandID, map[string]any{"confirmationCode": "ABC"}),
		engine.StepOptions{})
	require.NoError(t, err)

	// Instance completes: start → sub(inner-start → inner-svc → inner-end) → end.
	assert.Equal(t, engine.StatusCompleted, r2.State.Status)

	// The sub-process scope is now closed (removed from r2.State.Scopes).
	assert.Empty(t, r2.State.Scopes, "sub-process scope must be closed")

	// ADR-0039 archive-by-scope: the inner activity's record must now be in
	// ArchivedCompensations keyed by the sub-process node ID ("sub"), NOT in RootCompensations.
	assert.Empty(t, r2.State.RootCompensations,
		"RootCompensations must NOT contain inner records — they live in ArchivedCompensations")
	require.NotNil(t, r2.State.ArchivedCompensations,
		"ArchivedCompensations must be non-nil after sub-process closes with compensable records")
	require.Contains(t, r2.State.ArchivedCompensations, "sub",
		"ArchivedCompensations must be keyed by the sub-process node ID")
	require.Len(t, r2.State.ArchivedCompensations["sub"], 1,
		"exactly one record must be archived for the sub-process scope")
	assert.Equal(t, "inner-svc", r2.State.ArchivedCompensations["sub"][0].NodeID,
		"archived record NodeID must be the inner compensable activity")
	assert.Equal(t, "cancel-booking", r2.State.ArchivedCompensations["sub"][0].Action,
		"archived record Action must be the inner activity's CompensateAction")
}

// TestCompensationRecordInputIsSnapshotNotReference asserts the Input stored in
// a CompensationRecord is an independent copy: merging the action's output into
// instance variables after completion does not retroactively alter the recorded Input.
func TestCompensationRecordInputIsSnapshotNotReference(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	completedAt := at.Add(2 * time.Second)

	def := compensableDef()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "snap-inst-1"},
		engine.NewStartInstance(at, map[string]any{"val": "original"}),
		engine.StepOptions{})
	require.NoError(t, err)
	cmdID := r1.Commands[0].(engine.InvokeAction).CommandID

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(completedAt, cmdID, map[string]any{"val": "mutated"}),
		engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, r2.State.RootCompensations, 1)

	// The Input snapshot was taken BEFORE the output was merged, so it should
	// contain the original value, not the mutated output value.
	rec := r2.State.RootCompensations[0]
	assert.Equal(t, "original", rec.Input["val"],
		"Input must be a snapshot of variables BEFORE ActionCompleted output is merged")
}

// ── ADR-0013 regression: nested-scope compensation hoist ─────────────────────

// compensableSubThenRootDef returns a process:
//
//	Outer: start → sub → rootUserTask → end
//	Inner: inner-start → inner-svc(CompensateAction:"cancel-inner") → inner-end
//
// The root user task keeps the instance Running after the sub-process exits, so
// a CompensateRequested can be issued and must reach the now-hoisted inner record.
func compensableSubThenRootDef() *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "nested-hoist", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithTaskAction("book-inner"), activity.WithCompensateAction("cancel-inner")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-hoist", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", nested),
			activity.NewUserTask("rootUserTask"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "rootUserTask"},
			{ID: "f3", Source: "rootUserTask", Target: "end"},
		},
	}
}

// TestArchiveSubProcessCompensationAndReachViaWalk verifies that after a
// sub-process completes and its scope closes, the inner compensable activity's
// record is archived in ArchivedCompensations (not RootCompensations), and that
// a subsequent CompensateRequested consolidates the archive and emits the
// compensation action (ADR-0039).
func TestArchiveSubProcessCompensationAndReachViaWalk(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := compensableSubThenRootDef()

	// Step 1: start the instance — drives to inner-svc (InvokeAction).
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "hoist-inst-1"},
		engine.NewStartInstance(at, map[string]any{"x": 1}),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1, "expected InvokeAction for inner-svc")
	ia, ok := r1.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "expected InvokeAction")
	require.Equal(t, "book-inner", ia.Name)

	// Step 2: complete inner-svc — drives inner-svc → inner-end → scope closes
	// → archiveCompensations called → token placed at rootUserTask. Instance Running.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(at.Add(1*time.Second), ia.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r2.State.Status,
		"instance must still be running (parked at rootUserTask)")
	// Sub-process scope must be closed.
	require.Empty(t, r2.State.Scopes, "sub-process scope must be closed")

	// ADR-0039: record must be in ArchivedCompensations (keyed by "sub"), NOT RootCompensations.
	assert.Empty(t, r2.State.RootCompensations,
		"RootCompensations must NOT contain inner records after sub-process closes (ADR-0039)")
	require.NotNil(t, r2.State.ArchivedCompensations,
		"ArchivedCompensations must be populated after sub-process closes with compensable record")
	require.Contains(t, r2.State.ArchivedCompensations, "sub",
		"ArchivedCompensations must be keyed by the sub-process node ID")
	require.Len(t, r2.State.ArchivedCompensations["sub"], 1,
		"exactly one record must be archived")
	require.Equal(t, "inner-svc", r2.State.ArchivedCompensations["sub"][0].NodeID)
	require.Equal(t, "cancel-inner", r2.State.ArchivedCompensations["sub"][0].Action)

	// Step 3: issue full CompensateRequested — consolidation runs then walk emits cancel-inner.
	compensateAt := at.Add(5 * time.Second)
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewCompensateRequested(compensateAt, ""),
		engine.StepOptions{})
	require.NoError(t, err)
	var gotActions []string
	for _, c := range r3.Commands {
		if invokeAction, ok2 := c.(engine.InvokeAction); ok2 {
			gotActions = append(gotActions, invokeAction.Name)
		}
	}
	require.Contains(t, gotActions, "cancel-inner",
		"inner sub-process activity must be rollback-able via archive consolidation")
}

// ── Task-1 gap: positive sub-process-scope compensation recording ────────────

// openSubProcessWithParkDef returns a process whose sub-process contains a
// compensable service task followed by a user task. The user task keeps the
// scope OPEN after the service task completes, so we can observe that the
// CompensationRecord was written into the sub-process scope (not the root).
//
//	Outer: start → sub → end
//	Inner: inner-start → svc(CompensateAction:"x") → userTask → inner-end
func openSubProcessWithParkDef() *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "nested-park", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("svc", activity.WithTaskAction("book"), activity.WithCompensateAction("x")),
			activity.NewUserTask("userTask"),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "svc"},
			{ID: "if2", Source: "svc", Target: "userTask"},
			{ID: "if3", Source: "userTask", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-park", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", nested),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "end"},
		},
	}
}

// TestCompensableActivityInsideOpenSubProcessScopeRecords is the Task-1 gap test.
// It verifies the POSITIVE recording path: after svc completes (ActionCompleted),
// while the scope is STILL OPEN (userTask is now parked), the sub-process scope's
// Compensations[0].NodeID == "svc".
func TestCompensableActivityInsideOpenSubProcessScopeRecords(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)

	def := openSubProcessWithParkDef()

	// Start the instance: outer start → sub → inner start → svc (parked).
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "open-sub-inst-1"},
		engine.NewStartInstance(at, map[string]any{"seat": "3B"}),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1, "expected exactly one InvokeAction for inner svc")
	ia, ok := r1.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "expected InvokeAction for inner svc")
	assert.Equal(t, "book", ia.Name)

	// Verify the sub-process scope is open.
	require.Len(t, r1.State.Scopes, 1, "sub-process scope must be open")
	subScopeID := r1.State.Scopes[0].ID

	// Complete svc → should drive to userTask (parked AwaitHuman), scope stays OPEN.
	completedAt := at.Add(3 * time.Second)
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(completedAt, ia.CommandID, map[string]any{"code": "OK"}),
		engine.StepOptions{})
	require.NoError(t, err)

	// Instance must still be running (userTask is parked).
	assert.Equal(t, engine.StatusRunning, r2.State.Status)

	// The sub-process scope must still be open.
	require.Len(t, r2.State.Scopes, 1, "scope must remain open while userTask is parked")
	assert.Equal(t, subScopeID, r2.State.Scopes[0].ID)

	// Positive assertion: the sub-process scope must have exactly one compensation record
	// for "svc", proving the scope-recording path works (not just the root path).
	require.Len(t, r2.State.Scopes[0].Compensations, 1,
		"sub-process scope must carry the compensation record for svc")
	assert.Equal(t, "svc", r2.State.Scopes[0].Compensations[0].NodeID,
		"NodeID must be the compensable service task")
	assert.Equal(t, "x", r2.State.Scopes[0].Compensations[0].Action,
		"Action must be the CompensateAction configured on svc")

	// Root compensations must be empty: activity was inside the sub-process.
	assert.Empty(t, r2.State.RootCompensations,
		"root must NOT contain records from a sub-process's compensable task")
}

// ── Task 3: CompensateRequested reverse-order rollback ───────────────────────

// threeCompensableDef returns a process:
//
//	start → step1(CompensateAction:"c1") → step2(CompensateAction:"c2") →
//	         step3(CompensateAction:"c3") → userTask → end
//
// The user task keeps the process running after the three compensable steps
// complete, giving us a stable state to issue CompensateRequested against.
func threeCompensableDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "three-comp", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("step1", activity.WithTaskAction("a1"), activity.WithCompensateAction("c1")),
			activity.NewServiceTask("step2", activity.WithTaskAction("a2"), activity.WithCompensateAction("c2")),
			activity.NewServiceTask("step3", activity.WithTaskAction("a3"), activity.WithCompensateAction("c3")),
			activity.NewUserTask("userTask"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "step1"},
			{ID: "f2", Source: "step1", Target: "step2"},
			{ID: "f3", Source: "step2", Target: "step3"},
			{ID: "f4", Source: "step3", Target: "userTask"},
			{ID: "f5", Source: "userTask", Target: "end"},
		},
	}
}

// runThreeCompensableActivities drives the process through three completed compensable
// service tasks and parks at the user task. Returns the state with three CompensationRecords
// in RootCompensations (step1, step2, step3) and the process running.
func runThreeCompensableActivities(t *testing.T) engine.InstanceState {
	t.Helper()
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	def := threeCompensableDef()

	// Start.
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "three-comp-inst"},
		engine.NewStartInstance(at, map[string]any{"x": 1}),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1)
	step1ID := r1.Commands[0].(engine.InvokeAction).CommandID

	// Complete step1.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(at.Add(1*time.Second), step1ID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.Commands, 1)
	step2ID := r2.Commands[0].(engine.InvokeAction).CommandID

	// Complete step2.
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), step2ID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r3.Commands, 1)
	step3ID := r3.Commands[0].(engine.InvokeAction).CommandID

	// Complete step3 → parks at userTask.
	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewActionCompleted(at.Add(3*time.Second), step3ID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	// Expect AwaitHuman for the user task.
	require.GreaterOrEqual(t, len(r4.Commands), 1, "expected AwaitHuman")

	state := r4.State
	assert.Equal(t, engine.StatusRunning, state.Status)
	require.Len(t, state.RootCompensations, 3, "three compensation records expected")
	assert.Equal(t, "step1", state.RootCompensations[0].NodeID)
	assert.Equal(t, "step2", state.RootCompensations[1].NodeID)
	assert.Equal(t, "step3", state.RootCompensations[2].NodeID)
	return state
}

// TestCompensateRequestedRollsBackInReverseOrder asserts that CompensateRequested
// with ToNode="step1" emits compensation InvokeActions for step3 then step2 (reverse
// order, EXCLUDING step1) one at a time, and finally parks a token at step1 with
// Status back to StatusRunning.
func TestCompensateRequestedRollsBackInReverseOrder(t *testing.T) {
	def := threeCompensableDef()
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	state := runThreeCompensableActivities(t)

	// Issue CompensateRequested with ToNode = "step1" (rollback everything AFTER step1).
	compensateAt := at.Add(10 * time.Second)
	r5, err := engine.Step(t.Context(), def, state,
		engine.NewCompensateRequested(compensateAt, "step1"),
		engine.StepOptions{})
	require.NoError(t, err)

	// Status must immediately become Compensating.
	assert.Equal(t, engine.StatusCompensating, r5.State.Status)

	// First compensation InvokeAction must be for step3's action (most recently completed).
	require.Len(t, r5.Commands, 1, "CompensateRequested emits exactly one InvokeAction (for step3)")
	ia3, ok := r5.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "first compensation command must be InvokeAction for c3")
	assert.Equal(t, "c3", ia3.Name, "first compensation must be for step3 (c3)")

	// Advance: complete compensation for step3 → next in reverse is step2.
	r6, err := engine.Step(t.Context(), def, r5.State,
		engine.NewActionCompleted(compensateAt.Add(1*time.Second), ia3.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)

	// Still compensating; second compensation action emitted for step2.
	assert.Equal(t, engine.StatusCompensating, r6.State.Status)
	require.Len(t, r6.Commands, 1, "second compensation step emits one InvokeAction (for step2)")
	ia2, ok := r6.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "second compensation command must be InvokeAction for c2")
	assert.Equal(t, "c2", ia2.Name, "second compensation must be for step2 (c2)")

	// Advance: complete compensation for step2 → ToNode="step1", so walk is done.
	r7, err := engine.Step(t.Context(), def, r6.State,
		engine.NewActionCompleted(compensateAt.Add(2*time.Second), ia2.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)

	// Compensation complete: status resumes to Running (ToNode != ""), token placed at step1.
	assert.Equal(t, engine.StatusRunning, r7.State.Status)
	require.Len(t, r7.State.Tokens, 1, "one token must be placed at step1")
	assert.Equal(t, "step1", r7.State.Tokens[0].NodeID, "token must be parked at step1")

	// No compensation commands emitted (walk is done; drive parks at step1 since it's a ServiceTask).
	// The token at step1 is waiting for InvokeAction (drive fires it). So we expect an InvokeAction
	// for step1's action "a1".
	invokeStep1 := false
	for _, cmd := range r7.Commands {
		if ia, ok2 := cmd.(engine.InvokeAction); ok2 && ia.Name == "a1" {
			invokeStep1 = true
		}
	}
	assert.True(t, invokeStep1, "after rollback to step1, drive must emit InvokeAction for a1")
}

// TestCompensateRequestedFullRollback asserts that CompensateRequested with
// ToNode="" rolls back ALL root compensations (step3 → step2 → step1) and
// leaves the instance in StatusTerminated (full compensation, no resume point).
func TestCompensateRequestedFullRollback(t *testing.T) {
	def := threeCompensableDef()
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	state := runThreeCompensableActivities(t)

	compensateAt := at.Add(10 * time.Second)

	// Full rollback: ToNode == "".
	r5, err := engine.Step(t.Context(), def, state,
		engine.NewCompensateRequested(compensateAt, ""),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompensating, r5.State.Status)
	require.Len(t, r5.Commands, 1)
	ia3, ok := r5.Commands[0].(engine.InvokeAction)
	require.True(t, ok)
	assert.Equal(t, "c3", ia3.Name)

	r6, err := engine.Step(t.Context(), def, r5.State,
		engine.NewActionCompleted(compensateAt.Add(1*time.Second), ia3.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompensating, r6.State.Status)
	require.Len(t, r6.Commands, 1)
	ia2, ok := r6.Commands[0].(engine.InvokeAction)
	require.True(t, ok)
	assert.Equal(t, "c2", ia2.Name)

	r7, err := engine.Step(t.Context(), def, r6.State,
		engine.NewActionCompleted(compensateAt.Add(2*time.Second), ia2.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	// Still compensating: step1's compensation (c1) must be emitted.
	assert.Equal(t, engine.StatusCompensating, r7.State.Status)
	require.Len(t, r7.Commands, 1)
	ia1, ok := r7.Commands[0].(engine.InvokeAction)
	require.True(t, ok)
	assert.Equal(t, "c1", ia1.Name)

	r8, err := engine.Step(t.Context(), def, r7.State,
		engine.NewActionCompleted(compensateAt.Add(3*time.Second), ia1.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	// All records exhausted and ToNode == "": instance is Terminated.
	assert.Equal(t, engine.StatusTerminated, r8.State.Status,
		"full rollback with empty ToNode must leave instance in StatusTerminated")
	assert.Empty(t, r8.State.Tokens, "no tokens remain after full rollback")
}

// ── ADR-0013: ordering and nested-depth tests ─────────────────────────────────

// rootThenSubProcessCompensableDef returns a process:
//
//	start → rootSvc(CompensateAction:"root-comp") → sub(inner-svc CompensateAction:"inner-comp") → rootUserTask → end
//
// Completion order: rootSvc(1) → sub exits (inner-svc hoisted 2) → rootUserTask (parked).
// Expected reverse-order: inner-comp first (most recent), root-comp second.
func rootThenSubProcessCompensableDef() *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "nested-order", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithTaskAction("inner-book"), activity.WithCompensateAction("inner-comp")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "order-proc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("rootSvc", activity.WithTaskAction("root-book"), activity.WithCompensateAction("root-comp")),
			activity.NewSubProcess("sub", nested),
			activity.NewUserTask("rootUserTask"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "rootSvc"},
			{ID: "f2", Source: "rootSvc", Target: "sub"},
			{ID: "f3", Source: "sub", Target: "rootUserTask"},
			{ID: "f4", Source: "rootUserTask", Target: "end"},
		},
	}
}

// TestArchiveCompensationOrderingReversed asserts that after a root compensable
// activity completes, a sub-process containing a compensable inner activity
// completes (archiving the inner record), a full CompensateRequested consolidates
// the archive and emits compensation InvokeActions in strict reverse completion
// order: inner-comp (most recent) then root-comp (earlier root).
//
// ADR-0039: after sub-process closes, RootCompensations = [rootSvc] and
// ArchivedCompensations["sub"] = [{inner-svc}]. CompensateRequested triggers
// consolidation → [rootSvc, inner-svc] sorted by CompletedAt → reversed walk
// emits inner-comp first, then root-comp.
func TestArchiveCompensationOrderingReversed(t *testing.T) {
	at := time.Date(2026, 6, 21, 11, 0, 0, 0, time.UTC)
	def := rootThenSubProcessCompensableDef()

	// Step 1: start → rootSvc invoked.
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "order-inst"},
		engine.NewStartInstance(at, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1)
	rootSvcCmd := r1.Commands[0].(engine.InvokeAction)
	require.Equal(t, "root-book", rootSvcCmd.Name)

	// Step 2: complete rootSvc → inner-svc invoked.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(at.Add(1*time.Second), rootSvcCmd.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.Commands, 1)
	innerSvcCmd := r2.Commands[0].(engine.InvokeAction)
	require.Equal(t, "inner-book", innerSvcCmd.Name)

	// root-comp is recorded in root (rootSvc completed at T+1s).
	require.Len(t, r2.State.RootCompensations, 1)
	assert.Equal(t, "rootSvc", r2.State.RootCompensations[0].NodeID)

	// Step 3: complete inner-svc → sub exits (inner record archived) → parks at rootUserTask.
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), innerSvcCmd.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r3.State.Status)

	// ADR-0039: after sub-process closes, RootCompensations = [rootSvc] only.
	// The inner record is in ArchivedCompensations, NOT hoisted to root yet.
	require.Len(t, r3.State.RootCompensations, 1,
		"RootCompensations must contain only the root record (inner is archived)")
	assert.Equal(t, "rootSvc", r3.State.RootCompensations[0].NodeID)
	require.NotNil(t, r3.State.ArchivedCompensations,
		"ArchivedCompensations must be set after sub-process closes")
	require.Contains(t, r3.State.ArchivedCompensations, "sub",
		"ArchivedCompensations must contain the sub-process node ID key")
	require.Len(t, r3.State.ArchivedCompensations["sub"], 1,
		"inner-svc must be in the archive under the sub key")
	assert.Equal(t, "inner-svc", r3.State.ArchivedCompensations["sub"][0].NodeID)

	// Step 4: full CompensateRequested → consolidate (root+archive→sorted root) →
	// reverse walk: inner-comp (most recent, T+2s) first, root-comp (T+1s) second.
	compensateAt := at.Add(10 * time.Second)
	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewCompensateRequested(compensateAt, ""),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompensating, r4.State.Status)
	require.Len(t, r4.Commands, 1)
	ia1 := r4.Commands[0].(engine.InvokeAction)
	assert.Equal(t, "inner-comp", ia1.Name,
		"first emitted compensation must be inner-comp (most recently completed)")

	// Step 5: complete inner-comp → root-comp.
	r5, err := engine.Step(t.Context(), def, r4.State,
		engine.NewActionCompleted(compensateAt.Add(1*time.Second), ia1.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompensating, r5.State.Status)
	require.Len(t, r5.Commands, 1)
	ia2 := r5.Commands[0].(engine.InvokeAction)
	assert.Equal(t, "root-comp", ia2.Name,
		"second emitted compensation must be root-comp (earliest completed)")

	// Step 6: complete root-comp → full rollback done → StatusTerminated.
	r6, err := engine.Step(t.Context(), def, r5.State,
		engine.NewActionCompleted(compensateAt.Add(2*time.Second), ia2.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusTerminated, r6.State.Status)
}

// twoLevelNestedCompensableDef returns a process with two levels of sub-process nesting:
//
//	Outer: start → outerSub → rootUserTask → end
//	OuterSub: inner-start → innerSub → outer-end
//	InnerSub: g-start → grandchildSvc(CompensateAction:"gc-comp") → g-end
//
// After both scopes close, the grandchild record must be reachable at root.
func twoLevelNestedCompensableDef() *model.ProcessDefinition {
	grandchild := &model.ProcessDefinition{
		ID: "grandchild", Version: 1,
		Nodes: []model.Node{
			event.NewStart("g-start"),
			activity.NewServiceTask("grandchildSvc", activity.WithTaskAction("gc-book"), activity.WithCompensateAction("gc-comp")),
			event.NewEnd("g-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "gf1", Source: "g-start", Target: "grandchildSvc"},
			{ID: "gf2", Source: "grandchildSvc", Target: "g-end"},
		},
	}
	outerNested := &model.ProcessDefinition{
		ID: "outer-nested", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewSubProcess("innerSub", grandchild),
			event.NewEnd("outer-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "of1", Source: "inner-start", Target: "innerSub"},
			{ID: "of2", Source: "innerSub", Target: "outer-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "two-level", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("outerSub", outerNested),
			activity.NewUserTask("rootUserTask"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "outerSub"},
			{ID: "f2", Source: "outerSub", Target: "rootUserTask"},
			{ID: "f3", Source: "rootUserTask", Target: "end"},
		},
	}
}

// TestArchiveTwoLevelNestedCompensation verifies that a compensable activity at
// grandchild depth (sub-process inside a sub-process) is reachable by a full
// CompensateRequested after both scopes have closed. ADR-0039 archive-by-scope:
// each closing scope archives into ArchivedCompensations keyed by its NodeID.
// After both scopes close, ArchivedCompensations contains entries for both
// sub-process nodes. CompensateRequested consolidates all into RootCompensations
// before the walk, making gc-comp reachable.
func TestArchiveTwoLevelNestedCompensation(t *testing.T) {
	at := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	def := twoLevelNestedCompensableDef()

	// Step 1: start → grandchildSvc invoked (two sub-process scopes opened).
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "two-level-inst"},
		engine.NewStartInstance(at, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1)
	gcCmd := r1.Commands[0].(engine.InvokeAction)
	require.Equal(t, "gc-book", gcCmd.Name)

	// Two scopes must be open: outerSub scope and innerSub scope.
	require.Len(t, r1.State.Scopes, 2, "both outerSub and innerSub scopes must be open")

	// Step 2: complete grandchildSvc → both scopes drain and close.
	// archiveCompensations called for innerSub (grandchild record → archive["innerSub"]),
	// then for outerSub (no inner-scope records at outerSub level → archive may be empty).
	// Parks at rootUserTask.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(at.Add(1*time.Second), gcCmd.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)
	assert.Empty(t, r2.State.Scopes, "both scopes must be closed")

	// ADR-0039: grandchild record must be in ArchivedCompensations (NOT RootCompensations).
	assert.Empty(t, r2.State.RootCompensations,
		"RootCompensations must be empty — grandchild record is in ArchivedCompensations")
	require.NotNil(t, r2.State.ArchivedCompensations,
		"ArchivedCompensations must be non-nil after nested scopes close")

	// Step 3: full CompensateRequested → consolidation merges archive into root → walk emits gc-comp.
	compensateAt := at.Add(5 * time.Second)
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewCompensateRequested(compensateAt, ""),
		engine.StepOptions{})
	require.NoError(t, err)
	var gotActions []string
	for _, c := range r3.Commands {
		if ia, ok := c.(engine.InvokeAction); ok {
			gotActions = append(gotActions, ia.Name)
		}
	}
	require.Contains(t, gotActions, "gc-comp",
		"grandchild activity must be rollback-able via archive consolidation after two-level nesting")
}

// TestSecondCancelMidCompensationWalkDoesNotDoubleCompensate is a regression test
// for a pre-existing double-compensation bug found during the ADR-0039 review: a
// CancelRequested delivered while a TERMINAL cancel/error compensation walk is
// already in flight (Compensating.ResumeNode == "") must NOT re-enter
// beginCompensation — doing so re-emits the in-flight compensation record, running
// a money-moving action twice. Each completed compensable activity must be
// compensated AT MOST ONCE.
func TestSecondCancelMidCompensationWalkDoesNotDoubleCompensate(t *testing.T) {
	def := threeCompensableDef()
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	state := runThreeCompensableActivities(t) // 3 root compensations (step1/2/3), Running.

	invokeCount := map[string]int{}
	record := func(cmds []engine.Command) {
		for _, c := range cmds {
			if ia, ok := c.(engine.InvokeAction); ok {
				invokeCount[ia.Name]++
			}
		}
	}

	// Cancel #1 → starts the terminal compensation walk; emits c3 (most recent).
	rA, err := engine.Step(t.Context(), def, state,
		engine.NewCancelRequested(at.Add(10*time.Second)), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, rA.State.Status)
	record(rA.Commands)
	var c3cmd string
	for _, c := range rA.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "c3" {
			c3cmd = ia.CommandID
		}
	}
	require.NotEmpty(t, c3cmd, "cancel #1 must emit InvokeAction c3")

	// Cancel #2 mid-walk (BEFORE c3's ActionCompleted). It must NOT start a second
	// compensation walk: no new compensation InvokeAction.
	rB, err := engine.Step(t.Context(), def, rA.State,
		engine.NewCancelRequested(at.Add(11*time.Second)), engine.StepOptions{})
	require.NoError(t, err)
	for _, c := range rB.Commands {
		_, isInvoke := c.(engine.InvokeAction)
		assert.False(t, isInvoke,
			"a cancel during an in-flight compensation walk must not emit a second compensation InvokeAction")
	}

	// Complete the walk on the ORIGINAL cursor (c3 → c2 → c1 → terminate). If the
	// redundant cancel had restarted the walk, c3cmd would no longer be in flight
	// and this would error / mis-route.
	rC, err := engine.Step(t.Context(), def, rB.State,
		engine.NewActionCompleted(at.Add(12*time.Second), c3cmd, nil), engine.StepOptions{})
	require.NoError(t, err)
	record(rC.Commands)
	c2cmd := firstInvokeCmd(rC.Commands, "c2")
	require.NotEmpty(t, c2cmd, "walk must continue to c2 on the original cursor")

	rD, err := engine.Step(t.Context(), def, rC.State,
		engine.NewActionCompleted(at.Add(13*time.Second), c2cmd, nil), engine.StepOptions{})
	require.NoError(t, err)
	record(rD.Commands)
	c1cmd := firstInvokeCmd(rD.Commands, "c1")
	require.NotEmpty(t, c1cmd, "walk must continue to c1")

	rE, err := engine.Step(t.Context(), def, rD.State,
		engine.NewActionCompleted(at.Add(14*time.Second), c1cmd, nil), engine.StepOptions{})
	require.NoError(t, err)
	record(rE.Commands)

	// Each compensation ran EXACTLY once; the instance terminated.
	assert.Equal(t, 1, invokeCount["c1"], "c1 compensated exactly once")
	assert.Equal(t, 1, invokeCount["c2"], "c2 compensated exactly once")
	assert.Equal(t, 1, invokeCount["c3"],
		"c3 compensated exactly once (not re-emitted by the redundant mid-walk cancel)")
	assert.Equal(t, engine.StatusTerminated, rE.State.Status)
}

// TestSecondCompensateRequestedMidWalkDoesNotDoubleCompensate is the companion
// regression test for the second trigger that can re-enter beginCompensation: a
// redundant admin CompensateRequested delivered while a compensation walk is
// already in flight must NOT restart the walk (which would re-emit the in-flight
// record).
func TestSecondCompensateRequestedMidWalkDoesNotDoubleCompensate(t *testing.T) {
	def := threeCompensableDef()
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	state := runThreeCompensableActivities(t)

	invokeCount := map[string]int{}
	record := func(cmds []engine.Command) {
		for _, c := range cmds {
			if ia, ok := c.(engine.InvokeAction); ok {
				invokeCount[ia.Name]++
			}
		}
	}

	// Admin CompensateRequested (full rollback) → walk starts, emits c3.
	rA, err := engine.Step(t.Context(), def, state,
		engine.NewCompensateRequested(at.Add(10*time.Second), ""), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, rA.State.Status)
	record(rA.Commands)
	c3cmd := firstInvokeCmd(rA.Commands, "c3")
	require.NotEmpty(t, c3cmd)

	// A SECOND CompensateRequested mid-walk must NOT emit a second InvokeAction.
	rB, err := engine.Step(t.Context(), def, rA.State,
		engine.NewCompensateRequested(at.Add(11*time.Second), ""), engine.StepOptions{})
	require.NoError(t, err)
	for _, c := range rB.Commands {
		_, isInvoke := c.(engine.InvokeAction)
		assert.False(t, isInvoke,
			"a redundant CompensateRequested during an in-flight walk must not re-emit a compensation")
	}

	// The walk continues on the original cursor: c3 → c2 → c1 → terminate.
	rC, err := engine.Step(t.Context(), def, rB.State,
		engine.NewActionCompleted(at.Add(12*time.Second), c3cmd, nil), engine.StepOptions{})
	require.NoError(t, err)
	record(rC.Commands)
	c2cmd := firstInvokeCmd(rC.Commands, "c2")
	require.NotEmpty(t, c2cmd)
	rD, err := engine.Step(t.Context(), def, rC.State,
		engine.NewActionCompleted(at.Add(13*time.Second), c2cmd, nil), engine.StepOptions{})
	require.NoError(t, err)
	record(rD.Commands)
	c1cmd := firstInvokeCmd(rD.Commands, "c1")
	require.NotEmpty(t, c1cmd)
	rE, err := engine.Step(t.Context(), def, rD.State,
		engine.NewActionCompleted(at.Add(14*time.Second), c1cmd, nil), engine.StepOptions{})
	require.NoError(t, err)
	record(rE.Commands)

	assert.Equal(t, 1, invokeCount["c1"], "c1 compensated exactly once")
	assert.Equal(t, 1, invokeCount["c2"], "c2 compensated exactly once")
	assert.Equal(t, 1, invokeCount["c3"], "c3 compensated exactly once")
}

// firstInvokeCmd returns the CommandID of the first InvokeAction with the given
// name in cmds, or "" if none.
func firstInvokeCmd(cmds []engine.Command, name string) string {
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == name {
			return ia.CommandID
		}
	}
	return ""
}
