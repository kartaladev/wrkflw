package engine_test

// Detection of a stalled compensation walk.
//
// A compensation walk advances ONLY on a trigger carrying the cursor's
// ActiveCmdID. If the dispatched action never reports back, the instance holds
// no tokens, no timers and no waiters, so nothing can wake it. These tests cover
// the arm/cancel half: a TimerCompensationStall record armed at each of
// armCompensationStallTimer's call sites — beginCompensation,
// stepCompensationAdvance, startCompensationWalk and retryStalledCompensation —
// and cancelled wherever the walk moves on.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
)

// stallWindow is the detection window these tests configure. Any non-zero value
// works; a round one keeps the AfterDuration assertions readable.
const stallWindow = 30 * time.Minute

// stallTimerRecords returns only the TimerCompensationStall records in s.
func stallTimerRecords(s engine.InstanceState) []engine.TimerRecordView {
	var out []engine.TimerRecordView
	for _, tr := range engine.TimerRecords(&s) {
		if tr.Kind == engine.TimerCompensationStall {
			out = append(out, tr)
		}
	}
	return out
}

// TestBeginCompensationArmsStallTimer covers a CancelRequested walk started by
// beginCompensation arms exactly one TimerCompensationStall, carrying the
// command id of the compensation action it guards.
//
// The record must be walk-scoped, not token-scoped: beginCompensation's prologue
// cancels every token, so a Token-keyed record would name a token that no longer
// exists — and an empty key names no record, which is exactly the
// property that keeps token-keyed cleanup from sweeping it.
func TestBeginCompensationArmsStallTimer(t *testing.T) {
	state := runThreeCompensableActivities(t)
	at := time.Date(2026, 6, 21, 11, 0, 0, 0, time.UTC)

	res, err := engine.Step(t.Context(), threeCompensableDef(), state,
		engine.NewCancelRequested(at),
		engine.StepOptions{CompensationStallAfter: stallWindow})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, res.State.Status,
		"CancelRequested with records must start a compensation walk")

	// The walk's first dispatch is the LAST-completed record, step3 → "c3".
	undo := engine.InvokeAction{}
	found := false
	for _, cmd := range res.Commands {
		if ia, ok := cmd.(engine.InvokeAction); ok {
			undo, found = ia, true
		}
	}
	require.True(t, found, "the walk must dispatch a compensation InvokeAction")
	require.Equal(t, "c3", undo.Name, "reverse walk dispatches step3's compensation first")

	sched, ok := findScheduleTimerByKind(res.Commands, engine.TimerCompensationStall)
	require.True(t, ok, "beginCompensation must emit a ScheduleTimer{Kind: TimerCompensationStall}")
	assert.Equal(t, schedule.AfterDuration(stallWindow), sched.Trigger,
		"the stall timer fires once, after the configured window")
	assert.Empty(t, sched.Token, "the stall timer guards the WALK, not a token")

	recs := stallTimerRecords(res.State)
	require.Len(t, recs, 1, "exactly one stall record is armed per dispatch")
	assert.Equal(t, sched.TimerID, recs[0].TimerID)
	assert.Equal(t, undo.CommandID, recs[0].CommandID,
		"the record must carry the guarded ActiveCmdID, or a late fire cannot be told from a live one")
	assert.Empty(t, recs[0].Token,
		"an empty Token names no record, so token-keyed cleanup never sweeps it")
	assert.Equal(t, "step3", recs[0].NodeID)
}

// TestZeroWindowDisablesStallDetection pins that with CompensationStallAfter
// unset (the zero default) the walk's command stream and its timer bookkeeping
// are both byte-identical to a build without this feature.
//
// The command list is an explicit GOLDEN captured by probing this fixture — no
// test can assert equality against another git revision, so the list is written
// out and any change to it must be adjudicated rather than absorbed.
//
// ⚠ It deliberately does NOT assert s.Timers is nil here. Measured on main
// @ 5270838, this cancel path already leaves Timers as an empty NON-nil slice
// (beginCompensation's prologue runs cancelTimersByTaskID for the parked human
// task, which rebuilds the slice). That null→[] drift is PRE-EXISTING and
// unrelated to this delivery; the new writer's own no-op-when-disabled property
// is pinned by TestZeroWindowLeavesThrowWalkTimersUntouched below, on a path
// where main really does leave nil.
func TestZeroWindowDisablesStallDetection(t *testing.T) {
	state := runThreeCompensableActivities(t)
	at := time.Date(2026, 6, 21, 11, 0, 0, 0, time.UTC)

	res, err := engine.Step(t.Context(), threeCompensableDef(), state,
		engine.NewCancelRequested(at), engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, res.Commands, 2, "golden: exactly [UpdateTask, InvokeAction{c3}]")
	upd, ok := res.Commands[0].(engine.UpdateTask)
	require.True(t, ok, "golden[0] is UpdateTask")
	assert.Equal(t, "userTask", upd.Task.NodeID)
	inv, ok := res.Commands[1].(engine.InvokeAction)
	require.True(t, ok, "golden[1] is InvokeAction")
	assert.Equal(t, "c3", inv.Name)

	assert.Empty(t, stallTimerRecords(res.State), "no stall record when detection is off")
}

