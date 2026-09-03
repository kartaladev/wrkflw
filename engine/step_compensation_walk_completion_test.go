package engine_test

import (
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var walkCompletionT0 = time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)

// walkVsSiblingDef builds the reproduction fixture: a compensable service task,
// then a parallel fork into two user-task branches, each with its own
// interrupting signal boundary. Branch A's boundary throws compensation; branch
// B's boundary just ends.
//
//	start → svcSaga(doA/undoA) → fork ⇒
//	  A: taskA → endTaskA ;  bndA(signal s1, interrupting) → rb(CompensateThrow) → endA
//	  B: taskB → endTaskB ;  bndB(signal s2, interrupting) → endB
//
// Two wiring details are load-bearing and easy to get wrong:
//
//   - It is the BOUNDARY's outgoing flow that reaches rb, not taskA's. taskA and
//     taskB each still need their own normal outgoing flow (endTaskA/endTaskB).
//   - rb MUST carry an outgoing flow. Without one,
//     compensationThrowEventStrategy.enter auto-advances and no walk starts at
//     all — the same trap documented at step_stale_commands_e2e_test.go:236.
func walkVsSiblingDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-walk-vs-sibling", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svcSaga",
				activity.WithTaskAction("doA"),
				activity.WithCompensateAction("undoA"),
			),
			gateway.NewParallel("fork"),
			activity.NewUserTask("taskA"),
			event.NewBoundary("bndA", "taskA", event.WithSignalName("s1")),
			event.NewCompensateThrow("rb"),
			event.NewEnd("endA"),
			event.NewEnd("endTaskA"),
			activity.NewUserTask("taskB"),
			event.NewBoundary("bndB", "taskB", event.WithSignalName("s2")),
			event.NewEnd("endB"),
			event.NewEnd("endTaskB"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "svcSaga"},
			{ID: "f-saga-fork", Source: "svcSaga", Target: "fork"},
			{ID: "f-fork-a", Source: "fork", Target: "taskA"},
			{ID: "f-fork-b", Source: "fork", Target: "taskB"},
			{ID: "f-a-end", Source: "taskA", Target: "endTaskA"},
			{ID: "f-bnda-rb", Source: "bndA", Target: "rb"},
			{ID: "f-rb-enda", Source: "rb", Target: "endA"},
			{ID: "f-b-end", Source: "taskB", Target: "endTaskB"},
			{ID: "f-bndb-endb", Source: "bndB", Target: "endB"},
		},
	}
}

// driveToWalkInFlight drives walkVsSiblingDef to "walk in flight": both branches
// parked on their user tasks, signal s1 delivered so bndA fired and rb started a
// compensation walk. It returns that step's result and the walk's command ID.
//
// Every require here is a setup control, not an assertion about the fix: if any
// of them stops holding, the tests below would silently stop exercising the
// scenario they name.
func driveToWalkInFlight(t *testing.T, def *model.ProcessDefinition) (engine.StepResult, string) {
	t.Helper()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(walkCompletionT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	var doACmd string
	for _, c := range r1.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "doA" {
			doACmd = ia.CommandID
		}
	}
	require.NotEmpty(t, doACmd, "setup: svcSaga must park on doA")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(walkCompletionT0.Add(time.Second), doACmd, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.State.Tokens, 2, "setup: both branches must be parked")
	require.Len(t, r2.State.Boundaries, 2, "setup: two boundary arms must be recorded")

	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewSignalReceived(walkCompletionT0.Add(2*time.Second), "s1", nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, r3.State.Status,
		"setup: signal s1 must have started the compensation walk")
	walkCmd := r3.State.Compensating.ActiveCmdID
	require.NotEmpty(t, walkCmd, "setup: the walk cursor must be live")
	return r3, walkCmd
}

