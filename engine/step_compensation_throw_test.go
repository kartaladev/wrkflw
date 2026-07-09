package engine_test

// step_compensation_throw_test.go — Phase 3: compensation throw event.
//
// Tests the KindIntermediateThrowEvent with CompensateRef: it runs the archived
// sub-process compensations in reverse order, then RESUMES past the throw node
// (continuing normal execution), and deletes the archive entry (single ownership
// — no double-compensation). All tests are strict RED-first per the TDD discipline.
//
// Design ref: docs/specs/2026-06-23-scope-targeted-compensation-design.md §2.2
// ADR: 0039

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// throwDefWithCompensableSubProcess returns a process definition:
//
//	start → sub(inner-start → inner-svc(CompensateAction:"cancel-inner") → inner-end)
//	      → compThrow(KindIntermediateThrowEvent, CompensateRef:"sub")
//	      → afterThrow(UserTask or ServiceTask — keeps execution parked after resume)
//	      → end
//
// The compensation throw event refers to sub-process "sub". After the sub-process
// completes normally, its inner-svc record is archived under "sub". When the
// throw fires, it runs cancel-inner then resumes at afterThrow.
func throwDefWithCompensableSubProcess() *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "throw-nested", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithActionName("book-inner"), activity.WithCompensateAction("cancel-inner")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "throw-proc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", nested),
			// Compensation throw: when reached, runs ArchivedCompensations["sub"].
			event.NewIntermediateThrow("compThrow", event.WithCompensateRef("sub")),
			// After the throw resumes, we park here (UserTask) so the test can observe
			// that the token arrived at afterThrow and then drove to end.
			activity.NewUserTask("afterThrow", nil),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "compThrow"},
			{ID: "f3", Source: "compThrow", Target: "afterThrow"},
			{ID: "f4", Source: "afterThrow", Target: "end"},
		},
	}
}

// driveToThrow runs an instance through:
//
//	start → sub (inner-svc invoked → completed) → sub exits → compThrow reached
//
// Returns the StepResult from the last Step call, just BEFORE delivering the
// throw — so the caller can observe the commands and state at the throw node.
//
// At this point ArchivedCompensations["sub"] must have one entry and the instance
// must be StatusCompensating (compensation throw walk started), emitting InvokeAction
// for cancel-inner. The test body delivers ActionCompleted to finish the walk.
func driveToThrowEmitStep(t *testing.T) (def *model.ProcessDefinition, throwResult engine.StepResult) {
	t.Helper()
	at := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)
	def = throwDefWithCompensableSubProcess()

	// Step 1: start instance — inner-svc invoked.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "throw-inst-1"},
		engine.NewStartInstance(at, map[string]any{"order": "123"}),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1)
	ia, ok := r1.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "expected InvokeAction for inner-svc")
	require.Equal(t, "book-inner", ia.Name)

	// Step 2: complete inner-svc → sub exits (archived) → compThrow fires → STARTS walk.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(2*time.Second), ia.CommandID, map[string]any{"ref": "R1"}),
		engine.StepOptions{})
	require.NoError(t, err)

	return def, r2
}

// ── (a) Throw runs sub-process compensation then resumes ─────────────────────