// TestZeroWindowLeavesThrowWalkTimersUntouched pins that the stall machinery is
// a true no-op when disabled, on a path where that is observable.
//
// Measured on main @ 5270838, a scope-wide throw's first dispatch emits exactly
// ONE command and leaves s.Timers nil. startCompensationWalk is a site this
// delivery adds a new s.Timers writer to, so if the cancel-then-arm helper
// rebuilt the slice unconditionally, every throw walk would persist `timers: []`
// where it used to persist null — with detection switched OFF.
//
// Falsifiable: delete the `if cmds == nil { return nil }` early return in
// cancelCompensationWalkTimers and the nil assertion reddens.
func TestZeroWindowLeavesThrowWalkTimersUntouched(t *testing.T) {
	at := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	def := rootSagaWithScopeWideThrow()

	res := driveToScopeWideThrowWithOptions(t, def, "stall-off-1", at, engine.StepOptions{})

	require.Len(t, res.Commands, 1, "golden from main: the throw dispatch emits one InvokeAction")
	assert.True(t, engine.TimersAreNil(&res.State),
		"a disabled feature must not rewrite the persisted timers shape from null to []")
}

// TestScopeWideThrowFirstDispatchArmsStallTimer covers the gap the design audit
// found.
//
// The dispatch sites are not just beginCompensation and stepCompensationAdvance:
// startCompensationWalk is the compensation THROW walk's FIRST dispatch and
// lives in a different file (retryStalledCompensation, the operator escape, is
// the fourth). Leaving startCompensationWalk unarmed leaves a throw
// walk undetected until its second record — and a single-record throw walk
// undetected entirely. It is also the site at which the measured
// deferred-cancel deadlock arises.
//
// Fails before the arm exists: driveToScopeWideThrow dispatches undoB through
// startCompensationWalk and no ScheduleTimer of this kind is emitted.
func TestScopeWideThrowFirstDispatchArmsStallTimer(t *testing.T) {
	at := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	def := rootSagaWithScopeWideThrow()

	res := driveToScopeWideThrowWithOptions(t, def, "stall-throw-1", at,
		engine.StepOptions{CompensationStallAfter: stallWindow})

	require.Equal(t, engine.StatusCompensating, res.State.Status)
	undoB := invokeActionNamed(res.Commands, "undoB")
	require.NotNil(t, undoB, "the throw walk's first dispatch is undoB")

	sched, ok := findScheduleTimerByKind(res.Commands, engine.TimerCompensationStall)
	require.True(t, ok,
		"startCompensationWalk must arm a stall timer: it is the throw walk's FIRST dispatch, "+
			"so without it a single-record throw walk gets no detection at all")

	recs := stallTimerRecords(res.State)
	require.Len(t, recs, 1)
	assert.Equal(t, sched.TimerID, recs[0].TimerID)
	assert.Equal(t, undoB.CommandID, recs[0].CommandID)
	assert.Equal(t, "svcB", recs[0].NodeID)
}