// TestCompensationWalkInFlightBlocksCompletion pins that an instance whose
// compensation walk is still in flight is NOT complete, even when its last token
// is gone.
//
// startCompensationWalk consumes the throwing token before parking the walk on
// Compensating.ActiveCmdID, so once the sibling branch's own token also leaves,
// len(s.Tokens) == 0 reads true while the rollback is outstanding.
// Status.IsTerminal() is false here and cannot stop it — StatusCompensating is
// deliberately non-terminal so the walk's own ActionCompleted can be dispatched.
//
// Measured on unpatched main: after signal #2 the instance was `completed` with
// activeCmd="" and a CompleteInstance{} published for a process whose rollback
// never ran.
func TestCompensationWalkInFlightBlocksCompletion(t *testing.T) {
	t.Parallel()

	def := walkVsSiblingDef()
	r3, walkCmd := driveToWalkInFlight(t, def)

	// Signal s2 fires bndB, cancelling taskB and driving branch B to endB. That
	// consumes the last token — but the walk started by branch A is still
	// awaiting undoA.
	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewSignalReceived(walkCompletionT0.Add(3*time.Second), "s2", nil), engine.StepOptions{})
	require.NoError(t, err)

	require.Equal(t, engine.StatusCompensating, r4.State.Status,
		"an instance with a compensation walk in flight is not complete")
	require.Equal(t, walkCmd, r4.State.Compensating.ActiveCmdID,
		"the walk cursor must survive the sibling branch draining the last token")
	for _, c := range r4.Commands {
		_, isComplete := c.(engine.CompleteInstance)
		require.False(t, isComplete,
			"no CompleteInstance may be published while the rollback is outstanding")
	}
}

// TestCompensationWalkFinishCompletesInstance is the other half of
// TestCompensationWalkInFlightBlocksCompletion: deferring completion is only
// correct if the deferred completion still HAPPENS.
// Once the walk's own ActionCompleted arrives, stepCompensationFinish resumes at
// the throw's resume node, drives to the end event, and the instance completes
// there — that step emitting exactly one CompleteInstance, and the earlier
// sibling-signal step none.
//
// Measured on unpatched main: the instance was already `completed` by the time
// signal #2 returned, so this trigger hit the dispatch guard
// (`outcome=dropped`) and no CompleteInstance followed it at all.
func TestCompensationWalkFinishCompletesInstance(t *testing.T) {
	t.Parallel()

	def := walkVsSiblingDef()
	r3, walkCmd := driveToWalkInFlight(t, def)

	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewSignalReceived(walkCompletionT0.Add(3*time.Second), "s2", nil), engine.StepOptions{})
	require.NoError(t, err)

	r5, err := engine.Step(t.Context(), def, r4.State,
		engine.NewActionCompleted(walkCompletionT0.Add(4*time.Second), walkCmd, nil), engine.StepOptions{})
	require.NoError(t, err)

	require.Equal(t, engine.StatusCompleted, r5.State.Status,
		"the instance completes once the compensation walk finishes")
	require.Empty(t, r5.State.Compensating.ActiveCmdID,
		"the walk cursor must be cleared by the finish")

	// Counted PER STEP, deliberately. A total across both steps would be VACUOUS:
	// unpatched, the sibling's signal (r4) emits the one CompleteInstance and the
	// walk's finish (r5) emits none, so the total is 1 either way. Measured — this
	// exact test passed on unpatched step_nodes.go until the count was pinned to
	// the step that must carry it.
	require.Equal(t, 1, countCompleteInstance(r5.Commands),
		"the walk's finish is the step that completes the instance")
	require.Equal(t, 0, countCompleteInstance(r4.Commands),
		"the sibling's signal must not have completed it earlier")
}

