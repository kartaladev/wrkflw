package engine_test

// ADR-0175 — the three operator escapes from a stalled compensation walk.
//
// ⚠ Every skip-vs-abandon case needs a fixture with at least TWO compensable
// activities. A one-record walk finishes on its first advance, so skip and
// abandon produce the same observable outcome and the test cannot discriminate
// them. threeCompensableDef has three; rootSagaWithScopeWideThrow has two.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
)

// recordNodeIDs names the root compensation records still present, in order.
func recordNodeIDs(s engine.InstanceState) []string { return engine.RecordNodeIDs(&s) }

// TestRetryRedispatchesUnderAFreshCommandID is T6.
//
// Retry is the verb for a compensation that never actually ran. It re-dispatches
// the SAME record — the cursor does not advance — under a new command id, and
// re-arms the stall guard so a second silence is caught too.
//
// It must NOT re-run consumeDispatchedRecord: ownership of the record
// transferred at the ORIGINAL dispatch (ADR-0173), so consuming again would
// corrupt the walk's teardown window.
func TestRetryRedispatchesUnderAFreshCommandID(t *testing.T) {
	state, cmdID, timerID := startedStallWalk(t)
	def := threeCompensableDef()
	opt := engine.StepOptions{CompensationStallAfter: stallWindow}
	at := time.Date(2026, 6, 21, 11, 40, 0, 0, time.UTC)

	fired, err := engine.Step(t.Context(), def, state, engine.NewTimerFired(at, timerID), opt)
	require.NoError(t, err)
	require.Len(t, fired.State.Incidents, 1)
	incID := fired.State.Incidents[0].ID

	res, err := engine.Step(t.Context(), def, fired.State,
		engine.NewRetryStalledCompensation(at.Add(time.Minute), cmdID, incID), opt)
	require.NoError(t, err)

	redispatched := invokeActionNamed(res.Commands, "c3")
	require.NotNil(t, redispatched, "retry re-dispatches the SAME record")
	assert.NotEqual(t, cmdID, redispatched.CommandID, "under a fresh command id")

	recs := stallTimerRecords(res.State)
	require.Len(t, recs, 1, "the stall guard is re-armed, exactly once")
	assert.Equal(t, redispatched.CommandID, recs[0].CommandID,
		"and it guards the NEW command, not the abandoned one")

	assert.Empty(t, res.State.Incidents, "the stall incident is retired by the retry")
}

// TestSkipAdvancesTheWalk is T7. Skip takes the same path a returned
// ActionFailed already takes (ADR-0034 Decision 4's best-effort skip).
func TestSkipAdvancesTheWalk(t *testing.T) {
	state, cmdID, _ := startedStallWalk(t)
	def := threeCompensableDef()
	opt := engine.StepOptions{CompensationStallAfter: stallWindow}
	at := time.Date(2026, 6, 21, 11, 40, 0, 0, time.UTC)

	res, err := engine.Step(t.Context(), def, state,
		engine.NewSkipStalledCompensation(at, cmdID, ""), opt)
	require.NoError(t, err)

	assert.Nil(t, invokeActionNamed(res.Commands, "c3"), "the stalled record is given up on")
	assert.NotNil(t, invokeActionNamed(res.Commands, "c2"), "and the walk moves to the next")
	assert.Equal(t, engine.StatusCompensating, res.State.Status)
}

