package engine_test

// compensation_throw_test.go — ADR-0120: dedicated CompensationThrowEvent
// (model.KindCompensationThrowEvent). Exercises the new engine strategy for
// BOTH a scope-wide throw (empty CompensateRef — the throwing scope's completed
// compensable activities, reverse order, throw-then-continue) and a targeted
// throw (CompensateRef set — the archived sub-process records, ported from the
// legacy IntermediateThrowEvent path). Covers reverse order + resume,
// second-throw no-op (records cleared), targeted parity against the new kind,
// scope-local vs whole-instance breadth, and compensate-once (a later cancel
// does not re-run the already-run compensations).
//
// engine.Step is a pure, context-free function; the table form below therefore
// omits the table-test skill's ctx modifier (there is no context to cancel).
//
// ADR: 0120

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

// invokeActionNamed returns the first InvokeAction in cmds whose Name matches, or
// nil when none is present.
func invokeActionNamed(cmds []engine.Command, name string) *engine.InvokeAction {
	for _, cmd := range cmds {
		if ia, ok := cmd.(engine.InvokeAction); ok && ia.Name == name {
			ia := ia
			return &ia
		}
	}
	return nil
}

// anyInvokeAction returns the first InvokeAction in cmds regardless of name.
func anyInvokeAction(cmds []engine.Command) *engine.InvokeAction {
	for _, cmd := range cmds {
		if ia, ok := cmd.(engine.InvokeAction); ok {
			ia := ia
			return &ia
		}
	}
	return nil
}

// firstAwaitHuman returns the first AwaitHuman command in cmds, or nil.
func firstAwaitHuman(cmds []engine.Command) *engine.AwaitHuman {
	for _, cmd := range cmds {
		if ah, ok := cmd.(engine.AwaitHuman); ok {
			ah := ah
			return &ah
		}
	}
	return nil
}

// rootSagaWithScopeWideThrow returns:
//
//	start → svcA(doA/undoA) → svcB(doB/undoB) → rb(CompensateThrow, scope-wide)
//	      → afterThrow(UserTask) → end
//
// Two root-level compensable service tasks complete, then a scope-wide
// compensation throw fires: it must run undoB then undoA (reverse order) and
// RESUME at afterThrow (throw-then-continue), NOT terminate.
func rootSagaWithScopeWideThrow() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "scopewide-proc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svcA", activity.WithTaskAction("doA"), activity.WithCompensateAction("undoA")),
			activity.NewServiceTask("svcB", activity.WithTaskAction("doB"), activity.WithCompensateAction("undoB")),
			event.NewCompensateThrow("rb"),
			activity.NewUserTask("afterThrow"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svcA"},
			{ID: "f2", Source: "svcA", Target: "svcB"},
			{ID: "f3", Source: "svcB", Target: "rb"},
			{ID: "f4", Source: "rb", Target: "afterThrow"},
			{ID: "f5", Source: "afterThrow", Target: "end"},
		},
	}
}

// driveToScopeWideThrow drives start → complete doA → complete doB → rb fires,
// returning the def and the StepResult at the throw (walk started, emitting the
// first reverse compensation InvokeAction).
func driveToScopeWideThrow(t *testing.T, def *model.ProcessDefinition, instID string, at time.Time) engine.StepResult {
	t.Helper()

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: instID},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	doA := invokeActionNamed(r1.Commands, "doA")
	require.NotNil(t, doA, "expected InvokeAction for doA")

	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(1*time.Second), doA.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	doB := invokeActionNamed(r2.Commands, "doB")
	require.NotNil(t, doB, "expected InvokeAction for doB")

	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), doB.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	return r3
}

// ── (1) Scope-wide reverse order + resume + records cleared ──────────────────