// TestSiblingSignalHonouredDuringCompensationWalk is a REGRESSION PIN, not a
// red-green test: it passes with and without the completion-deferral fix, and
// that is deliberate. What it guards is the predicate that fix did NOT choose.
//
// A sibling branch progressing while an in-definition CompensateThrow walks is
// correct BPMN — StatusCompensating means two different things (whole-instance
// rollback via beginCompensation, which drains boundary and gateway arms; and a
// LOCAL walk via startCompensationWalk, which drains nothing). The obvious-looking
// fix `if s.Status != StatusRunning { return nil, nil }` conflates them: it is a
// no-op on the rollback route, where those arms are already gone, and blocks
// exactly the local-throw route where firing is legitimate. Signals are one-shot
// broadcasts, so a swallowed s2 strands taskB forever.
//
// It discriminates in TWO places:
//
//   - `!= StatusRunning` substituted at the top of fireBoundaryArm
//     (engine/step_boundaries.go). RE-MEASURED when this test was written: it goes
//     RED with `cmds=[]engine.Command(nil)`, and the rest of the engine suite —
//     this file removed — stays EXIT=0 under the same mutation. So for that
//     mutation this test is the only thing in the repo that catches it.
//   - the same wrong predicate used for a future guard in handleSignalReceived
//     instead of IsTerminal(). NOT re-measured here: that guard does not exist yet.
//     Inherited from an earlier audit, which measured this test RED under it.
//     Re-verify rather than restate when that guard lands.
//
// Deleting this test silently reopens the first, and — once that guard lands —
// very likely the second, since the refuted shape is the single most likely
// implementation slip.
func TestSiblingSignalHonouredDuringCompensationWalk(t *testing.T) {
	t.Parallel()

	def := walkVsSiblingDef()
	r3, _ := driveToWalkInFlight(t, def)

	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewSignalReceived(walkCompletionT0.Add(3*time.Second), "s2", nil), engine.StepOptions{})
	require.NoError(t, err)

	cancelled := false
	for _, c := range r4.Commands {
		if ut, ok := c.(engine.UpdateTask); ok &&
			ut.Task.NodeID == "taskB" && ut.Task.State == humantask.Cancelled {
			cancelled = true
		}
	}
	require.True(t, cancelled,
		"signal s2 must still fire bndB and cancel taskB while the walk is in flight; cmds=%#v",
		r4.Commands)
}

// standbyESPBody is a second, never-triggered event sub-process body used by the
// two event-sub-process fixtures below. Its only job is to leave an ARMED event
// sub-process in the scope so the arm-retirement effect is observable rather
// than argued.
func standbyESPBody() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "standby-esp-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("standbyStart", event.WithSignalName("standby")),
			event.NewEnd("standbyEnd"),
		},
		Flows: []flow.SequenceFlow{{ID: "sb1", Source: "standbyStart", Target: "standbyEnd"}},
	}
}

// walkingESPBody is the event sub-process body that starts a compensation walk
// and then drains its own scope:
//
//	espStart(signal "boom", NON-interrupting) → svcInner(doB/undoB) → innerFork ⇒
//	  { throwInner(CompensateThrow) → innerEnd1 ;  innerEnd2 }
//
// Three details are load-bearing:
//
//   - The throw branch is declared FIRST, so the walk starts (consuming its own
//     token) before the sibling reaches innerEnd2 — which is the step that finds
//     the scope drained and calls the event-sub-process exit.
//   - throwInner has an outgoing flow, or the strategy auto-advances and no walk
//     starts at all.
//   - The start is NON-interrupting so the host token outside survives and can be
//     retired separately; an interrupting start tears the enclosing scope's other
//     event-sub-process arms down at trigger time, leaving nothing for the
//     retirement assertion to observe.
func walkingESPBody(id string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: id, Version: 1,
		Nodes: []model.Node{
			event.NewStart("espStart", event.WithSignalName("boom"), event.WithNonInterrupting()),
			activity.NewServiceTask("svcInner",
				activity.WithTaskAction("doB"),
				activity.WithCompensateAction("undoB"),
			),
			gateway.NewParallel("innerFork"),
			event.NewCompensateThrow("throwInner"),
			event.NewEnd("innerEnd1"),
			event.NewEnd("innerEnd2"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "e1", Source: "espStart", Target: "svcInner"},
			{ID: "e2", Source: "svcInner", Target: "innerFork"},
			{ID: "e3", Source: "innerFork", Target: "throwInner"},
			{ID: "e4", Source: "throwInner", Target: "innerEnd1"},
			{ID: "e5", Source: "innerFork", Target: "innerEnd2"},
		},
	}
}

// rootESPWalkDef drives exitRootEventSubprocessScope: the walking event
// sub-process sits at the ROOT level, so its exit takes the parentScopeID == ""
// branch.
func rootESPWalkDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-root-esp-walk", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("host", activity.WithTaskAction("host-action")),
			event.NewEnd("end"),
			activity.NewSubProcess("esp", walkingESPBody("root-esp-body")),
			activity.NewSubProcess("espStandby", standbyESPBody()),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "host"},
			{ID: "f2", Source: "host", Target: "end"},
		},
	}
}