// TestAbandonTerminatesAndRetainsOnlyUndispatchedRecords is T8 — and the C2
// measurement the audit corrected.
//
// Abandon retains [0 .. NextIndex-1] and DROPS the record at NextIndex.
// Retaining the whole list was measured re-dispatching [undoB undoA] with undoB
// already run: on a beginCompensation walk consumeDispatchedRecord early-returns
// (the cursor is unpinned), so RootCompensations still holds every record, run
// or not. Retaining all of them is therefore not "keeping the un-run records".
//
// The dropped record is the accepted cost: its action may still be in flight at
// a worker. retry is the verb for the case where it genuinely never ran.
func TestAbandonTerminatesAndRetainsOnlyUndispatchedRecords(t *testing.T) {
	state, cmdID, _ := startedStallWalk(t)
	def := threeCompensableDef()
	opt := engine.StepOptions{CompensationStallAfter: stallWindow}
	at := time.Date(2026, 6, 21, 11, 40, 0, 0, time.UTC)

	// The walk dispatched c3 (step3, index 2) and stalled there.
	require.Equal(t, []string{"step1", "step2", "step3"}, recordNodeIDs(state))

	res, err := engine.Step(t.Context(), def, state,
		engine.NewAbandonCompensationWalk(at, cmdID, ""), opt)
	require.NoError(t, err)

	assert.Equal(t, engine.StatusTerminated, res.State.Status, "abandon ends the instance")
	assert.Equal(t, []string{"step1", "step2"}, recordNodeIDs(res.State),
		"exactly the records the walk never dispatched are retained; the stalled one is dropped")
	assert.Empty(t, stallTimerRecords(res.State), "no stall timer survives")

	// ADR-0164 Decision 2 still admits a later FULL rollback, which is what makes
	// retention worth anything. It must run step2 then step1 — and NOT c3.
	later, err := engine.Step(t.Context(), def, res.State,
		engine.NewCompensateRequested(at.Add(time.Hour), ""), opt)
	require.NoError(t, err)
	assert.NotNil(t, invokeActionNamed(later.Commands, "c2"),
		"the retained records stay compensable by a later admin rollback")
	assert.Nil(t, invokeActionNamed(later.Commands, "c3"),
		"the already-dispatched compensation must NOT run a second time (the C2 double-run)")
}

// TestAbandonRetiresTheStallIncident is the regression test the first pass
// MISSED, and its absence is why a mutation was misread.
//
// Deleting abandon's own retireCompensationStallIncidents call leaves the whole
// engine suite green — not because endInstance covers it, but because nothing
// asserted it. stepCompensationFinish clears s.Compensating BEFORE delegating to
// applyFinish, so endInstance's remainder sweep runs with ActiveCmdID == "" and
// early-returns. The call on the abandon path is LOAD-BEARING.
//
// Without it an abandoned walk terminates carrying a stale "compensation action
// stalled" record as Incidents[0], which runtime/outbox.go's terminalEventErr
// and runtime/processdriver_action.go's terminalErr publish unconditionally as
// the instance's cause of death.
func TestAbandonRetiresTheStallIncident(t *testing.T) {
	state, cmdID, timerID := startedStallWalk(t)
	def := threeCompensableDef()
	opt := engine.StepOptions{CompensationStallAfter: stallWindow}
	at := time.Date(2026, 6, 21, 11, 40, 0, 0, time.UTC)

	fired, err := engine.Step(t.Context(), def, state, engine.NewTimerFired(at, timerID), opt)
	require.NoError(t, err)
	require.Len(t, fired.State.Incidents, 1, "precondition: the stall is visible")

	res, err := engine.Step(t.Context(), def, fired.State,
		engine.NewAbandonCompensationWalk(at.Add(time.Minute), cmdID, ""), opt)
	require.NoError(t, err)

	require.Equal(t, engine.StatusTerminated, res.State.Status)
	assert.Empty(t, res.State.Incidents,
		"abandon must retire its own stall incident: the cursor is already cleared by the "+
			"time endInstance sweeps, so nothing else will")
}

// TestAbandonIsRefusedOnAResumingWalk is T8c — the C3 measurement.
//
// stepCompensationFinish picks its plan from walkMode(), so a throw walk takes a
// RESUME plan and a "terminate plan only" override never applies. Measured, an
// abandon on a targeted throw destroyed the un-run stalled record
// ({sub=[undoIA,undoIB]} → {sub=[undoIA]}) and left the instance RUNNING; on a
// scope-wide throw it cleared the whole drained prefix (root=[]).
//
// So abandon is accepted only on walkAdmin. No escape is lost: skip already
// drains a resuming walk to its natural resume.
func TestAbandonIsRefusedOnAResumingWalk(t *testing.T) {
	at := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	def := rootSagaWithScopeWideThrow()
	opt := engine.StepOptions{CompensationStallAfter: stallWindow}

	res := driveToScopeWideThrowWithOptions(t, def, "stall-abandon-refused", at, opt)
	undoB := invokeActionNamed(res.Commands, "undoB")
	require.NotNil(t, undoB)
	before := res.State

	_, err := engine.Step(t.Context(), def, before,
		engine.NewAbandonCompensationWalk(at.Add(time.Hour), undoB.CommandID, ""), opt)

	require.Error(t, err, "abandon must be refused on a walk that finishes by RESUMING")
	assert.ErrorIs(t, err, engine.ErrCompensationWalkResumes)
	assert.Contains(t, err.Error(), "skip", "the error must name the verb that does work here")

	// And it must be a clean refusal: nothing touched.
	after, err2 := engine.Step(t.Context(), def, before,
		engine.NewSkipStalledCompensation(at.Add(time.Hour), undoB.CommandID, ""), opt)
	require.NoError(t, err2, "control: skip IS accepted on the same walk")
	assert.NotNil(t, invokeActionNamed(after.Commands, "undoA"),
		"control: skip drains the resuming walk toward its natural resume")
}