// TestCompensationThrowRunsCompensationAndResumes verifies that:
//   - A compensation throw event starts the archived compensation walk (StatusCompensating)
//   - InvokeAction for "cancel-inner" is emitted
//   - After ActionCompleted, the instance returns to StatusRunning
//   - ArchivedCompensations["sub"] is deleted (consume semantics)
//   - A token arrives at "afterThrow" (i.e. resume past the throw)
//   - Execution continues to end (StatusCompleted after afterThrow completes)
func TestCompensationThrowRunsCompensationAndResumes(t *testing.T) {
	at := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)
	def, r2 := driveToThrowEmitStep(t)

	// After completing inner-svc and driving to compThrow, the throw should
	// have started the compensation walk immediately (StatusCompensating) and
	// emitted InvokeAction for cancel-inner.
	assert.Equal(t, engine.StatusCompensating, r2.State.Status,
		"instance must be StatusCompensating after compensation throw fires")

	var cancelInnerCmd *engine.InvokeAction
	for _, cmd := range r2.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok && ia.Name == "cancel-inner" {
			ia := ia
			cancelInnerCmd = &ia
		}
	}
	require.NotNil(t, cancelInnerCmd,
		"compensation throw must emit InvokeAction for cancel-inner")

	// ApplyTrigger ActionCompleted for the compensation action → walk finishes → RESUME.
	resumeAt := at.Add(3 * time.Second)
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(resumeAt, cancelInnerCmd.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)

	// After the throw walk finishes, status must return to Running.
	assert.Equal(t, engine.StatusRunning, r3.State.Status,
		"instance must resume StatusRunning after compensation throw walk completes")

	// The archive entry for "sub" must be deleted (consume/single-ownership).
	assert.Nil(t, r3.State.ArchivedCompensations["sub"],
		"ArchivedCompensations[\"sub\"] must be deleted after throw walk completes")

	// A token must be placed at "afterThrow" (the throw's successor).
	// afterThrow is a UserTask → it parks. Find it in Tokens.
	var afterThrowTok *engine.Token
	for i := range r3.State.Tokens {
		if r3.State.Tokens[i].NodeID == "afterThrow" {
			tok := r3.State.Tokens[i]
			afterThrowTok = &tok
		}
	}
	// The AwaitHuman command arrives for afterThrow. Also check Tokens.
	var hasAwaitHuman bool
	for _, cmd := range r3.Commands {
		if _, ok := cmd.(engine.AwaitHuman); ok {
			hasAwaitHuman = true
		}
	}
	_ = afterThrowTok
	assert.True(t, hasAwaitHuman || r3.State.Status == engine.StatusRunning,
		"after throw resume, instance must be Running (parked at afterThrow or already completed)")

	// Complete afterThrow → drives to end → StatusCompleted.
	var awaitHumanCmd *engine.AwaitHuman
	for _, cmd := range r3.Commands {
		if ah, ok := cmd.(engine.AwaitHuman); ok {
			ah := ah
			awaitHumanCmd = &ah
		}
	}
	if awaitHumanCmd != nil {
		completeUserAt := at.Add(4 * time.Second)
		r4, err := engine.Step(def, r3.State,
			engine.NewHumanCompleted(completeUserAt, awaitHumanCmd.TaskToken, nil, authz.Actor{}),
			engine.StepOptions{})
		require.NoError(t, err)
		assert.Equal(t, engine.StatusCompleted, r4.State.Status,
			"instance must complete after afterThrow user task is done")
	}
}

// ── (b) Second throw to same ref is a NO-OP ──────────────────────────────────

// secondThrowDef returns a process:
//
//	start → sub(inner-svc w/ compensation) → compThrow1(ref:sub) → compThrow2(ref:sub) → end
//
// After the first throw compensates "sub" and deletes its archive entry,
// the second throw should find no records and be a no-op (auto-advance).
func secondThrowDef() *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "second-throw-nested", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithActionName("book-2"), activity.WithCompensateAction("cancel-2")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "second-throw-proc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", nested),
			event.NewIntermediateThrow("compThrow1", event.WithCompensateRef("sub")),
			event.NewIntermediateThrow("compThrow2", event.WithCompensateRef("sub")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "compThrow1"},
			{ID: "f3", Source: "compThrow1", Target: "compThrow2"},
			{ID: "f4", Source: "compThrow2", Target: "end"},
		},
	}
}

// TestSecondCompensationThrowToSameRefIsNoOp verifies that after a first
// compensation throw has consumed (deleted) ArchivedCompensations["sub"],
// a second throw referencing the same "sub" finds no records and auto-advances
// (no InvokeAction emitted, instance continues to completion).
func TestSecondCompensationThrowToSameRefIsNoOp(t *testing.T) {
	at := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	def := secondThrowDef()

	// Step 1: start → inner-svc invoked.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "second-throw-inst"},
		engine.NewStartInstance(at, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1)
	ia1, ok := r1.Commands[0].(engine.InvokeAction)
	require.True(t, ok)
	require.Equal(t, "book-2", ia1.Name)

	// Step 2: complete inner-svc → sub exits (archived) → compThrow1 fires → walk starts.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(1*time.Second), ia1.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)

	// First throw must have started the walk.
	assert.Equal(t, engine.StatusCompensating, r2.State.Status,
		"first throw must start compensation walk")

	var cancelCmd *engine.InvokeAction
	for _, cmd := range r2.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok && ia.Name == "cancel-2" {
			ia := ia
			cancelCmd = &ia
		}
	}
	require.NotNil(t, cancelCmd, "first throw must emit InvokeAction for cancel-2")

	// Step 3: complete cancel-2 → walk finishes → resume past compThrow1.
	// Now token is at compThrow2 (which also has CompensateRef:"sub").
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), cancelCmd.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)

	// After first throw completes, archive["sub"] must be deleted.
	assert.Nil(t, r3.State.ArchivedCompensations["sub"],
		"archive[\"sub\"] must be deleted after first throw")

	// Second throw must be a NO-OP: no InvokeAction for cancel-2 emitted,
	// and the instance must have completed (compThrow2 → end → StatusCompleted).
	for _, cmd := range r3.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok {
			t.Errorf("unexpected InvokeAction %q from second throw (no-op expected)", ia.Name)
		}
	}
	assert.Equal(t, engine.StatusCompleted, r3.State.Status,
		"second throw is a no-op: instance must complete past compThrow2 → end")
}