// TestCompensationAdvanceRearmsStallTimer pins that each advance cancels the
// previous stall timer and arms a fresh one carrying the NEW ActiveCmdID.
//
// The re-arm must be cancel-THEN-arm. Two live records would let the stale one
// fire against a command that already completed, and its CommandID would no
// longer match the cursor — so the fire handler would drop it, silently
// consuming the detection the operator configured.
func TestCompensationAdvanceRearmsStallTimer(t *testing.T) {
	state := runThreeCompensableActivities(t)
	at := time.Date(2026, 6, 21, 11, 0, 0, 0, time.UTC)
	def := threeCompensableDef()
	opt := engine.StepOptions{CompensationStallAfter: stallWindow}

	r1, err := engine.Step(t.Context(), def, state, engine.NewCancelRequested(at), opt)
	require.NoError(t, err)
	undo3 := invokeActionNamed(r1.Commands, "c3")
	require.NotNil(t, undo3)
	first := stallTimerRecords(r1.State)
	require.Len(t, first, 1)

	// Complete c3 → the walk advances to c2 under a new command id.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(at.Add(time.Second), undo3.CommandID, nil), opt)
	require.NoError(t, err)
	undo2 := invokeActionNamed(r2.Commands, "c2")
	require.NotNil(t, undo2, "the walk must advance to step2's compensation")

	assert.Contains(t, r2.Commands, engine.CancelTimer{TimerID: first[0].TimerID},
		"the advance must cancel the stall timer guarding the completed command")

	second := stallTimerRecords(r2.State)
	require.Len(t, second, 1,
		"cancel-then-arm: exactly one stall record may be live at a time")
	assert.NotEqual(t, first[0].TimerID, second[0].TimerID, "a fresh timer is armed")
	assert.Equal(t, undo2.CommandID, second[0].CommandID,
		"the re-armed record must carry the NEW ActiveCmdID")
	assert.Equal(t, "step2", second[0].NodeID)
}

// TestResumeFinishCancelsStallTimer pins that a RESUME finish cancels the
// outstanding stall timer.
//
// Only TERMINAL finishes reach cancelAllTimers via endInstance. The four resume
// finishes — throw-targeted, throw-scope-wide, partial rollback and full reverse
// — do not touch s.Timers at all, so without an explicit cancel in
// stepCompensationFinish the stall record leaks onto a Running instance and its
// scheduler timer survives with nothing left to guard.
//
// This is a scope-wide throw, which resumes at afterThrow.
func TestResumeFinishCancelsStallTimer(t *testing.T) {
	at := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	def := rootSagaWithScopeWideThrow()
	opt := engine.StepOptions{CompensationStallAfter: stallWindow}

	r3 := driveToScopeWideThrowWithOptions(t, def, "stall-resume-1", at, opt)
	undoB := invokeActionNamed(r3.Commands, "undoB")
	require.NotNil(t, undoB)

	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewActionCompleted(at.Add(3*time.Second), undoB.CommandID, nil), opt)
	require.NoError(t, err)
	undoA := invokeActionNamed(r4.Commands, "undoA")
	require.NotNil(t, undoA, "the walk advances to undoA")
	live := stallTimerRecords(r4.State)
	require.Len(t, live, 1, "a stall timer guards undoA")

	// Complete undoA → the walk finishes and RESUMES at afterThrow.
	r5, err := engine.Step(t.Context(), def, r4.State,
		engine.NewActionCompleted(at.Add(4*time.Second), undoA.CommandID, nil), opt)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r5.State.Status,
		"a scope-wide throw resumes rather than terminating")

	assert.Contains(t, r5.Commands, engine.CancelTimer{TimerID: live[0].TimerID},
		"a resume finish must cancel the stall timer it leaves behind")
	assert.Empty(t, stallTimerRecords(r5.State),
		"no stall record may survive onto a Running instance")
}

// TestResumeFinishLeavesTimersNilNotEmpty closes the other half of the
// persisted-shape promise (found by /code-review).
//
// cancelCompensationWalkTimers already early-returns when there is nothing to
// cancel, precisely so a nil Timers is not rewritten to []. But when it DOES
// cancel and the stall record was the only one, the rebuild yields a non-nil
// empty slice — so every resume finish of a walk that armed a stall guard
// persisted `timers: []` where it previously persisted null.
//
// Measured before the fix: status=running timersNil=false len=0.
func TestResumeFinishLeavesTimersNilNotEmpty(t *testing.T) {
	at := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	def := rootSagaWithScopeWideThrow()
	opt := engine.StepOptions{CompensationStallAfter: stallWindow}

	res := driveToScopeWideThrowWithOptions(t, def, "stall-shape", at, opt)
	for i, name := range []string{"undoB", "undoA"} {
		undo := invokeActionNamed(res.Commands, name)
		require.NotNil(t, undo, "control: walk dispatches %s", name)
		var err error
		res, err = engine.Step(t.Context(), def, res.State,
			engine.NewActionCompleted(at.Add(time.Duration(i+3)*time.Second), undo.CommandID, nil), opt)
		require.NoError(t, err)
	}

	require.Equal(t, engine.StatusRunning, res.State.Status, "control: the throw walk resumed")
	assert.Empty(t, stallTimerRecords(res.State))
	assert.True(t, engine.TimersAreNil(&res.State),
		"cancelling the last stall timer must leave Timers nil, not an empty slice: "+
			"s.Timers is marshalled into the snapshot, so [] vs null is a stored-shape change")
}