// TestEscapeVerbsRefuseAMismatchedCursor is T12: the guards every verb shares.
func TestEscapeVerbsRefuseAMismatchedCursor(t *testing.T) {
	at := time.Date(2026, 6, 21, 11, 40, 0, 0, time.UTC)

	type testCase struct {
		name    string
		build   func(cmdID, incID string) engine.Trigger
		wantErr error
		// setup selects the state: "" is a live stalled walk, "running" is an
		// instance with no walk in flight at all.
		running bool
		badCmd  bool
		badInc  bool
	}

	cases := []testCase{
		{
			name:    "retry with no walk in flight",
			build:   func(c, i string) engine.Trigger { return engine.NewRetryStalledCompensation(at, c, i) },
			running: true, wantErr: engine.ErrNoCompensationWalk,
		},
		{
			name:    "skip with no walk in flight",
			build:   func(c, i string) engine.Trigger { return engine.NewSkipStalledCompensation(at, c, i) },
			running: true, wantErr: engine.ErrNoCompensationWalk,
		},
		{
			name:    "abandon with no walk in flight",
			build:   func(c, i string) engine.Trigger { return engine.NewAbandonCompensationWalk(at, c, i) },
			running: true, wantErr: engine.ErrNoCompensationWalk,
		},
		{
			name:    "retry naming a command the walk has moved past",
			build:   func(c, i string) engine.Trigger { return engine.NewRetryStalledCompensation(at, c, i) },
			badCmd:  true,
			wantErr: engine.ErrCompensationCommandMismatch,
		},
		{
			name:    "skip naming a command the walk has moved past",
			build:   func(c, i string) engine.Trigger { return engine.NewSkipStalledCompensation(at, c, i) },
			badCmd:  true,
			wantErr: engine.ErrCompensationCommandMismatch,
		},
		{
			name:    "abandon naming a command the walk has moved past",
			build:   func(c, i string) engine.Trigger { return engine.NewAbandonCompensationWalk(at, c, i) },
			badCmd:  true,
			wantErr: engine.ErrCompensationCommandMismatch,
		},
		{
			// A mistyped incident id must NOT silently fall back to a walk-wide
			// action — unlike ResolveIncident, whose unknown-id case is an
			// idempotent no-op.
			name:    "skip naming an incident that does not exist",
			build:   func(c, i string) engine.Trigger { return engine.NewSkipStalledCompensation(at, c, i) },
			badInc:  true,
			wantErr: engine.ErrStallIncidentNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := threeCompensableDef()
			opt := engine.StepOptions{CompensationStallAfter: stallWindow}

			var state engine.InstanceState
			var cmdID string
			if tc.running {
				state = runThreeCompensableActivities(t)
				cmdID = "any-command"
				require.Empty(t, state.Compensating.ActiveCmdID, "control: no walk in flight")
			} else {
				state, cmdID, _ = startedStallWalk(t)
			}
			if tc.badCmd {
				cmdID = "a-command-the-walk-passed"
			}
			incID := ""
			if tc.badInc {
				incID = "inc-does-not-exist"
			}

			res, err := engine.Step(t.Context(), def, state, tc.build(cmdID, incID), opt)

			require.Error(t, err)
			assert.ErrorIs(t, err, tc.wantErr)
			assert.Empty(t, res.Commands, "a refused verb must dispatch nothing")
		})
	}
}