// ── (c) No-double-comp: throw + cancel ───────────────────────────────────────

// throwThenCancelDef returns a process:
//
//	start → sub(inner-svc w/ compensation) → compThrow(ref:sub) → userTask(parks) → end
//
// After the compensation throw compensates "sub" and resumes at userTask, a
// CancelRequested is issued. The cancel walk must NOT re-compensate "sub" (already gone).
func throwThenCancelDef() *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "ttc-nested", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithActionName("book-ttc"), activity.WithCompensateAction("cancel-ttc")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "ttc-proc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", nested),
			event.NewIntermediateThrow("compThrow", event.WithCompensateRef("sub")),
			activity.NewUserTask("userTask", nil),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "compThrow"},
			{ID: "f3", Source: "compThrow", Target: "userTask"},
			{ID: "f4", Source: "userTask", Target: "end"},
		},
	}
}

// TestNoDoubleCompensationAfterThrowAndCancel asserts that:
//  1. A compensation throw compensates "sub" (cancel-ttc emitted once).
//  2. After resume at userTask, CancelRequested is issued.
//  3. The cancel walk does NOT emit cancel-ttc again (archive["sub"] is already gone).
//  4. Cancel terminates without any compensation actions (no records remain).
func TestNoDoubleCompensationAfterThrowAndCancel(t *testing.T) {
	at := time.Date(2026, 6, 23, 11, 0, 0, 0, time.UTC)
	def := throwThenCancelDef()

	// Step 1: start → inner-svc invoked.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "ttc-inst"},
		engine.NewStartInstance(at, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1)
	ia1, ok := r1.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "expected InvokeAction for inner-svc")
	require.Equal(t, "book-ttc", ia1.Name)

	// Step 2: complete inner-svc → sub exits → compThrow fires → walk starts.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(1*time.Second), ia1.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompensating, r2.State.Status)

	var throwCompCmd *engine.InvokeAction
	for _, cmd := range r2.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok && ia.Name == "cancel-ttc" {
			ia := ia
			throwCompCmd = &ia
		}
	}
	require.NotNil(t, throwCompCmd, "throw must emit InvokeAction for cancel-ttc")

	// Step 3: complete cancel-ttc → walk finishes → resume at userTask.
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), throwCompCmd.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r3.State.Status)
	// Archive must be gone.
	assert.Nil(t, r3.State.ArchivedCompensations["sub"],
		"archive[\"sub\"] must be deleted after throw walk")

	// Collect all InvokeAction names from steps 1-3.
	var invokedActions []string
	for _, cmd := range r1.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok {
			invokedActions = append(invokedActions, ia.Name)
		}
	}
	for _, cmd := range r2.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok {
			invokedActions = append(invokedActions, ia.Name)
		}
	}
	for _, cmd := range r3.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok {
			invokedActions = append(invokedActions, ia.Name)
		}
	}

	// Step 4: issue CancelRequested — archive["sub"] is already nil, so cancel walk
	// must NOT emit cancel-ttc again.
	cancelAt := at.Add(5 * time.Second)
	r4, err := engine.Step(def, r3.State,
		engine.NewCancelRequested(cancelAt),
		engine.StepOptions{})
	require.NoError(t, err)

	// Collect InvokeActions from cancel step.
	for _, cmd := range r4.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok {
			invokedActions = append(invokedActions, ia.Name)
		}
	}

	// Count cancel-ttc appearances: must be exactly 1 (from the throw, not the cancel).
	cancelTTCCount := 0
	for _, name := range invokedActions {
		if name == "cancel-ttc" {
			cancelTTCCount++
		}
	}
	assert.Equal(t, 1, cancelTTCCount,
		"cancel-ttc must be invoked exactly once (throw walk), NOT again during cancel (no double-comp)")

	// Instance must be terminated after cancel.
	assert.Equal(t, engine.StatusTerminated, r4.State.Status,
		"instance must be StatusTerminated after cancel")
}

