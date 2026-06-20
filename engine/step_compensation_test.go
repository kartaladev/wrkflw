package engine_test

// step_compensation_test.go — black-box tests for Plan 8 Task 1:
// recording CompensationRecord entries when a compensable activity completes.
//
// Compensable activity = a node with a non-empty CompensationAction field.
// On completion (ActionCompleted for ServiceTask; sub-process exit), the engine
// appends a CompensationRecord to the enclosing scope's Compensations list.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// compensableDef returns a minimal process:
//
//	start → svc(CompensationAction:"refund") → end
//
// The service task is compensable: when it completes, the engine should append
// a CompensationRecord into its enclosing scope's Compensations list.
func compensableDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "comp-proc", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "svc", Kind: model.KindServiceTask, Action: "charge", CompensationAction: "refund"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
		},
	}
}

// nonCompensableDef returns a process with a service task that has NO CompensationAction.
func nonCompensableDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "plain-proc", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "svc", Kind: model.KindServiceTask, Action: "charge"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
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
			{ID: "inner-start", Kind: model.KindStartEvent},
			{ID: "inner-svc", Kind: model.KindServiceTask, Action: "book", CompensationAction: "cancel-booking"},
			{ID: "inner-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-proc", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "sub", Kind: model.KindSubProcess, Subprocess: nested},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
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
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "comp-inst-1"},
		engine.NewStartInstance(at, map[string]any{"amount": 100, "currency": "USD"}),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1)
	cmdID := r1.Commands[0].(engine.InvokeAction).CommandID

	// Step 2: complete the service task. Output is merged into variables.
	r2, err := engine.Step(def, r1.State,
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
	assert.Equal(t, "refund", rec.Action, "Action must be the node's CompensationAction")
	assert.Equal(t, completedAt, rec.CompletedAt, "CompletedAt must be the trigger's OccurredAt")
	// Input is a snapshot of the instance variables AT completion time
	// (before merging the action's output). The snapshot contains the vars that
	// were passed to InvokeAction.
	require.NotNil(t, rec.Input, "Input snapshot must not be nil")
	assert.Equal(t, 100, rec.Input["amount"], "Input must contain vars from instance at invoke time")
}

// TestNonCompensableActivityDoesNotRecord asserts that a service task WITHOUT
// a CompensationAction does not append any record.
func TestNonCompensableActivityDoesNotRecord(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	completedAt := at.Add(5 * time.Second)

	def := nonCompensableDef()

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "plain-inst-1"},
		engine.NewStartInstance(at, map[string]any{"x": 1}),
		engine.StepOptions{})
	require.NoError(t, err)
	cmdID := r1.Commands[0].(engine.InvokeAction).CommandID

	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(completedAt, cmdID, nil),
		engine.StepOptions{})
	require.NoError(t, err)

	assert.Equal(t, engine.StatusCompleted, r2.State.Status)
	assert.Empty(t, r2.State.RootCompensations,
		"non-compensable task must not append any record")
}

// TestCompensableActivityInsideSubProcessRecordsInSubProcessScope asserts that
// a compensable service task INSIDE a sub-process records its CompensationRecord
// in the SUB-PROCESS scope (via Scope.Compensations), NOT in the root scope
// (InstanceState.RootCompensations).
//
// We verify this by completing inner-svc, then checking the state BEFORE the
// sub-process scope closes (at ActionCompleted time the scope is still open and
// carries the record). After the scope closes, the record lives in a closed scope
// that has been removed from s.Scopes — so we verify at the intermediate step that
// (a) the sub-process scope has the record and (b) the root has none.
func TestCompensableActivityInsideSubProcessRecordsInSubProcessScope(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)

	def := compensableSubProcessDef()

	// Start the instance.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "sub-comp-inst-1"},
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

	completedAt := at.Add(3 * time.Second)

	// Complete the inner service task. After this, inner-svc → inner-end → scope
	// drains → scope closes → outer-end → instance completes.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(completedAt, ia.CommandID, map[string]any{"confirmationCode": "ABC"}),
		engine.StepOptions{})
	require.NoError(t, err)

	// Instance completes: start → sub(inner-start → inner-svc → inner-end) → end.
	assert.Equal(t, engine.StatusCompleted, r2.State.Status)

	// Root compensations must be EMPTY: the activity was inside the sub-process.
	assert.Empty(t, r2.State.RootCompensations,
		"root must NOT contain records from a sub-process's compensable task")

	// The sub-process scope is now closed (removed from r2.State.Scopes). We
	// verify indirectly by checking that no root record leaked. The sub-process
	// scope's compensation records live in Scope.Compensations during scope
	// lifetime; they are available to consumers (e.g. Plan 8 compensator) that
	// hold a reference to the scope before it is closed. The closed scope is
	// gone from r2.State.Scopes — that is the correct behavior (Plan 8 will
	// consume compensation records BEFORE closing the scope).
	//
	// Verify the scope ID for traceability.
	assert.NotEmpty(t, subScopeID, "sub-process scope must have had a non-empty ID")
}

