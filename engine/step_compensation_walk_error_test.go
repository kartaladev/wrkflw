package engine_test

import (
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/stretchr/testify/require"
)

var walkErrorT0 = time.Date(2026, 8, 9, 9, 0, 0, 0, time.UTC)

// walkVsUncaughtErrorDef builds the uncaught-error-mid-walk reproduction: a
// compensable service task, then a parallel fork into THREE user-task branches.
//
//	start → svcSaga(doA/undoA) → fork ⇒
//	  A: taskA → endTaskA ;  bndA(signal s1, interrupting) → rb(CompensateThrow) → endA
//	  B: taskB → endTaskB ;  bndB(signal s2, interrupting) → errEnd(EndError "boom")
//	  C: taskC → endTaskC                                  (no boundary; stays parked)
//
// Branch A's signal starts an in-definition compensation walk. Branch B's signal
// then routes to an error end event whose code no boundary catches, so the
// uncaught error reaches handleUnhandledError WHILE that walk is in flight.
//
// Branch C is not decoration: it is the only thing that can observe the
// cancellation half of the decision. A stamp-only shape (clear the resume target
// and stamp the outcome, but skip the cancellation) was measured during this
// bundle's audit to leave branch C live and completable by a human on an instance
// that is already doomed. With only branches A and B there is no surviving token
// and that shape is indistinguishable from the decided one.
//
// The same two wiring traps as walkVsSiblingDef apply: it is the BOUNDARY's
// outgoing flow that reaches rb (taskA still needs its own normal outgoing flow),
// and rb MUST carry an outgoing flow or compensationThrowEventStrategy.enter
// auto-advances and no walk starts at all.
func walkVsUncaughtErrorDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-walk-vs-uncaught-error", Version: 1,
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
			event.NewEnd("errEnd", event.WithErrorCode("boom")),
			event.NewEnd("endTaskB"),
			activity.NewUserTask("taskC"),
			event.NewEnd("endTaskC"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "svcSaga"},
			{ID: "f-saga-fork", Source: "svcSaga", Target: "fork"},
			{ID: "f-fork-a", Source: "fork", Target: "taskA"},
			{ID: "f-fork-b", Source: "fork", Target: "taskB"},
			{ID: "f-fork-c", Source: "fork", Target: "taskC"},
			{ID: "f-a-end", Source: "taskA", Target: "endTaskA"},
			{ID: "f-bnda-rb", Source: "bndA", Target: "rb"},
			{ID: "f-rb-enda", Source: "rb", Target: "endA"},
			{ID: "f-b-end", Source: "taskB", Target: "endTaskB"},
			{ID: "f-bndb-err", Source: "bndB", Target: "errEnd"},
			{ID: "f-c-end", Source: "taskC", Target: "endTaskC"},
		},
	}
}

// driveWalkThenUncaughtError drives walkVsUncaughtErrorDef through:
//
//	StartInstance             → svcSaga parks on doA
//	ActionCompleted(doA)      → fork; taskA, taskB, taskC all parked
//	SignalReceived("s1")      → bndA fires; rb starts the compensation walk (undoA)
//	SignalReceived("s2")      → bndB fires; errEnd throws "boom", uncaught, MID-WALK
//
// It returns the walk-start step, the uncaught-error step, and the walk's command
// ID as it stood before the error.
//
// Every require here is a setup control, not an assertion about the fix.
func driveWalkThenUncaughtError(t *testing.T, def *model.ProcessDefinition) (engine.StepResult, engine.StepResult, string) {
	t.Helper()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-walk-err"},
		engine.NewStartInstance(walkErrorT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	var doACmd string
	for _, c := range r1.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "doA" {
			doACmd = ia.CommandID
		}
	}
	require.NotEmpty(t, doACmd, "setup: svcSaga must park on doA")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(walkErrorT0.Add(time.Second), doACmd, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.State.Tokens, 3, "setup: all three branches must be parked")
	require.Len(t, r2.State.Boundaries, 2, "setup: bndA and bndB must both be armed")

	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewSignalReceived(walkErrorT0.Add(2*time.Second), "s1", nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, r3.State.Status,
		"setup: signal s1 must have started the compensation walk")
	walkCmd := r3.State.Compensating.ActiveCmdID
	require.NotEmpty(t, walkCmd, "setup: the walk cursor must be live")
	require.NotEmpty(t, r3.State.Compensating.ResumeNode,
		"setup: a throw walk must carry a resume target — that is what the fix has to clear")
	require.Len(t, r3.State.Tokens, 2, "setup: taskB and taskC must still be parked")

	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewSignalReceived(walkErrorT0.Add(3*time.Second), "s2", nil), engine.StepOptions{})
	require.NoError(t, err)
	return r3, r4, walkCmd
}