// driveESPWalkToScopeDrain runs either event-sub-process fixture through:
//
//	StartInstance                 → host parked on hostCmd
//	SignalReceived("boom")        → the event sub-process starts, svcInner parked
//	ActionCompleted(hostCmd)      → the enclosing scope's own token retires
//	ActionCompleted(doB)          → fork; the throw starts the walk; the sibling
//	                                token drains the scope and reaches the exit
//
// It returns the state just before that last step and that step's result.
func driveESPWalkToScopeDrain(t *testing.T, def *model.ProcessDefinition, instanceID, hostCmd string) (engine.InstanceState, engine.StepResult) {
	t.Helper()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: instanceID},
		engine.NewStartInstance(walkCompletionT0, nil), engine.StepOptions{})
	require.NoError(t, err)

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(walkCompletionT0.Add(time.Second), "boom", nil), engine.StepOptions{})
	require.NoError(t, err)
	var doB string
	for _, c := range r2.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "doB" {
			doB = ia.CommandID
		}
	}
	require.NotEmpty(t, doB, "setup: the event sub-process must have parked on doB")

	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewActionCompleted(walkCompletionT0.Add(2*time.Second), hostCmd, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r3.State.Tokens, 1,
		"setup: only the event sub-process's own token may remain")
	require.Len(t, r3.State.EventTriggeredSubprocesses, 2,
		"setup: both event-sub-process arms must still be armed before the walk starts")

	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewActionCompleted(walkCompletionT0.Add(3*time.Second), doB, nil), engine.StepOptions{})
	require.NoError(t, err)
	return r3.State, r4
}

// assertWalkDeferredESPCompletion holds the shared expectations of the two
// event-sub-process exits: the instance does NOT complete under a live cursor,
// the walk's compensation action reaches the runtime, and the exit is HELD
// rather than closing the scope the walk still needs — after which the walk's
// own resume drives the instance to completion.
func assertWalkDeferredESPCompletion(t *testing.T, def *model.ProcessDefinition, before engine.InstanceState, r engine.StepResult) {
	t.Helper()

	require.Equal(t, engine.StatusCompensating, r.State.Status,
		"an event-sub-process exit must not complete an instance whose walk is in flight")
	walkCmd := r.State.Compensating.ActiveCmdID
	require.NotEmpty(t, walkCmd, "the walk cursor must survive the scope exit")
	require.Equal(t, 0, countCompleteInstance(r.Commands),
		"no CompleteInstance while the rollback is outstanding")

	invoked := false
	for _, c := range r.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "undoB" {
			invoked = ia.CommandID == walkCmd
		}
	}
	require.True(t, invoked,
		"the walk's compensation action must reach the runtime, not be dropped as stale")

	// The held exit CORRECTS what this assertion previously pinned: it read
	// `require.Empty(r.State.EventTriggeredSubprocesses)` with the message
	// "ACCEPTED COST: the fallthrough retires this scope's event-sub-process
	// arms" — the exit closed the scope, fell past the completion branch, and
	// retired both arms. That premature close was the defect in this fixture's
	// clothing: MEASURED one Step further on the unpatched tree, this
	// walk's own ActionCompleted then resumed into the scope closeScope had just
	// pruned and every subsequent Step returned `workflow-engine: defForScope:
	// unknown scope "i-root-esp-s1"` / `"…-s2"` — a permanent wedge. Holding the
	// exit keeps the arms alive instead of losing them, and the "accepted cost"
	// is retired with the defect.
	require.Len(t, before.EventTriggeredSubprocesses, 2,
		"control: two arms were live immediately before the exit")
	require.Len(t, r.State.EventTriggeredSubprocesses, 2,
		"the held exit does not retire this scope's event-sub-process arms")
	require.NotEmpty(t, r.State.Scopes,
		"the scope the walk resumes into is still open")

	// The held exit is not a stall: the walk's resume places a token at the
	// throw's successor INSIDE that scope, which re-runs the exit with a clear
	// cursor and completes the instance.
	after, err := engine.Step(t.Context(), def, r.State,
		engine.NewActionCompleted(walkCompletionT0.Add(10*time.Second), walkCmd, nil), engine.StepOptions{})
	require.NoError(t, err, "the walk's completion must not wedge on a pruned scope")
	require.Equal(t, engine.StatusCompleted, after.State.Status,
		"once the walk drains, the deferred scope exit runs and the instance completes")
	require.Empty(t, after.State.Tokens)
	require.Equal(t, 1, countCompleteInstance(after.Commands))
}