// TestCompensationThrowScopeWideReverseAndResume verifies the scope-wide throw:
// completed compensable activities run in reverse order (undoB then undoA), the
// instance RESUMES at the throw's successor (afterThrow) rather than terminating,
// and RootCompensations is cleared afterwards (compensate-once).
func TestCompensationThrowScopeWideReverseAndResume(t *testing.T) {
	at := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	def := rootSagaWithScopeWideThrow()

	r3 := driveToScopeWideThrow(t, def, "scopewide-1", at)

	// Throw fired: instance is compensating, first reverse action is undoB.
	assert.Equal(t, engine.StatusCompensating, r3.State.Status,
		"scope-wide throw must enter the compensation walk")
	undoB := invokeActionNamed(r3.Commands, "undoB")
	require.NotNil(t, undoB, "reverse walk must emit undoB first (last-completed activity)")
	assert.Nil(t, invokeActionNamed(r3.Commands, "undoA"),
		"undoA must not be emitted before undoB (reverse order)")

	// Complete undoB → walk advances to undoA.
	r4, err := engine.Step(def, r3.State,
		engine.NewActionCompleted(at.Add(3*time.Second), undoB.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	undoA := invokeActionNamed(r4.Commands, "undoA")
	require.NotNil(t, undoA, "reverse walk must emit undoA after undoB")

	// Complete undoA → walk finishes → RESUME at afterThrow (UserTask parks).
	r5, err := engine.Step(def, r4.State,
		engine.NewActionCompleted(at.Add(4*time.Second), undoA.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r5.State.Status,
		"instance must resume Running after the scope-wide throw walk completes")
	assert.Nil(t, r5.State.RootCompensations,
		"RootCompensations must be cleared after a scope-wide throw (compensate-once)")
	require.NotNil(t, firstAwaitHuman(r5.Commands),
		"resume must park at afterThrow (AwaitHuman)")
}

// ── (2) Second scope-wide throw is a NO-OP (records cleared) ──────────────────

// secondScopeWideThrowDef returns:
//
//	start → svcA(doA/undoA) → svcB(doB/undoB) → rb1(throw) → rb2(throw) → end
//
// After rb1 compensates both activities and clears RootCompensations, rb2 finds
// no records and auto-advances to end (StatusCompleted). No compensation action
// is emitted twice.
func secondScopeWideThrowDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "second-scopewide-proc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svcA", activity.WithTaskAction("doA"), activity.WithCompensateAction("undoA")),
			activity.NewServiceTask("svcB", activity.WithTaskAction("doB"), activity.WithCompensateAction("undoB")),
			event.NewCompensateThrow("rb1"),
			event.NewCompensateThrow("rb2"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svcA"},
			{ID: "f2", Source: "svcA", Target: "svcB"},
			{ID: "f3", Source: "svcB", Target: "rb1"},
			{ID: "f4", Source: "rb1", Target: "rb2"},
			{ID: "f5", Source: "rb2", Target: "end"},
		},
	}
}

// TestSecondScopeWideThrowIsNoOp verifies compensate-once at the record level: a
// second scope-wide throw after the first drained-and-cleared the scope's
// records finds nothing to do and completes the instance, each compensation
// action having run exactly once.
func TestSecondScopeWideThrowIsNoOp(t *testing.T) {
	at := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	def := secondScopeWideThrowDef()

	r3 := driveToScopeWideThrow(t, def, "second-scopewide-1", at)
	assert.Equal(t, engine.StatusCompensating, r3.State.Status)

	undoCount := map[string]int{}
	countUndos := func(cmds []engine.Command) {
		for _, cmd := range cmds {
			if ia, ok := cmd.(engine.InvokeAction); ok {
				undoCount[ia.Name]++
			}
		}
	}
	countUndos(r3.Commands)

	undoB := invokeActionNamed(r3.Commands, "undoB")
	require.NotNil(t, undoB)
	r4, err := engine.Step(def, r3.State,
		engine.NewActionCompleted(at.Add(3*time.Second), undoB.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	countUndos(r4.Commands)

	undoA := invokeActionNamed(r4.Commands, "undoA")
	require.NotNil(t, undoA)
	// Completing undoA finishes rb1's walk, resumes at rb2 which — finding zero
	// records — auto-advances to end.
	r5, err := engine.Step(def, r4.State,
		engine.NewActionCompleted(at.Add(4*time.Second), undoA.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	countUndos(r5.Commands)

	assert.Equal(t, engine.StatusCompleted, r5.State.Status,
		"second scope-wide throw is a no-op: instance completes past rb2 → end")
	assert.Equal(t, 1, undoCount["undoA"], "undoA must run exactly once")
	assert.Equal(t, 1, undoCount["undoB"], "undoB must run exactly once")
}

// ── (3) Targeted throw parity against the NEW kind ───────────────────────────

// targetedNewKindDef mirrors throwDefWithCompensableSubProcess (the legacy
// IntermediateThrowEvent targeted throw) but uses the NEW dedicated kind:
//
//	start → sub(inner-svc, comp:cancel-inner) → tgt(CompensateThrow, ref:"sub")
//	      → afterThrow(UserTask) → end
func targetedNewKindDef() *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "tgt-nested", Version: 1,
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
		ID: "tgt-proc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", nested),
			event.NewCompensateThrow("tgt", event.WithCompensateTargetRef("sub")),
			activity.NewUserTask("afterThrow"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "tgt"},
			{ID: "f3", Source: "tgt", Target: "afterThrow"},
			{ID: "f4", Source: "afterThrow", Target: "end"},
		},
	}
}