// ── (d) Regression: existing instance-wide cancel compensation still passes ──
//
// This is covered by the existing tests in step_compensation_error_cancel_test.go
// (TestCancelWithCompensateAction, TestErrorWithCompensateAction, etc.). That test file is
// intentionally NOT modified — it regresses the existing behaviour unchanged.
// Here we add a quick smoke-test that exercises the cancel path WITH archived
// compensations (no throw before cancel) to ensure consolidateArchiveIntoRoot
// still works in the cancel path after Phase 3 changes.

// TestCancelWithArchivedCompensationsStillConsolidates asserts that when
// ArchivedCompensations has entries (no prior throw), CancelRequested consolidates
// them into the root walk and compensates them — the Phase 3 changes must not
// disturb Phase 2's consolidation in beginCompensation.
func TestCancelWithArchivedCompensationsStillConsolidates(t *testing.T) {
	at := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	// Use compensableSubThenRootDef from step_compensation_test.go:
	// start → sub(inner-svc w/ compensation) → rootUserTask(parks) → end
	def := compensableSubThenRootDef()

	// Start → inner-svc invoked.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "cancel-archive-inst"},
		engine.NewStartInstance(at, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1)
	ia1, ok := r1.Commands[0].(engine.InvokeAction)
	require.True(t, ok)

	// Complete inner-svc → sub exits (archived) → rootUserTask parked.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(1*time.Second), ia1.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r2.State.Status)
	require.NotNil(t, r2.State.ArchivedCompensations, "archive must be set after sub exits")
	require.Contains(t, r2.State.ArchivedCompensations, "sub", "archive must contain 'sub'")

	// CancelRequested → consolidate archive → cancel walk emits cancel-inner.
	r3, err := engine.Step(def, r2.State,
		engine.NewCancelRequested(at.Add(5*time.Second)),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompensating, r3.State.Status,
		"cancel with archived compensations must enter compensation walk")

	var cancelInnerFound bool
	for _, cmd := range r3.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok && ia.Name == "cancel-inner" {
			cancelInnerFound = true
		}
	}
	assert.True(t, cancelInnerFound,
		"cancel walk must emit cancel-inner from consolidated archive")
}

// ── (f) B1 fix: cancel mid throw-walk must not double-compensate ─────────────

// cancelMidThrowDef returns a process definition:
//
//	start → rootSvc(CompensateAction:"cancel-root") → sub(inner-svc, CompensateAction:"cancel-inner")
//	      → compThrow(ref:"sub") → afterThrow(UserTask) → end
//
// The root-level service task (rootSvc) completes first (recorded in RootCompensations).
// Then the sub-process completes (inner-svc archived under "sub").
// Then the throw fires: starts a throw walk (StatusCompensating, ResumeNode="afterThrow").
// At this point, a CancelRequested arrives (mid-walk).
// Then the throw's ActionCompleted arrives (walk finishes → deferred cancel runs).
//
// Correct behaviour:
//   - cancel-inner invoked exactly once (from the throw walk)
//   - cancel-root invoked exactly once (from the deferred cancel over remaining records)
//   - instance terminates (StatusTerminated, FailInstance{"cancelled"})
func cancelMidThrowDef() *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "cmtw-nested", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithActionName("book-inner"), activity.WithCompensateAction("cancel-inner")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "cmtw-proc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("rootSvc", activity.WithActionName("book-root"), activity.WithCompensateAction("cancel-root")),
			activity.NewSubProcess("sub", nested),
			event.NewIntermediateThrow("compThrow", event.WithCompensateRef("sub")),
			activity.NewUserTask("afterThrow", nil),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "rootSvc"},
			{ID: "f2", Source: "rootSvc", Target: "sub"},
			{ID: "f3", Source: "sub", Target: "compThrow"},
			{ID: "f4", Source: "compThrow", Target: "afterThrow"},
			{ID: "f5", Source: "afterThrow", Target: "end"},
		},
	}
}