// TestUncaughtErrorDoesNotRestartInFlightCompensationWalk pins the step-level
// half: an unhandled error arriving while a compensation walk is in
// flight does not start a second walk, and does not touch the live cursor at
// all. The walk in flight IS the rollback; the error is recorded as the pending
// terminal outcome the walk's finish must apply.
//
// Measured on unpatched engine/step_errors.go, same fixture:
//
//	AFTER s1  activeCmd="i-walk-err-c2" resumeNode="endA"  InvokeAction{undoA}
//	AFTER s2  activeCmd="i-walk-err-c3" resumeNode="endA"  InvokeAction{undoA}   ← second
//
// so the cursor assertion and the per-step undoA count both go RED there.
//
// The DEFERRAL assertions below replace the cursor-conversion assertions this
// test originally carried (stamping FinalStatus/FinalErr and
// clearing ResumeNode/ResumeScope in place). That shape converted the live walk
// into this error's rollback and so inherited its record source, stranding —
// and, for a targeted throw, erasing — every record outside it. See
// step_compensation_walk_error_scope_test.go, which measures all three cases.
func TestUncaughtErrorDoesNotRestartInFlightCompensationWalk(t *testing.T) {
	t.Parallel()

	def := walkVsUncaughtErrorDef()
	r3, r4, walkCmd := driveWalkThenUncaughtError(t, def)

	require.Equal(t, engine.StatusCompensating, r4.State.Status,
		"the in-flight walk keeps running; the error only decides how it ends")
	require.Equal(t, walkCmd, r4.State.Compensating.ActiveCmdID,
		"the uncaught error must not install a fresh cursor over the live walk")

	// Counted PER STEP. A total across both steps would still discriminate here
	// (2 unpatched vs 1 patched), but pinning WHICH step carries the dispatch is
	// what makes the assertion survive a future fixture change that moves the
	// walk's start.
	require.Equal(t, 1, countInvokeActionNamed(r3.Commands, "undoA"),
		"control: the walk's start is the step that dispatches undoA")
	require.Equal(t, 0, countInvokeActionNamed(r4.Commands, "undoA"),
		"the uncaught error must not dispatch undoA a second time")

	// The cursor is left ALONE — every field of it. The resume target in
	// particular survives, because the walk still has to finish through its own
	// throw-resume plan; it is applyFinish that preempts the resume once it sees
	// the pending outcome. r3's value is used rather than the literal "endA" so
	// the assertion follows the fixture if the throw's successor is ever renamed.
	require.Equal(t, r3.State.Compensating.ResumeNode, r4.State.Compensating.ResumeNode,
		"the live walk's resume target must not be rewritten by the error")

	// Untouched too. These two pin the ABSENCE of the old conversion shape: it
	// stamped StatusFailed and "boom" here, which is exactly what made the walk's
	// finish inherit this walk's narrow record source.
	require.Zero(t, r4.State.Compensating.FinalStatus,
		"the error's outcome must not be stamped on the live walk's cursor")
	require.Empty(t, r4.State.Compensating.FinalErr,
		"the error's code must not be stamped on the live walk's cursor")

	// The deferral itself: the outcome rides on the instance, not the cursor, and
	// is consumed by applyFinish when this walk drains (the PendingCancel
	// protocol, generalized to carry a terminal outcome).
	require.True(t, r4.State.PendingCancel,
		"the error must be deferred behind the live walk")
	require.Equal(t, engine.StatusFailed, r4.State.PendingFinalStatus,
		"the deferred outcome must be a failure, not the default cancel")
	require.Equal(t, "boom", r4.State.PendingFinalErr,
		"the uncaught error code must ride on the deferred outcome")

	// The cancellation half. Without it branch C survives the error: its token
	// stays live and its human task stays open on a doomed instance.
	require.Empty(t, r4.State.Tokens,
		"every remaining branch must be cancelled at the moment of the error")
	cancelledC := false
	for _, c := range r4.Commands {
		if ut, ok := c.(engine.UpdateTask); ok &&
			ut.Task.NodeID == "taskC" && ut.Task.State == humantask.Cancelled {
			cancelledC = true
		}
	}
	require.True(t, cancelledC,
		"branch C's human task must be cancelled, not left completable; cmds=%#v", r4.Commands)
}