// TestCompensationWalkBlocksRootEventSubprocessCompletion drives a compensation
// throw INSIDE a root-level event sub-process whose sibling drains the scope.
//
// ⚠ It no longer pins the second guard site, and the docstring that said it did
// has been corrected rather than left to rot. The scope exit is held while a
// walk names that scope as its resume target, so control returns before
// exitRootEventSubprocessScope is entered at all — measured, reverting that
// conjunct now leaves the whole engine suite at EXIT=0. Coverage for the site
// moved to TestRootEventSubprocessExitBlocksCompletionUnderRootWalk, which
// reaches it with a ROOT-scope walk the hold cannot match.
//
// What this fixture pins now is the hold on the event-sub-process route. The
// historical measurement stands as history: on an older step_nodes.go the
// instance went `completed` with a CompleteInstance published and the walk's
// undoB InvokeAction dropped in the same step; one Step further, before the hold
// existed, it wedged permanently on `defForScope: unknown scope "i-root-esp-s1"`.
func TestCompensationWalkBlocksRootEventSubprocessCompletion(t *testing.T) {
	t.Parallel()

	def := rootESPWalkDef()
	before, r := driveESPWalkToScopeDrain(t, def, "i-root-esp", "i-root-esp-c1")
	assertWalkDeferredESPCompletion(t, def, before, r)
}

// nestedESPWalkDef drives exitNestedEventSubprocessScope's root-grandparent
// completion branch. The walking event sub-process lives inside a regular
// sub-process "outer", and "outer" deliberately has NO outgoing flow in the root
// definition: resumeInParentScope then finds nothing, grandparentScopeID is "",
// and the completion check is reached.
func nestedESPWalkDef() *model.ProcessDefinition {
	outerBody := &model.ProcessDefinition{
		ID: "outer-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("oStart"),
			activity.NewServiceTask("oHost", activity.WithTaskAction("host-action")),
			event.NewEnd("oEnd"),
			activity.NewSubProcess("nesp", walkingESPBody("nested-esp-body")),
			activity.NewSubProcess("nespStandby", standbyESPBody()),
		},
		Flows: []flow.SequenceFlow{
			{ID: "o1", Source: "oStart", Target: "oHost"},
			{ID: "o2", Source: "oHost", Target: "oEnd"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p-nested-esp-walk", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("outer", outerBody),
		},
		Flows: []flow.SequenceFlow{{ID: "f1", Source: "start", Target: "outer"}},
	}
}

// TestCompensationWalkBlocksNestedEventSubprocessCompletion is the nested twin of
// the test above, and carries the same correction: since the scope-exit hold was
// added it no longer pins the third guard site. That conjunct
// (exitNestedEventSubprocessScope's grandparent-is-root branch) can now be
// reverted with the engine suite at EXIT=0, and no other fixture covers it —
// recorded rather than silently dropped. What this fixture pins now is the hold;
// measured before it existed, the fixture wedged permanently on `defForScope:
// unknown scope "i-nested-esp-s2"`.
func TestCompensationWalkBlocksNestedEventSubprocessCompletion(t *testing.T) {
	t.Parallel()

	def := nestedESPWalkDef()
	before, r := driveESPWalkToScopeDrain(t, def, "i-nested-esp", "i-nested-esp-c1")
	assertWalkDeferredESPCompletion(t, def, before, r)
}

func countCompleteInstance(cmds []engine.Command) int {
	n := 0
	for _, c := range cmds {
		if _, ok := c.(engine.CompleteInstance); ok {
			n++
		}
	}
	return n
}