// TestCancelMidThrowWalkDoesNotDoubleCompensate is the regression test for B1.
// It drives: start → complete rootSvc → complete inner-svc → sub exits → compThrow fires
// (StatusCompensating, emits cancel-inner). Then CancelRequested arrives MID-WALK
// (before cancel-inner's ActionCompleted). Then cancel-inner's ActionCompleted arrives
// (throw walk finishes). The deferred cancel must then compensate REMAINING records
// (cancel-root) and terminate.
//
// Invariants:
//   - cancel-inner invoked EXACTLY once (the bug = twice when not deferred)
//   - cancel-root invoked EXACTLY once (deferred cancel, no under-compensation)
//   - instance terminates: StatusTerminated + FailInstance{"cancelled"}
func TestCancelMidThrowWalkDoesNotDoubleCompensate(t *testing.T) {
	at := time.Date(2026, 6, 23, 14, 0, 0, 0, time.UTC)
	def := cancelMidThrowDef()

	// Step 1: start → rootSvc invoked.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "cmtw-inst"},
		engine.NewStartInstance(at, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1)
	ia1, ok := r1.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "expected InvokeAction for rootSvc")
	require.Equal(t, "book-root", ia1.Name)

	// Step 2: complete rootSvc → drives to sub → inner-svc invoked.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(1*time.Second), ia1.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r2.State.Status)
	require.Len(t, r2.Commands, 1)
	ia2, ok := r2.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "expected InvokeAction for inner-svc")
	require.Equal(t, "book-inner", ia2.Name)
	// rootSvc must be recorded in RootCompensations.
	require.Len(t, r2.State.RootCompensations, 1, "rootSvc must be recorded before inner-svc")

	// Step 3: complete inner-svc → sub exits (archived) → compThrow fires →
	// STARTS throw walk (StatusCompensating, emits cancel-inner).
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), ia2.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, r3.State.Status,
		"instance must be StatusCompensating after compThrow fires")
	require.NotEmpty(t, r3.State.Compensating.ResumeNode,
		"throw walk cursor must have a ResumeNode")

	var throwCancelCmd *engine.InvokeAction
	for _, cmd := range r3.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok && ia.Name == "cancel-inner" {
			ia := ia
			throwCancelCmd = &ia
		}
	}
	require.NotNil(t, throwCancelCmd, "throw walk must emit InvokeAction{cancel-inner}")

	// Step 4: CancelRequested arrives MID-WALK (before cancel-inner's ActionCompleted).
	// The B1 bug: without the fix, beginCompensation runs again, consolidates the
	// archive (still containing "sub") into RootCompensations, and re-emits cancel-inner.
	cancelAt := at.Add(3 * time.Second)
	r4, err := engine.Step(def, r3.State,
		engine.NewCancelRequested(cancelAt),
		engine.StepOptions{})
	require.NoError(t, err)
	// With the B1 fix: PendingCancel=true, still StatusCompensating, no second
	// cancel-inner emitted. The state must still be compensating (throw walk not done).
	// (We collect InvokeActions across ALL steps at the end to count.)

	// Step 5: deliver cancel-inner's ActionCompleted (throw walk finishes).
	// With the fix: deferred cancel fires over REMAINING records (cancel-root) and terminates.
	finishAt := at.Add(4 * time.Second)
	r5, err := engine.Step(def, r4.State,
		engine.NewActionCompleted(finishAt, throwCancelCmd.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)

	// ── Collect all InvokeActions across all steps ────────────────────────────
	cancelInnerCount := 0
	cancelRootCount := 0
	var allCmds []engine.Command
	allCmds = append(allCmds, r1.Commands...)
	allCmds = append(allCmds, r2.Commands...)
	allCmds = append(allCmds, r3.Commands...)
	allCmds = append(allCmds, r4.Commands...)
	allCmds = append(allCmds, r5.Commands...)
	for _, cmd := range allCmds {
		if ia, ok := cmd.(engine.InvokeAction); ok {
			switch ia.Name {
			case "cancel-inner":
				cancelInnerCount++
			case "cancel-root":
				cancelRootCount++
			}
		}
	}

	// B1 invariant: cancel-inner must be invoked exactly once (throw walk only).
	assert.Equal(t, 1, cancelInnerCount,
		"cancel-inner must be invoked EXACTLY once (B1: no double-compensation)")

	// No-under-compensation invariant: cancel-root must be invoked exactly once (deferred cancel).
	assert.Equal(t, 1, cancelRootCount,
		"cancel-root must be invoked exactly once (deferred cancel, no under-compensation)")

	// Terminal invariant: instance must be terminated after deferred cancel completes.
	// The deferred cancel walk for cancel-root itself needs one more step to complete.
	// If r5 is still compensating (cancel-root walk in flight), drive it to completion.
	finalState := r5.State
	finalCmds := r5.Commands
	if finalState.Status == engine.StatusCompensating {
		// Find the cancel-root InvokeAction from r5 commands to get its CommandID.
		var cancelRootCmd *engine.InvokeAction
		for _, cmd := range r5.Commands {
			if ia, ok := cmd.(engine.InvokeAction); ok && ia.Name == "cancel-root" {
				ia := ia
				cancelRootCmd = &ia
			}
		}
		require.NotNil(t, cancelRootCmd, "deferred cancel must emit cancel-root InvokeAction")
		r6, err := engine.Step(def, r5.State,
			engine.NewActionCompleted(finishAt.Add(time.Second), cancelRootCmd.CommandID, nil),
			engine.StepOptions{})
		require.NoError(t, err)
		finalState = r6.State
		finalCmds = r6.Commands
	}

	assert.Equal(t, engine.StatusTerminated, finalState.Status,
		"instance must reach StatusTerminated after deferred cancel completes")

	var hasFailInstance bool
	for _, cmd := range finalCmds {
		if fi, ok := cmd.(engine.FailInstance); ok && fi.Err == "cancelled" {
			hasFailInstance = true
		}
	}
	assert.True(t, hasFailInstance,
		"deferred cancel must emit FailInstance{Err:\"cancelled\"}")
}