// TestTerminalFinishCancelsStallTimerExactlyOnce covers the OTHER side of the
// finish-site cancel: a terminate finish reaches cancelAllTimers through
// endInstance, so the stall timer has two would-be cancellers.
//
// ⚠ Honest scope: this pins exactly-once, but ORDERING is not what secures it.
// Measured by moving stepCompensationFinish's sweep below applyFinish — the
// count stayed at 1, because whichever sweep runs first removes the record and
// the other finds nothing. The test is a regression pin against BOTH paths
// emitting, not evidence for the placement.
func TestTerminalFinishCancelsStallTimerExactlyOnce(t *testing.T) {
	state := runThreeCompensableActivities(t)
	at := time.Date(2026, 6, 21, 11, 0, 0, 0, time.UTC)
	def := threeCompensableDef()
	opt := engine.StepOptions{CompensationStallAfter: stallWindow}

	res, err := engine.Step(t.Context(), def, state, engine.NewCancelRequested(at), opt)
	require.NoError(t, err)

	// Walk all three records to the terminal finish, tracking the live stall timer.
	var lastTimerID string
	for i, name := range []string{"c3", "c2", "c1"} {
		undo := invokeActionNamed(res.Commands, name)
		require.NotNil(t, undo, "walk must dispatch %s", name)
		recs := stallTimerRecords(res.State)
		require.Len(t, recs, 1, "one stall timer guards %s", name)
		lastTimerID = recs[0].TimerID

		res, err = engine.Step(t.Context(), def, res.State,
			engine.NewActionCompleted(at.Add(time.Duration(i+1)*time.Second), undo.CommandID, nil), opt)
		require.NoError(t, err)
	}

	require.Equal(t, engine.StatusTerminated, res.State.Status,
		"a full rollback started by CancelRequested terminates")

	n := 0
	for _, cmd := range res.Commands {
		if ct, ok := cmd.(engine.CancelTimer); ok && ct.TimerID == lastTimerID {
			n++
		}
	}
	assert.Equal(t, 1, n,
		"the terminate finish must cancel the stall timer exactly once, not once here "+
			"and again from endInstance's cancelAllTimers")
	assert.Empty(t, stallTimerRecords(res.State))
}

// TestCompensationCursorStampsWalkStartTime covers the `compensating_since`
// stamp.
//
// An operator hunting wedged instances lists status=compensating and needs to
// tell a walk that started 20 seconds ago from one that has been stuck for a
// day. The stamp is taken at WALK START and must survive every advance — a
// timestamp reset on each dispatch would make a slow-but-healthy walk look
// permanently fresh.
func TestCompensationCursorStampsWalkStartTime(t *testing.T) {
	state := runThreeCompensableActivities(t)
	def := threeCompensableDef()
	opt := engine.StepOptions{CompensationStallAfter: stallWindow}
	startedAt := time.Date(2026, 6, 21, 11, 0, 0, 0, time.UTC)

	r1, err := engine.Step(t.Context(), def, state, engine.NewCancelRequested(startedAt), opt)
	require.NoError(t, err)
	assert.Equal(t, startedAt, engine.CompensatingSince(&r1.State),
		"the cursor is stamped when the walk starts")

	undo3 := invokeActionNamed(r1.Commands, "c3")
	require.NotNil(t, undo3)
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(startedAt.Add(time.Hour), undo3.CommandID, nil), opt)
	require.NoError(t, err)

	assert.Equal(t, startedAt, engine.CompensatingSince(&r2.State),
		"an advance must NOT restamp it, or a long-stuck walk looks permanently fresh")
}

// TestHasArmedTimersExcludesStallTimers covers the engine-side predicate
// processtest needs.
//
// timerRecord.Kind is unexported, so a consumer package physically cannot tell a
// compensation-stall deadline from an ordinary armed timer by reading
// state.Timers. The engine has to answer that question, or processtest.AutoTimers()
// fires stall timers by itself inside consumers' harnesses.
func TestHasArmedTimersExcludesStallTimers(t *testing.T) {
	state := runThreeCompensableActivities(t)
	def := threeCompensableDef()
	at := time.Date(2026, 6, 21, 11, 0, 0, 0, time.UTC)

	res, err := engine.Step(t.Context(), def, state, engine.NewCancelRequested(at),
		engine.StepOptions{CompensationStallAfter: stallWindow})
	require.NoError(t, err)
	require.Len(t, stallTimerRecords(res.State), 1, "control: a stall timer IS armed")

	assert.False(t, res.State.HasArmedTimers(),
		"a stall timer is a detection deadline, not work a harness may fire")
}