// idleESPBody is an event sub-process that simply parks on a user task. It never
// starts a compensation walk of its own — it exists to OUTLIVE one started
// elsewhere, so that its scope exit reaches exitRootEventSubprocessScope while
// a walk rooted in another scope is still outstanding.
func idleESPBody() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "idle-esp-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("espStart", event.WithSignalName("boom"), event.WithNonInterrupting()),
			activity.NewUserTask("espTask"),
			event.NewEnd("espEnd"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "ie1", Source: "espStart", Target: "espTask"},
			{ID: "ie2", Source: "espTask", Target: "espEnd"},
		},
	}
}

// rootWalkOutlivedByESPDef puts the compensation throw at the ROOT scope and a
// root-level event sub-process alongside it. The walk's ResumeScope is therefore
// "" while the scope that exits is the event sub-process's own — the one
// combination the exitSubprocessScope hold deliberately does not hold,
// so exitRootEventSubprocessScope is still entered with a live cursor.
func rootWalkOutlivedByESPDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-root-walk-outlived-by-esp", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svcSaga",
				activity.WithTaskAction("doA"), activity.WithCompensateAction("undoA")),
			gateway.NewParallel("fork"),
			activity.NewUserTask("taskA"),
			event.NewBoundary("bndA", "taskA", event.WithSignalName("s1")),
			event.NewCompensateThrow("rb"),
			event.NewEnd("endThrow"),
			event.NewEnd("endTaskA"),
			activity.NewUserTask("taskB"),
			event.NewEnd("endTaskB"),
			activity.NewSubProcess("esp", idleESPBody()),
		},
		Flows: []flow.SequenceFlow{
			{ID: "g1", Source: "start", Target: "svcSaga"},
			{ID: "g2", Source: "svcSaga", Target: "fork"},
			{ID: "g3", Source: "fork", Target: "taskA"},
			{ID: "g4", Source: "fork", Target: "taskB"},
			{ID: "g5", Source: "taskA", Target: "endTaskA"},
			{ID: "g6", Source: "bndA", Target: "rb"},
			{ID: "g7", Source: "rb", Target: "endThrow"},
			{ID: "g8", Source: "taskB", Target: "endTaskB"},
		},
	}
}