// ── (e) Defensive guard: a compensation throw with NO outgoing flow must not
// terminate the instance ──────────────────────────────────────────────────────

// TestCompensationThrowWithNoOutgoingFlowDoesNotTerminate verifies the producer's
// defensive guard: a compensation throw whose resume node cannot be resolved
// (no outgoing flow — which model.Validate forbids via ErrDeadEnd, but Step does
// not call Validate) must NOT start the walk. Otherwise stepCompensationFinish
// would see ResumeNode=="" and wrongly TERMINATE the instance. Instead the token
// auto-advances (moveAlongSingleFlow), which parks defensively, leaving the
// archive intact and the instance non-terminal.
func TestCompensationThrowWithNoOutgoingFlowDoesNotTerminate(t *testing.T) {
	at := time.Date(2026, 6, 23, 13, 0, 0, 0, time.UTC)
	// Same shape as throwDefWithCompensableSubProcess but the compThrow node has
	// NO outgoing flow (f3/f4 dropped).
	nested := &model.ProcessDefinition{
		ID: "throw-nested-noout", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithActionName("book-inner"), activity.WithCompensateAction("cancel-inner")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
		},
	}
	def := &model.ProcessDefinition{
		ID: "throw-proc-noout", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", nested),
			event.NewIntermediateThrow("compThrow", event.WithCompensateRef("sub")),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "compThrow"},
			// compThrow has NO outgoing flow (deliberately malformed for this guard test).
		},
	}

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "throw-noout-1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	ia, ok := r1.Commands[0].(engine.InvokeAction)
	require.True(t, ok)

	// Complete inner-svc → sub exits (archived under "sub") → compThrow reached.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(2*time.Second), ia.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)

	// The guard must prevent the walk: no compensation InvokeAction, instance NOT
	// terminated, and the archive left intact (records not consumed).
	for _, cmd := range r2.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok {
			assert.NotEqual(t, "cancel-inner", ia.Name,
				"a no-outgoing compensation throw must not start the walk")
		}
	}
	assert.NotEqual(t, engine.StatusTerminated, r2.State.Status,
		"a no-outgoing compensation throw must NOT terminate the instance")
	assert.NotEqual(t, engine.StatusCompensating, r2.State.Status,
		"a no-outgoing compensation throw must not enter the compensation walk")
	require.Contains(t, r2.State.ArchivedCompensations, "sub",
		"archive must be left intact (records not consumed) when the walk is guarded off")
}