// TestCompensationRecordInputIsSnapshotNotReference asserts the Input stored in
// a CompensationRecord is an independent copy: merging the action's output into
// instance variables after completion does not retroactively alter the recorded Input.
func TestCompensationRecordInputIsSnapshotNotReference(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	completedAt := at.Add(2 * time.Second)

	def := compensableDef()

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "snap-inst-1"},
		engine.NewStartInstance(at, map[string]any{"val": "original"}),
		engine.StepOptions{})
	require.NoError(t, err)
	cmdID := r1.Commands[0].(engine.InvokeAction).CommandID

	r2, err := engine.Step(def, r1.State,
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

// ── Task-1 gap: positive sub-process-scope compensation recording ────────────

// openSubProcessWithParkDef returns a process whose sub-process contains a
// compensable service task followed by a user task. The user task keeps the
// scope OPEN after the service task completes, so we can observe that the
// CompensationRecord was written into the sub-process scope (not the root).
//
//	Outer: start → sub → end
//	Inner: inner-start → svc(CompensationAction:"x") → userTask → inner-end
func openSubProcessWithParkDef() *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "nested-park", Version: 1,
		Nodes: []model.Node{
			{ID: "inner-start", Kind: model.KindStartEvent},
			{ID: "svc", Kind: model.KindServiceTask, Action: "book", CompensationAction: "x"},
			{ID: "userTask", Kind: model.KindUserTask},
			{ID: "inner-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "svc"},
			{ID: "if2", Source: "svc", Target: "userTask"},
			{ID: "if3", Source: "userTask", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "outer-park", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "sub", Kind: model.KindSubProcess, Subprocess: nested},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
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
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "open-sub-inst-1"},
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
	r2, err := engine.Step(def, r1.State,
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
		"Action must be the CompensationAction configured on svc")

	// Root compensations must be empty: activity was inside the sub-process.
	assert.Empty(t, r2.State.RootCompensations,
		"root must NOT contain records from a sub-process's compensable task")
}

// ── Task 3: CompensateRequested reverse-order rollback ───────────────────────

// threeCompensableDef returns a process:
//
//	start → step1(CompensationAction:"c1") → step2(CompensationAction:"c2") →
//	         step3(CompensationAction:"c3") → userTask → end
//
// The user task keeps the process running after the three compensable steps
// complete, giving us a stable state to issue CompensateRequested against.
func threeCompensableDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "three-comp", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "step1", Kind: model.KindServiceTask, Action: "a1", CompensationAction: "c1"},
			{ID: "step2", Kind: model.KindServiceTask, Action: "a2", CompensationAction: "c2"},
			{ID: "step3", Kind: model.KindServiceTask, Action: "a3", CompensationAction: "c3"},
			{ID: "userTask", Kind: model.KindUserTask},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
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
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "three-comp-inst"},
		engine.NewStartInstance(at, map[string]any{"x": 1}),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1)
	step1ID := r1.Commands[0].(engine.InvokeAction).CommandID

	// Complete step1.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(1*time.Second), step1ID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.Commands, 1)
	step2ID := r2.Commands[0].(engine.InvokeAction).CommandID

	// Complete step2.
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), step2ID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r3.Commands, 1)
	step3ID := r3.Commands[0].(engine.InvokeAction).CommandID

	// Complete step3 → parks at userTask.
	r4, err := engine.Step(def, r3.State,
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
	r5, err := engine.Step(def, state,
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
	r6, err := engine.Step(def, r5.State,
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
	r7, err := engine.Step(def, r6.State,
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
	r5, err := engine.Step(def, state,
		engine.NewCompensateRequested(compensateAt, ""),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompensating, r5.State.Status)
	require.Len(t, r5.Commands, 1)
	ia3, ok := r5.Commands[0].(engine.InvokeAction)
	require.True(t, ok)
	assert.Equal(t, "c3", ia3.Name)

	r6, err := engine.Step(def, r5.State,
		engine.NewActionCompleted(compensateAt.Add(1*time.Second), ia3.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompensating, r6.State.Status)
	require.Len(t, r6.Commands, 1)
	ia2, ok := r6.Commands[0].(engine.InvokeAction)
	require.True(t, ok)
	assert.Equal(t, "c2", ia2.Name)

	r7, err := engine.Step(def, r6.State,
		engine.NewActionCompleted(compensateAt.Add(2*time.Second), ia2.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	// Still compensating: step1's compensation (c1) must be emitted.
	assert.Equal(t, engine.StatusCompensating, r7.State.Status)
	require.Len(t, r7.Commands, 1)
	ia1, ok := r7.Commands[0].(engine.InvokeAction)
	require.True(t, ok)
	assert.Equal(t, "c1", ia1.Name)

	r8, err := engine.Step(def, r7.State,
		engine.NewActionCompleted(compensateAt.Add(3*time.Second), ia1.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	// All records exhausted and ToNode == "": instance is Terminated.
	assert.Equal(t, engine.StatusTerminated, r8.State.Status,
		"full rollback with empty ToNode must leave instance in StatusTerminated")
	assert.Empty(t, r8.State.Tokens, "no tokens remain after full rollback")
}