// TestRootEventSubprocessExitBlocksCompletionUnderRootWalk restores the coverage
// the scope-exit hold took away from the SECOND guard site.
//
// TestCompensationWalkBlocksRootEventSubprocessCompletion used to reach
// exitRootEventSubprocessScope's completion branch with a live cursor, because
// the walk lived INSIDE the exiting event sub-process's own scope. The hold now
// covers that exit, so that fixture no longer reaches the branch at all —
// measured, reverting the conjunct at that site leaves the whole engine suite
// green. This fixture reaches it the other way: the walk is rooted at the ROOT
// scope (ResumeScope ""), which the hold never matches, while an unrelated
// root-level event sub-process drains and exits.
//
// What makes this fail without the conjunct at that site: the exit finds
// zero tokens and would publish CompleteInstance for an instance whose rollback
// is still outstanding.
func TestRootEventSubprocessExitBlocksCompletionUnderRootWalk(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	def := rootWalkOutlivedByESPDef()

	r, err := engine.Step(ctx, def, engine.InstanceState{InstanceID: "i-outlived"},
		engine.NewStartInstance(walkCompletionT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	var doA string
	for _, c := range r.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "doA" {
			doA = ia.CommandID
		}
	}
	require.NotEmpty(t, doA, "setup: svcSaga must park on doA")

	r, err = engine.Step(ctx, def, r.State,
		engine.NewActionCompleted(walkCompletionT0.Add(time.Second), doA, nil), engine.StepOptions{})
	require.NoError(t, err)
	taskB := humanTaskIDForNode(t, r.State, "taskB")

	// Open the event sub-process scope, then start the ROOT-scope walk.
	r, err = engine.Step(ctx, def, r.State,
		engine.NewSignalReceived(walkCompletionT0.Add(2*time.Second), "boom", nil), engine.StepOptions{})
	require.NoError(t, err)
	espTask := humanTaskIDForNode(t, r.State, "espTask")

	r, err = engine.Step(ctx, def, r.State,
		engine.NewSignalReceived(walkCompletionT0.Add(3*time.Second), "s1", nil), engine.StepOptions{})
	require.NoError(t, err)
	walkCmd := r.State.Compensating.ActiveCmdID
	require.NotEmpty(t, walkCmd, "setup: the root-scope walk must be in flight")
	require.Empty(t, r.State.Compensating.ResumeScope,
		"setup: the walk resumes at the ROOT scope, so the hold cannot fire")

	// Retire the remaining root token, leaving only the event sub-process running.
	r, err = engine.Step(ctx, def, r.State,
		engine.NewHumanCompleted(walkCompletionT0.Add(4*time.Second), taskB,
			engine.CompletionInput{}, authz.Actor{ID: "u1"}), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r.State.Tokens, 1, "setup: only the event sub-process token remains")

	// The event sub-process drains and exits — reaching the second guard site
	// with the cursor still live.
	r, err = engine.Step(ctx, def, r.State,
		engine.NewHumanCompleted(walkCompletionT0.Add(5*time.Second), espTask,
			engine.CompletionInput{}, authz.Actor{ID: "u1"}), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, r.State.Status,
		"the event-sub-process exit must not complete an instance whose walk is in flight")
	require.Equal(t, 0, countCompleteInstance(r.Commands),
		"no CompleteInstance while the rollback is outstanding")
	require.Equal(t, walkCmd, r.State.Compensating.ActiveCmdID,
		"the walk cursor survives the event-sub-process exit")
	require.Empty(t, r.State.Scopes,
		"the hold is NARROW: it must not hold a scope the walk does not resume into")

	// The deferred completion still happens once the walk finishes.
	r, err = engine.Step(ctx, def, r.State,
		engine.NewActionCompleted(walkCompletionT0.Add(6*time.Second), walkCmd, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, r.State.Status,
		"the walk's resume drives the instance to completion")
	require.Equal(t, 1, countCompleteInstance(r.Commands))
}

// humanTaskIDForNode returns the ID of the open human task minted for nodeID.
func humanTaskIDForNode(t *testing.T, s engine.InstanceState, nodeID string) string {
	t.Helper()
	for _, task := range s.Tasks {
		if task.NodeID == nodeID {
			return task.TaskID
		}
	}
	t.Fatalf("no human task for node %q", nodeID)
	return ""
}

// rootWalkOutlivedByESPRunningOnDef is rootWalkOutlivedByESPDef with a USER TASK
// between the throw and its end event. That single change makes the walk's
// resume leave the instance RUNNING instead of completing it immediately, which
// is what makes the root-level event-sub-process arms observable after the exit.
func rootWalkOutlivedByESPRunningOnDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-root-walk-outlived-running-on", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svcSaga",
				activity.WithTaskAction("doA"), activity.WithCompensateAction("undoA")),
			gateway.NewParallel("fork"),
			activity.NewUserTask("taskA"),
			event.NewBoundary("bndA", "taskA", event.WithSignalName("s1")),
			event.NewCompensateThrow("rb"),
			activity.NewUserTask("afterThrow"),
			event.NewEnd("endThrow"),
			event.NewEnd("endTaskA"),
			activity.NewUserTask("taskB"),
			event.NewEnd("endTaskB"),
			activity.NewSubProcess("esp", idleESPBody()),
		},
		Flows: []flow.SequenceFlow{
			{ID: "g1", Source: "start", Target: "svcSaga"},
			{ID: "g2", Source: "svcSaga", Target: "fork"},
			{ID: "g3", Source: "fork", Target: "taskA"},
			{ID: "g4", Source: "fork", Target: "taskB"},
			{ID: "g5", Source: "taskA", Target: "endTaskA"},
			{ID: "g6", Source: "bndA", Target: "rb"},
			{ID: "g7", Source: "rb", Target: "afterThrow"},
			{ID: "g8", Source: "afterThrow", Target: "endThrow"},
			{ID: "g9", Source: "taskB", Target: "endTaskB"},
		},
	}
}