// TestUncaughtErrorMidWalkFailsInstanceOnWalkFinish is the other half of the
// pair, and the one that matters most: driven to its end, the instance must
// report the failure rather than success.
//
// Measured on unpatched engine/step_errors.go: completing the (clobbered) cursor
// finished the walk through its inherited ResumeNode, so the instance ended
// `completed` with CompleteInstance{} and the error code "boom" reached nobody.
func TestUncaughtErrorMidWalkFailsInstanceOnWalkFinish(t *testing.T) {
	t.Parallel()

	def := walkVsUncaughtErrorDef()
	_, r4, walkCmd := driveWalkThenUncaughtError(t, def)

	r5, err := engine.Step(t.Context(), def, r4.State,
		engine.NewActionCompleted(walkErrorT0.Add(4*time.Second), walkCmd, nil), engine.StepOptions{})
	require.NoError(t, err)

	require.Equal(t, engine.StatusFailed, r5.State.Status,
		"a rollback ended by an uncaught error must leave the instance failed")
	require.Empty(t, r5.State.Tokens, "no token may survive the failure")

	var failures []engine.FailInstance
	for _, c := range r5.Commands {
		switch v := c.(type) {
		case engine.FailInstance:
			failures = append(failures, v)
		case engine.CompleteInstance:
			require.Fail(t, "the instance must not be reported complete",
				"uncaught error swallowed; cmds=%#v", r5.Commands)
		}
	}
	require.Len(t, failures, 1, "exactly one FailInstance; cmds=%#v", r5.Commands)
	require.Equal(t, "boom", failures[0].Err,
		"the uncaught error code must reach the terminal command")
}

func countInvokeActionNamed(cmds []engine.Command, name string) int {
	n := 0
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == name {
			n++
		}
	}
	return n
}

// TestCancelAfterDeferredErrorTerminatesAsCancelled pins that a CancelRequested
// arriving AFTER an unhandled error was already deferred behind the same
// in-flight walk decides the outcome itself, rather than silently inheriting the
// error's.
//
// Both deferrals write the same PendingCancel flag. handleUnhandledError stamps
// PendingFinalStatus/PendingFinalErr; handleCancelRequested must stamp its own
// outcome too, or the zero-value default it relies on is simply not reached and
// applyFinish replays the earlier error.
//
// What makes it fail without the fix (measured on this bundle's commit, same
// fixture): the finish produced status=failed with FailInstance{Err:"boom"} —
// last writer was the CANCEL, but the outcome applied was the ERROR's.
func TestCancelAfterDeferredErrorTerminatesAsCancelled(t *testing.T) {
	t.Parallel()

	def := walkVsUncaughtErrorDef()
	_, r4, walkCmd := driveWalkThenUncaughtError(t, def)
	require.Equal(t, engine.StatusFailed, r4.State.PendingFinalStatus,
		"control: the uncaught error is deferred behind the live walk")

	r5, err := engine.Step(t.Context(), def, r4.State,
		engine.NewCancelRequested(walkErrorT0.Add(4*time.Second)), engine.StepOptions{})
	require.NoError(t, err)
	require.True(t, r5.State.PendingCancel,
		"control: the cancel is deferred behind the same live walk")
	require.Equal(t, walkCmd, r5.State.Compensating.ActiveCmdID,
		"control: the cancel must not disturb the live walk")

	require.Equal(t, engine.StatusTerminated, r5.State.PendingFinalStatus,
		"the cancel must own the deferred outcome it will apply")
	require.Equal(t, "cancelled", r5.State.PendingFinalErr,
		"the cancel must overwrite the error code it supersedes")

	r6, err := engine.Step(t.Context(), def, r5.State,
		engine.NewActionCompleted(walkErrorT0.Add(5*time.Second), walkCmd, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusTerminated, r6.State.Status,
		"a cancelled instance must not report itself failed by a superseded error")
	var failures []engine.FailInstance
	for _, c := range r6.Commands {
		if fi, ok := c.(engine.FailInstance); ok {
			failures = append(failures, fi)
		}
	}
	require.Len(t, failures, 1, "exactly one terminal command; cmds=%#v", r6.Commands)
	require.Equal(t, "cancelled", failures[0].Err,
		"the terminal command must carry the cancel outcome, not the superseded error code")
}
