package engine_test

// step_compensation_error_cancel_test.go — Task 2: route cancel and
// unhandled-error terminal paths through the compensation walk before
// terminating, and make compensation best-effort.
//
// Design: docs/specs/2026-06-23-compensation-on-error-cancel-design.md
// ADR: 0034.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// cancelWithCompDef: start → compensable svc → user task (parked) → end
//
//	The user task parks execution so CancelRequested finds it mid-flight.
func cancelWithCompDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "cancel-comp-proc", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "svc", Kind: model.KindServiceTask, Action: "charge", CompensationAction: "refund"},
			{ID: "user", Kind: model.KindUserTask, Action: "review"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "user"},
			{ID: "f3", Source: "user", Target: "end"},
		},
	}
}

// errorWithCompDef: start → compensable svc1 → failing svc2 (no retry/boundary) → end
//
//	svc2's ActionFailed propagates unhandled, triggering the terminal path.
func errorWithCompDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "error-comp-proc", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "svc1", Kind: model.KindServiceTask, Action: "charge", CompensationAction: "refund"},
			{ID: "svc2", Kind: model.KindServiceTask, Action: "notify"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc1"},
			{ID: "f2", Source: "svc1", Target: "svc2"},
			{ID: "f3", Source: "svc2", Target: "end"},
		},
	}
}

// twoCompNodesDef: start → compensable svc1 → compensable svc2 → user task → end
//
//	Used for best-effort test: two compensation records, first comp action fails.
func twoCompNodesDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "two-comp-proc", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "svc1", Kind: model.KindServiceTask, Action: "step1", CompensationAction: "undo1"},
			{ID: "svc2", Kind: model.KindServiceTask, Action: "step2", CompensationAction: "undo2"},
			{ID: "user", Kind: model.KindUserTask, Action: "review"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc1"},
			{ID: "f2", Source: "svc1", Target: "svc2"},
			{ID: "f3", Source: "svc2", Target: "user"},
			{ID: "f4", Source: "user", Target: "end"},
		},
	}
}

// findInvokeAction scans commands and returns the first InvokeAction whose Name
// matches, together with its index.
func findInvokeAction(cmds []engine.Command, name string) (engine.InvokeAction, bool) {
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == name {
			return ia, true
		}
	}
	return engine.InvokeAction{}, false
}