// TestRootEventSubprocessExitKeepsRootArmsWhenTheWalkCanResume pins that the
// exit keeps the ROOT-scope event-sub-process arms alive; retiring them was once
// an accepted cost.
//
// exitRootEventSubprocessScope's non-completing tail used to retire every
// ROOT-scope event-sub-process arm on its way out — cleanup written when that
// tail was believed to run only as the instance finished. The cursor conjunct
// made it the DEFERRED-completion path instead, and the deferral can
// end in a resume: the instance goes back to Running with its root event
// sub-processes permanently disarmed.
//
// What makes this test fail without the change (measured on this bundle's
// commit, this fixture): arms 1 → 0 at the exit, the walk's resume left the
// instance `running` with arms=0, and a second "boom" signal opened no scope at
// all — a NON-interrupting root event sub-process, which is repeatable,
// silently stopped being triggerable.
func TestRootEventSubprocessExitKeepsRootArmsWhenTheWalkCanResume(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	def := rootWalkOutlivedByESPRunningOnDef()

	r, err := engine.Step(ctx, def, engine.InstanceState{InstanceID: "i-armskeep"},
		engine.NewStartInstance(walkCompletionT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	var doA string
	for _, c := range r.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "doA" {
			doA = ia.CommandID
		}
	}
	require.NotEmpty(t, doA, "setup: svcSaga must park on doA")

	r, err = engine.Step(ctx, def, r.State,
		engine.NewActionCompleted(walkCompletionT0.Add(time.Second), doA, nil), engine.StepOptions{})
	require.NoError(t, err)
	taskB := humanTaskIDForNode(t, r.State, "taskB")

	r, err = engine.Step(ctx, def, r.State,
		engine.NewSignalReceived(walkCompletionT0.Add(2*time.Second), "boom", nil), engine.StepOptions{})
	require.NoError(t, err)
	espTask := humanTaskIDForNode(t, r.State, "espTask")

	r, err = engine.Step(ctx, def, r.State,
		engine.NewSignalReceived(walkCompletionT0.Add(3*time.Second), "s1", nil), engine.StepOptions{})
	require.NoError(t, err)
	walkCmd := r.State.Compensating.ActiveCmdID
	require.NotEmpty(t, walkCmd, "setup: the root-scope walk must be in flight")
	require.NotEmpty(t, r.State.Compensating.ResumeNode,
		"setup: the walk must be a RESUMING throw walk")
	require.Len(t, r.State.EventTriggeredSubprocesses, 1,
		"setup: the root event sub-process arm is live before the exit")

	r, err = engine.Step(ctx, def, r.State,
		engine.NewHumanCompleted(walkCompletionT0.Add(4*time.Second), taskB,
			engine.CompletionInput{}, authz.Actor{ID: "u1"}), engine.StepOptions{})
	require.NoError(t, err)

	// The event sub-process drains and exits with the cursor still live.
	r, err = engine.Step(ctx, def, r.State,
		engine.NewHumanCompleted(walkCompletionT0.Add(5*time.Second), espTask,
			engine.CompletionInput{}, authz.Actor{ID: "u1"}), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, r.State.Status,
		"control: completion is deferred behind the live walk")
	assert.Len(t, r.State.EventTriggeredSubprocesses, 1,
		"a deferred completion must not disarm the root event sub-processes")

	// The walk resumes: the instance is running again, and must still be able to
	// trigger the event sub-process it never terminated.
	r, err = engine.Step(ctx, def, r.State,
		engine.NewActionCompleted(walkCompletionT0.Add(6*time.Second), walkCmd, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r.State.Status,
		"control: the walk's resume puts the instance back to work")
	require.Len(t, r.State.EventTriggeredSubprocesses, 1,
		"the resumed instance keeps its root event-sub-process arm")

	r, err = engine.Step(ctx, def, r.State,
		engine.NewSignalReceived(walkCompletionT0.Add(7*time.Second), "boom", nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Len(t, r.State.Scopes, 1,
		"the non-interrupting root event sub-process must still be triggerable")
	assert.NotEmpty(t, humanTaskIDForNode(t, r.State, "espTask"),
		"its body must run again")
}