// TestSkipDischargesTheDeferredCancelDeadlock is T9 — with the verb the
// implementation corrected.
//
// A cancel arriving during an in-flight walk does not terminate: handleCancelRequested
// stamps PendingCancel + PendingFinalStatus/PendingFinalErr to be consumed by an
// applyFinish that, on a stalled walk, never runs. Measured on main, a second
// cancel returns err=nil with ZERO commands and leaves pendingCancel=true.
//
// ⚠ ADR-0175 as written said ABANDON discharges this. Measured, it cannot:
// PendingCancel is only ever stamped on a walk that RESUMES — handleCancelRequested
// requires ResumeNode or ReverseNode to be non-empty — and the audit's C3 finding
// made abandon refuse exactly those walks. The two decisions are incompatible, and
// SKIP is the verb that actually discharges it:
//
//	PROBE[throw-walk]    mode=walkThrowScopeWide pendingCancel=false
//	PROBE[after-cancel]  cmds=0                  pendingCancel=true
//	PROBE[after-skip-1]  status=compensating     pendingCancel=true
//	PROBE[after-skip-2]  status=terminated       pendingCancel=false  cmds=[CancelTimer FailInstance{cancelled}]
func TestSkipDischargesTheDeferredCancelDeadlock(t *testing.T) {
	at := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	def := rootSagaWithScopeWideThrow()
	opt := engine.StepOptions{CompensationStallAfter: stallWindow}

	res := driveToScopeWideThrowWithOptions(t, def, "stall-deadlock", at, opt)
	undoB := invokeActionNamed(res.Commands, "undoB")
	require.NotNil(t, undoB)

	// A cancel while the walk is in flight: deferred, not applied.
	deferred, err := engine.Step(t.Context(), def, res.State,
		engine.NewCancelRequested(at.Add(time.Minute)), opt)
	require.NoError(t, err)
	require.Empty(t, deferred.Commands, "control: the cancel emits nothing — it is DEFERRED")
	require.True(t, engine.PendingCancelOf(&deferred.State), "control: to an applyFinish that never runs")

	// Abandon cannot rescue this: the walk resumes, so C3 refuses it.
	_, abandonErr := engine.Step(t.Context(), def, deferred.State,
		engine.NewAbandonCompensationWalk(at.Add(2*time.Minute), undoB.CommandID, ""), opt)
	require.ErrorIs(t, abandonErr, engine.ErrCompensationWalkResumes,
		"abandon is refused on a resuming walk, so it CANNOT be what discharges this deadlock")

	// Skip drains the walk to its finish, where consumePendingCancel runs.
	cur, err := engine.Step(t.Context(), def, deferred.State,
		engine.NewSkipStalledCompensation(at.Add(2*time.Minute), undoB.CommandID, ""), opt)
	require.NoError(t, err)
	undoA := invokeActionNamed(cur.Commands, "undoA")
	require.NotNil(t, undoA, "control: skip advances to the walk's remaining record")

	fin, err := engine.Step(t.Context(), def, cur.State,
		engine.NewSkipStalledCompensation(at.Add(3*time.Minute), undoA.CommandID, ""), opt)
	require.NoError(t, err)

	assert.False(t, engine.PendingCancelOf(&fin.State), "the deferred cancel is CONSUMED")
	assert.Equal(t, engine.StatusTerminated, fin.State.Status,
		"the walk terminates instead of resuming at afterThrow")
	assert.Contains(t, fin.Commands, engine.FailInstance{Err: "cancelled"},
		"and the instance dies of the cancel the operator asked for")
}

// TestRetryFindsTheRecordThroughANonEmptyArchiveKey is T6b.
//
// cursorRecords resolves an unpinned cursor through its ArchiveKey. A targeted
// compensation throw is the walk that sets one, so retry must read the record
// from the archive rather than from the scope's live list.
func TestRetryFindsTheRecordThroughANonEmptyArchiveKey(t *testing.T) {
	at := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	opt := engine.StepOptions{CompensationStallAfter: stallWindow}
	def := targetedThrowOnceDef()

	res, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "stall-archivekey"},
		engine.NewStartInstance(at, nil), opt)
	require.NoError(t, err)
	doInner := invokeActionNamed(res.Commands, "doInner")
	require.NotNil(t, doInner, "control: the sub-process body runs")

	res, err = engine.Step(t.Context(), def, res.State,
		engine.NewActionCompleted(at.Add(time.Second), doInner.CommandID, nil), opt)
	require.NoError(t, err)
	undoInner := invokeActionNamed(res.Commands, "undoInner")
	require.NotNil(t, undoInner, "control: the targeted throw dispatched the archived record")
	require.NotEmpty(t, engine.ArchiveKeyOf(&res.State),
		"control: a TARGETED throw pins an ArchiveKey — the read path this test is about")

	retried, err := engine.Step(t.Context(), def, res.State,
		engine.NewRetryStalledCompensation(at.Add(time.Hour), undoInner.CommandID, ""), opt)
	require.NoError(t, err)

	again := invokeActionNamed(retried.Commands, "undoInner")
	require.NotNil(t, again, "retry must find the record through the ArchiveKey")
	assert.NotEqual(t, undoInner.CommandID, again.CommandID, "under a fresh command id")
}