// TestCompensationThrowTargetedParity verifies the ported targeted branch: the
// new kind with CompensateTargetRef runs the archived sub-process compensation
// (cancel-inner), deletes the archive entry (single ownership), and resumes past
// the throw — identical to the legacy IntermediateThrowEvent targeted throw.
func TestCompensationThrowTargetedParity(t *testing.T) {
	at := time.Date(2026, 7, 10, 11, 0, 0, 0, time.UTC)
	def := targetedNewKindDef()

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "tgt-1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	book := invokeActionNamed(r1.Commands, "book-inner")
	require.NotNil(t, book)

	// Complete inner-svc → sub exits (archived under "sub") → tgt fires → walk.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(1*time.Second), book.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompensating, r2.State.Status,
		"targeted throw (new kind) must start the archived compensation walk")
	cancelInner := invokeActionNamed(r2.Commands, "cancel-inner")
	require.NotNil(t, cancelInner, "targeted throw must emit cancel-inner")

	// Complete cancel-inner → walk finishes → resume, archive consumed.
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), cancelInner.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r3.State.Status,
		"instance must resume Running after the targeted throw walk completes")
	assert.Nil(t, r3.State.ArchivedCompensations["sub"],
		"ArchivedCompensations[\"sub\"] must be deleted (single ownership)")
	require.NotNil(t, firstAwaitHuman(r3.Commands), "resume must park at afterThrow")

	// Targeted throw RETAINS RootCompensations (unrelated outer records); here
	// there are none, so it stays empty — the point is it must not have consumed
	// beyond the archive. Complete afterThrow → StatusCompleted.
	ah := firstAwaitHuman(r3.Commands)
	r4, err := engine.Step(def, r3.State,
		engine.NewHumanCompleted(at.Add(3*time.Second), ah.TaskToken, nil, authz.Actor{}),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r4.State.Status)
}

// ── (4) Scope-local vs whole-instance breadth ────────────────────────────────

// breadthDef returns:
//
//	start → rootSvc(do-root/undo-root) → sub(inner-svc, comp:undo-inner)
//	      → rb(CompensateThrow, scope-wide, breadth per opts) → end
//
// rootSvc records into RootCompensations; the sub-process's inner-svc is archived
// under "sub". A whole-instance throw (default) consolidates the archive and
// compensates BOTH undo-inner and undo-root; a scope-local throw compensates only
// undo-root (root-direct), leaving the archived sub-process records untouched.
func breadthDef(rb model.Node) *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "breadth-nested", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithTaskAction("book-inner"), activity.WithCompensateAction("undo-inner")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "breadth-proc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("rootSvc", activity.WithTaskAction("do-root"), activity.WithCompensateAction("undo-root")),
			activity.NewSubProcess("sub", nested),
			rb,
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "rootSvc"},
			{ID: "f2", Source: "rootSvc", Target: "sub"},
			{ID: "f3", Source: "sub", Target: "rb"},
			{ID: "f4", Source: "rb", Target: "end"},
		},
	}
}

// TestCompensationThrowScopeWideBreadth verifies the configurable root breadth:
// the whole-instance default consolidates archived sub-process records and
// compensates them, whereas WithScopeLocalCompensation compensates only
// root-direct records and leaves the archived sub-process records intact.
func TestCompensationThrowScopeWideBreadth(t *testing.T) {
	type testCase struct {
		name   string
		rb     model.Node
		assert func(t *testing.T, undos map[string]int, final engine.InstanceState)
	}

	cases := []testCase{
		{
			name: "whole-instance default compensates archived sub-process records",
			rb:   event.NewCompensateThrow("rb"),
			assert: func(t *testing.T, undos map[string]int, final engine.InstanceState) {
				assert.Equal(t, 1, undos["undo-inner"],
					"whole-instance default must compensate the archived sub-process record")
				assert.Equal(t, 1, undos["undo-root"],
					"whole-instance default must compensate the root-direct record")
			},
		},
		{
			name: "scope-local excludes archived sub-process records",
			rb:   event.NewCompensateThrow("rb", event.WithScopeLocalCompensation()),
			assert: func(t *testing.T, undos map[string]int, final engine.InstanceState) {
				assert.Equal(t, 0, undos["undo-inner"],
					"scope-local must NOT compensate archived sub-process records")
				assert.Equal(t, 1, undos["undo-root"],
					"scope-local must still compensate the root-direct record")
				require.Contains(t, final.ArchivedCompensations, "sub",
					"scope-local must leave the archived sub-process records intact")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			at := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
			def := breadthDef(tc.rb)

			// start → rootSvc invoked.
			r1, err := engine.Step(def, engine.InstanceState{InstanceID: "breadth-" + tc.name},
				engine.NewStartInstance(at, nil), engine.StepOptions{})
			require.NoError(t, err)
			doRoot := invokeActionNamed(r1.Commands, "do-root")
			require.NotNil(t, doRoot)

			// complete rootSvc → sub → inner-svc invoked.
			r2, err := engine.Step(def, r1.State,
				engine.NewActionCompleted(at.Add(1*time.Second), doRoot.CommandID, nil),
				engine.StepOptions{})
			require.NoError(t, err)
			bookInner := invokeActionNamed(r2.Commands, "book-inner")
			require.NotNil(t, bookInner)

			// complete inner-svc → sub exits (archived) → rb fires → walk starts.
			r3, err := engine.Step(def, r2.State,
				engine.NewActionCompleted(at.Add(2*time.Second), bookInner.CommandID, nil),
				engine.StepOptions{})
			require.NoError(t, err)
			require.Equal(t, engine.StatusCompensating, r3.State.Status,
				"scope-wide throw must enter the compensation walk")

			undos := map[string]int{}
			cur := r3
			// Drain the reverse walk: each step emits one compensation InvokeAction
			// until the walk finishes and the instance leaves StatusCompensating.
			for step := 0; step < 10; step++ {
				ia := anyInvokeAction(cur.Commands)
				if ia != nil {
					undos[ia.Name]++
				}
				if cur.State.Status != engine.StatusCompensating {
					break
				}
				require.NotNil(t, ia, "a compensating step must carry a compensation InvokeAction")
				next, err := engine.Step(def, cur.State,
					engine.NewActionCompleted(at.Add(time.Duration(3+step)*time.Second), ia.CommandID, nil),
					engine.StepOptions{})
				require.NoError(t, err)
				cur = next
			}
			tc.assert(t, undos, cur.State)
		})
	}
}