func findFailInstance(cmds []engine.Command) (engine.FailInstance, bool) {
	for _, c := range cmds {
		if fi, ok := c.(engine.FailInstance); ok {
			return fi, true
		}
	}
	return engine.FailInstance{}, false
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// TestCancelWithCompensation verifies that CancelRequested on an instance with
// completed compensable nodes routes through the compensation walk before
// terminating.
//
// Flow:
//  1. Start → svc completes (compensable "refund") → user task parked
//  2. CancelRequested → Status=StatusCompensating, InvokeAction{Name:"refund"} emitted
//  3. ActionCompleted for the comp action → StatusTerminated + FailInstance{Err:"cancelled"}
func TestCancelWithCompensation(t *testing.T) {
	at := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	def := cancelWithCompDef()

	// Step 1: start instance.
	r0, err := engine.Step(def, engine.InstanceState{InstanceID: "c-comp-1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	ia0, ok := r0.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "first command must be InvokeAction for svc")
	assert.Equal(t, "charge", ia0.Name)

	// Step 2: svc completes → drives to user task (parked).
	r1, err := engine.Step(def, r0.State,
		engine.NewActionCompleted(at.Add(1*time.Second), ia0.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.RootCompensations, 1, "svc completion must record a compensation entry")

	// Step 3: CancelRequested while user task is parked.
	r2, err := engine.Step(def, r1.State,
		engine.NewCancelRequested(at.Add(2*time.Second)), engine.StepOptions{})
	require.NoError(t, err)

	// Assert: engine enters StatusCompensating (not immediately StatusTerminated).
	assert.Equal(t, engine.StatusCompensating, r2.State.Status,
		"CancelRequested with compensation records must set StatusCompensating")

	// Assert: the "refund" InvokeAction is emitted.
	ia, found := findInvokeAction(r2.Commands, "refund")
	assert.True(t, found, "CancelRequested must emit InvokeAction{Name:\"refund\"} for the compensation walk")

	// Assert: no FailInstance yet (it comes at the end of the walk).
	_, hasFail := findFailInstance(r2.Commands)
	assert.False(t, hasFail, "FailInstance must NOT be emitted until the walk completes")

	// Step 4: deliver ActionCompleted for the compensation action.
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(3*time.Second), ia.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Assert: instance is now StatusTerminated.
	assert.Equal(t, engine.StatusTerminated, r3.State.Status,
		"after compensation walk, cancel must yield StatusTerminated")

	// Assert: FailInstance{Err:"cancelled"} is emitted.
	fi, hasFail := findFailInstance(r3.Commands)
	require.True(t, hasFail, "FailInstance{Err:\"cancelled\"} must be emitted after the walk")
	assert.Equal(t, "cancelled", fi.Err)
}

// TestErrorWithCompensation verifies that an unhandled ActionFailed on an instance
// with completed compensable nodes routes through the compensation walk before
// setting StatusFailed.
//
// Flow:
//  1. Start → svc1 completes (compensable "refund") → svc2 starts
//  2. svc2 fails (no retry/boundary) → Status=StatusCompensating, "refund" emitted
//  3. ActionCompleted for comp action → StatusFailed + FailInstance{Err:"notify-err"}
func TestErrorWithCompensation(t *testing.T) {
	at := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	def := errorWithCompDef()

	// Step 1: start → svc1.
	r0, err := engine.Step(def, engine.InstanceState{InstanceID: "e-comp-1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	ia0, ok := r0.Commands[0].(engine.InvokeAction)
	require.True(t, ok)
	assert.Equal(t, "charge", ia0.Name)

	// Step 2: svc1 completes → drives to svc2.
	r1, err := engine.Step(def, r0.State,
		engine.NewActionCompleted(at.Add(1*time.Second), ia0.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.RootCompensations, 1, "svc1 must record a compensation entry")
	ia1, ok := r1.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "svc2 must be invoked")
	assert.Equal(t, "notify", ia1.Name)

	// Step 3: svc2 fails unhandled.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionFailed(at.Add(2*time.Second), ia1.CommandID, "notify-err", false), engine.StepOptions{})
	require.NoError(t, err)

	// Assert: engine enters StatusCompensating.
	assert.Equal(t, engine.StatusCompensating, r2.State.Status,
		"unhandled error with compensation records must set StatusCompensating")

	// Assert: "refund" InvokeAction emitted.
	ia, found := findInvokeAction(r2.Commands, "refund")
	assert.True(t, found, "unhandled error must emit InvokeAction{Name:\"refund\"}")

	// Assert: no FailInstance yet.
	_, hasFail := findFailInstance(r2.Commands)
	assert.False(t, hasFail, "FailInstance must NOT be emitted until walk completes")

	// Step 4: compensation action completes.
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(3*time.Second), ia.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Assert: StatusFailed (not StatusTerminated).
	assert.Equal(t, engine.StatusFailed, r3.State.Status,
		"after compensation walk on error, status must be StatusFailed")

	// Assert: FailInstance{Err:"notify-err"}.
	fi, hasFail := findFailInstance(r3.Commands)
	require.True(t, hasFail, "FailInstance with the error code must be emitted after the walk")
	assert.Equal(t, "notify-err", fi.Err)
}

// TestEmptyRecordsCancelImmediate verifies that CancelRequested with NO compensation
// records still terminates immediately (unchanged behaviour).
func TestEmptyRecordsCancelImmediate(t *testing.T) {
	at := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	// Non-compensable process: no CompensationAction on svc.
	def := &model.ProcessDefinition{
		ID: "no-comp-proc", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "svc", Kind: model.KindServiceTask, Action: "charge"},
			{ID: "user", Kind: model.KindUserTask, Action: "review"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "user"},
			{ID: "f3", Source: "user", Target: "end"},
		},
	}

	r0, err := engine.Step(def, engine.InstanceState{InstanceID: "no-comp-cancel"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	ia, _ := r0.Commands[0].(engine.InvokeAction)

	r1, err := engine.Step(def, r0.State,
		engine.NewActionCompleted(at.Add(1*time.Second), ia.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Empty(t, r1.State.RootCompensations, "non-compensable svc must not create records")

	// CancelRequested with no compensation records: immediate termination.
	r2, err := engine.Step(def, r1.State,
		engine.NewCancelRequested(at.Add(2*time.Second)), engine.StepOptions{})
	require.NoError(t, err)

	assert.Equal(t, engine.StatusTerminated, r2.State.Status, "empty-records cancel must terminate immediately")
	fi, hasFail := findFailInstance(r2.Commands)
	require.True(t, hasFail, "FailInstance{Err:\"cancelled\"} must be emitted immediately")
	assert.Equal(t, "cancelled", fi.Err)
}

// TestEmptyRecordsErrorImmediate verifies that an unhandled error with NO
// compensation records still sets StatusFailed immediately (unchanged behaviour).
func TestEmptyRecordsErrorImmediate(t *testing.T) {
	at := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	def := &model.ProcessDefinition{
		ID: "no-comp-err-proc", Version: 1,
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

	r0, err := engine.Step(def, engine.InstanceState{InstanceID: "no-comp-err"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	ia, _ := r0.Commands[0].(engine.InvokeAction)

	// Fail unhandled with no compensation records.
	r1, err := engine.Step(def, r0.State,
		engine.NewActionFailed(at.Add(1*time.Second), ia.CommandID, "boom", false), engine.StepOptions{})
	require.NoError(t, err)

	assert.Equal(t, engine.StatusFailed, r1.State.Status, "empty-records error must set StatusFailed immediately")
	fi, hasFail := findFailInstance(r1.Commands)
	require.True(t, hasFail, "FailInstance with error code must be emitted immediately")
	assert.Equal(t, "boom", fi.Err)
}

// TestBestEffortCompActionFailure verifies that when a compensation action itself
// fails during the walk, the engine skips that record and continues to the next
// (best-effort), eventually reaching the terminal outcome.
//
// Flow:
//  1. Start → svc1 completes (comp "undo1") → svc2 completes (comp "undo2") → user parked
//  2. CancelRequested → StatusCompensating, "undo2" emitted (reverse order)
//  3. ActionFailed for "undo2" comp action → engine skips and emits "undo1"
//  4. ActionCompleted for "undo1" → StatusTerminated + FailInstance{Err:"cancelled"}
func TestBestEffortCompActionFailure(t *testing.T) {
	at := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	def := twoCompNodesDef()

	// Step 1: start → svc1.
	r0, err := engine.Step(def, engine.InstanceState{InstanceID: "be-comp-1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	ia0, _ := r0.Commands[0].(engine.InvokeAction)
	require.Equal(t, "step1", ia0.Name)

	// Step 2: svc1 completes → svc2 invoked.
	r1, err := engine.Step(def, r0.State,
		engine.NewActionCompleted(at.Add(1*time.Second), ia0.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.RootCompensations, 1)
	ia1, _ := r1.Commands[0].(engine.InvokeAction)
	require.Equal(t, "step2", ia1.Name)

	// Step 3: svc2 completes → user task parked.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(2*time.Second), ia1.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.State.RootCompensations, 2)

	// Step 4: CancelRequested → compensation walk starts with "undo2" (most recent).
	r3, err := engine.Step(def, r2.State,
		engine.NewCancelRequested(at.Add(3*time.Second)), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompensating, r3.State.Status)
	undo2, found := findInvokeAction(r3.Commands, "undo2")
	require.True(t, found, "first compensation action in reverse order must be undo2")

	// Step 5: "undo2" compensation action FAILS → best-effort: skip and emit "undo1".
	r4, err := engine.Step(def, r3.State,
		engine.NewActionFailed(at.Add(4*time.Second), undo2.CommandID, "comp-fail", false), engine.StepOptions{})
	require.NoError(t, err)

	// Assert: still compensating (walk continues).
	assert.Equal(t, engine.StatusCompensating, r4.State.Status,
		"failed compensation action must NOT strand the instance")

	// Assert: "undo1" is now emitted (walk advanced).
	undo1, found := findInvokeAction(r4.Commands, "undo1")
	require.True(t, found, "after a failed comp action, the walk must advance to undo1")

	// No FailInstance yet.
	_, hasFail := findFailInstance(r4.Commands)
	assert.False(t, hasFail, "FailInstance must NOT be emitted until walk fully completes")

	// Step 6: "undo1" completes → StatusTerminated + FailInstance{cancelled}.
	r5, err := engine.Step(def, r4.State,
		engine.NewActionCompleted(at.Add(5*time.Second), undo1.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)

	assert.Equal(t, engine.StatusTerminated, r5.State.Status,
		"after best-effort walk completes, cancel must yield StatusTerminated")
	fi, hasFail := findFailInstance(r5.Commands)
	require.True(t, hasFail, "FailInstance{Err:\"cancelled\"} must be emitted after walk")
	assert.Equal(t, "cancelled", fi.Err)
}

// TestRedeliveredCancelIdempotent verifies that a second CancelRequested delivered
// to an already-terminal (StatusTerminated) instance — which has completed a
// compensation walk — does NOT re-run compensation.
//
// The bug: RootCompensations was never cleared after the walk, so the
// CancelRequested guard (len(s.RootCompensations) > 0) stayed true and the engine
// would re-emit every compensation InvokeAction, double-compensating money-moving
// actions (e.g. issuing a double refund).
//
// Flow:
//  1. Start → compensable svc completes ("refund") → user task parked
//  2. CancelRequested → StatusCompensating, "refund" InvokeAction emitted
//  3. ActionCompleted for comp action → StatusTerminated (walk done, RootCompensations cleared)
//  4. Second CancelRequested on terminal state → no new InvokeAction, status stays terminal
func TestRedeliveredCancelIdempotent(t *testing.T) {
	at := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	def := cancelWithCompDef()

	// Step 1: start instance — drives to svc (InvokeAction emitted).
	r0, err := engine.Step(def, engine.InstanceState{InstanceID: "re-cancel-1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	ia0, ok := r0.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "first command must be InvokeAction for svc")
	require.Equal(t, "charge", ia0.Name)

	// Step 2: svc completes → user task parked (compensation record added).
	r1, err := engine.Step(def, r0.State,
		engine.NewActionCompleted(at.Add(1*time.Second), ia0.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.RootCompensations, 1, "svc completion must record a compensation entry")

	// Step 3: CancelRequested → StatusCompensating, "refund" InvokeAction emitted.
	r2, err := engine.Step(def, r1.State,
		engine.NewCancelRequested(at.Add(2*time.Second)), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompensating, r2.State.Status)
	ia, found := findInvokeAction(r2.Commands, "refund")
	require.True(t, found, "CancelRequested must emit InvokeAction{Name:\"refund\"}")

	// Step 4: deliver ActionCompleted for the compensation action → StatusTerminated.
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(3*time.Second), ia.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusTerminated, r3.State.Status,
		"after compensation walk, cancel must yield StatusTerminated")
	fi, hasFail := findFailInstance(r3.Commands)
	require.True(t, hasFail, "FailInstance{Err:\"cancelled\"} must be emitted after the walk")
	require.Equal(t, "cancelled", fi.Err)

	// Assert: RootCompensations cleared after full-rollback — no records remain.
	assert.Empty(t, r3.State.RootCompensations,
		"RootCompensations must be cleared after a full-rollback compensation walk completes")

	// Step 5 (THE IDEMPOTENCY CHECK): deliver a SECOND CancelRequested on the
	// already-terminal state.
	r4, err := engine.Step(def, r3.State,
		engine.NewCancelRequested(at.Add(4*time.Second)), engine.StepOptions{})
	require.NoError(t, err)

	// Assert: no new compensation InvokeAction (no double-compensation).
	for _, cmd := range r4.Commands {
		_, isInvoke := cmd.(engine.InvokeAction)
		assert.False(t, isInvoke,
			"second CancelRequested on terminal state must NOT emit any InvokeAction (double-compensation bug)")
	}

	// Assert: instance stays terminal (no regression to StatusCompensating or re-run).
	assert.Equal(t, engine.StatusTerminated, r4.State.Status,
		"second CancelRequested on terminal instance must keep StatusTerminated")
}

// TestNoDoubleCompensationAfterArchiveConsolidate is the no-double-compensation
// invariant test for ADR-0039 archive-by-scope. It verifies that:
//
//  1. A sub-process with a compensable task closes → record in ArchivedCompensations
//  2. CancelRequested → consolidation merges archive into RootCompensations →
//     compensation walk begins → compensation completes → instance Terminated
//  3. A SECOND CancelRequested on the already-Terminated instance emits NO
//     InvokeAction (idempotent: archive is nil + RootCompensations cleared).
//
// Uses compensableSubThenRootDef() (outer: start→sub→rootUserTask→end;
// inner: inner-start→inner-svc(cancel-inner)→inner-end).
func TestNoDoubleCompensationAfterArchiveConsolidate(t *testing.T) {
	at := time.Date(2026, 6, 23, 11, 0, 0, 0, time.UTC)
	// Import compensableSubThenRootDef via package-level access is not possible
	// here (different file). Inline the same definition.
	nested := func() *model.ProcessDefinition {
		inner := &model.ProcessDefinition{
			ID: "no-double-nested", Version: 1,
			Nodes: []model.Node{
				{ID: "inner-start", Kind: model.KindStartEvent},
				{ID: "inner-svc", Kind: model.KindServiceTask, Action: "book-inner", CompensationAction: "cancel-inner"},
				{ID: "inner-end", Kind: model.KindEndEvent},
			},
			Flows: []model.SequenceFlow{
				{ID: "if1", Source: "inner-start", Target: "inner-svc"},
				{ID: "if2", Source: "inner-svc", Target: "inner-end"},
			},
		}
		return &model.ProcessDefinition{
			ID: "no-double-outer", Version: 1,
			Nodes: []model.Node{
				{ID: "start", Kind: model.KindStartEvent},
				{ID: "sub", Kind: model.KindSubProcess, Subprocess: inner},
				{ID: "rootUserTask", Kind: model.KindUserTask},
				{ID: "end", Kind: model.KindEndEvent},
			},
			Flows: []model.SequenceFlow{
				{ID: "f1", Source: "start", Target: "sub"},
				{ID: "f2", Source: "sub", Target: "rootUserTask"},
				{ID: "f3", Source: "rootUserTask", Target: "end"},
			},
		}
	}()

	// Step 1: start → inner-svc invoked.
	r1, err := engine.Step(nested, engine.InstanceState{InstanceID: "no-double-1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	ia0 := r1.Commands[0].(engine.InvokeAction)
	require.Equal(t, "book-inner", ia0.Name)

	// Step 2: complete inner-svc → sub closes → record archived → parked at rootUserTask.
	r2, err := engine.Step(nested, r1.State,
		engine.NewActionCompleted(at.Add(1*time.Second), ia0.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r2.State.Status)
	require.NotNil(t, r2.State.ArchivedCompensations,
		"archive must be populated after sub-process closes")

	// Step 3: CancelRequested → consolidate + walk begins → "cancel-inner" emitted.
	r3, err := engine.Step(nested, r2.State,
		engine.NewCancelRequested(at.Add(2*time.Second)), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, r3.State.Status)
	ia1, found := findInvokeAction(r3.Commands, "cancel-inner")
	require.True(t, found, "CancelRequested must emit InvokeAction{Name:\"cancel-inner\"} via consolidation")

	// Step 4: ActionCompleted for cancel-inner → walk finishes → StatusTerminated.
	r4, err := engine.Step(nested, r3.State,
		engine.NewActionCompleted(at.Add(3*time.Second), ia1.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusTerminated, r4.State.Status)

	// Verify archive is nil and RootCompensations cleared after walk completes.
	assert.Nil(t, r4.State.ArchivedCompensations,
		"ArchivedCompensations must be nil after consolidation + walk")
	assert.Empty(t, r4.State.RootCompensations,
		"RootCompensations must be cleared after walk completes")

	// Step 5 (no-double-compensation): second CancelRequested on Terminated instance.
	r5, err := engine.Step(nested, r4.State,
		engine.NewCancelRequested(at.Add(4*time.Second)), engine.StepOptions{})
	require.NoError(t, err)

	// Must NOT emit any InvokeAction — no double compensation.
	for _, cmd := range r5.Commands {
		_, isInvoke := cmd.(engine.InvokeAction)
		assert.False(t, isInvoke,
			"second CancelRequested on terminal state must NOT emit InvokeAction (no double-compensation)")
	}
	assert.Equal(t, engine.StatusTerminated, r5.State.Status,
		"second CancelRequested must keep StatusTerminated")
}