// ── (5) Compensate-once: cancel after a scope-wide throw does not re-run ──────

// scopeWideThenCancelDef returns:
//
//	start → svcA(doA/undoA) → svcB(doB/undoB) → rb(throw) → userTask → end
//
// After the scope-wide throw compensates both and resumes at userTask, a
// CancelRequested must NOT re-run undoA/undoB (records cleared on the throw
// finish), and the instance terminates.
func scopeWideThenCancelDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "scopewide-cancel-proc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svcA", activity.WithTaskAction("doA"), activity.WithCompensateAction("undoA")),
			activity.NewServiceTask("svcB", activity.WithTaskAction("doB"), activity.WithCompensateAction("undoB")),
			event.NewCompensateThrow("rb"),
			activity.NewUserTask("userTask"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svcA"},
			{ID: "f2", Source: "svcA", Target: "svcB"},
			{ID: "f3", Source: "svcB", Target: "rb"},
			{ID: "f4", Source: "rb", Target: "userTask"},
			{ID: "f5", Source: "userTask", Target: "end"},
		},
	}
}

// TestScopeWideThrowCompensateOnceAcrossCancel asserts that once a scope-wide
// throw has run and cleared the throwing scope's records, a subsequent
// CancelRequested does not re-run the already-run compensations and the instance
// terminates cleanly.
func TestScopeWideThrowCompensateOnceAcrossCancel(t *testing.T) {
	at := time.Date(2026, 7, 10, 13, 0, 0, 0, time.UTC)
	def := scopeWideThenCancelDef()

	undoCount := map[string]int{}
	countUndos := func(cmds []engine.Command) {
		for _, cmd := range cmds {
			if ia, ok := cmd.(engine.InvokeAction); ok {
				undoCount[ia.Name]++
			}
		}
	}

	r3 := driveToScopeWideThrow(t, def, "scopewide-cancel-1", at)
	require.Equal(t, engine.StatusCompensating, r3.State.Status)
	countUndos(r3.Commands)

	undoB := invokeActionNamed(r3.Commands, "undoB")
	require.NotNil(t, undoB)
	r4, err := engine.Step(def, r3.State,
		engine.NewActionCompleted(at.Add(3*time.Second), undoB.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	countUndos(r4.Commands)

	undoA := invokeActionNamed(r4.Commands, "undoA")
	require.NotNil(t, undoA)
	r5, err := engine.Step(def, r4.State,
		engine.NewActionCompleted(at.Add(4*time.Second), undoA.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	countUndos(r5.Commands)
	require.Equal(t, engine.StatusRunning, r5.State.Status, "must resume at userTask")
	require.Nil(t, r5.State.RootCompensations, "records cleared on scope-wide finish")

	// Cancel after the throw — must NOT re-run undoA/undoB.
	r6, err := engine.Step(def, r5.State,
		engine.NewCancelRequested(at.Add(10*time.Second)), engine.StepOptions{})
	require.NoError(t, err)
	countUndos(r6.Commands)

	assert.Equal(t, 1, undoCount["undoA"], "undoA must run exactly once (throw only, not cancel)")
	assert.Equal(t, 1, undoCount["undoB"], "undoB must run exactly once (throw only, not cancel)")
	assert.Equal(t, engine.StatusTerminated, r6.State.Status,
		"instance must terminate after cancel")
}
