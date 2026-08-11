package engine_test

// ADR-0175 — a breached stall window raises an incident and changes nothing else.
//
// The fire handler is a case in handleTimerFired's path-4 Kind switch, which
// sits AHEAD of the !spawnsNewWork() guard. That placement is load-bearing:
// the walks that terminate (walkAdmin — cancel, error and the admin full
// rollback — walkReverse, and any throw walk carrying PendingCancel) are exactly
// the walks for which spawnsNewWork() is false, and they are the ones an
// operator most needs to see wedged.
//
// ⚠ Path 4 is NOT inherently safe on a dying instance: a TimerInWait reminder
// was measured emitting a real InvokeAction from there. This handler is safe
// only because it emits NOTHING — a constraint on the handler, not a property
// inherited from its location.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/engine"
)

// startedStallWalk drives the three-compensable fixture to a cancel-started
// compensation walk with detection on, returning the state, the dispatched
// compensation command id, and the armed stall timer's id.
func startedStallWalk(t *testing.T) (engine.InstanceState, string, string) {
	t.Helper()
	state := runThreeCompensableActivities(t)
	at := time.Date(2026, 6, 21, 11, 0, 0, 0, time.UTC)

	res, err := engine.Step(t.Context(), threeCompensableDef(), state,
		engine.NewCancelRequested(at),
		engine.StepOptions{CompensationStallAfter: stallWindow})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, res.State.Status)

	undo := invokeActionNamed(res.Commands, "c3")
	require.NotNil(t, undo)
	recs := stallTimerRecords(res.State)
	require.Len(t, recs, 1)
	return res.State, undo.CommandID, recs[0].TimerID
}

// TestStallTimerFireRaisesIncidentAndNothingElse is T3.
func TestStallTimerFireRaisesIncidentAndNothingElse(t *testing.T) {
	state, cmdID, timerID := startedStallWalk(t)
	cursorBefore := engine.CompensationCursorView(&state)
	at := time.Date(2026, 6, 21, 11, 40, 0, 0, time.UTC)

	res, err := engine.Step(t.Context(), threeCompensableDef(), state,
		engine.NewTimerFired(at, timerID),
		engine.StepOptions{CompensationStallAfter: stallWindow})
	require.NoError(t, err)

	assert.Empty(t, res.Commands,
		"the stall handler must emit NO commands: path 4 runs on dying instances too, "+
			"and emitting there is the measured ADR-0172 hole")

	require.Len(t, res.State.Incidents, 1, "the breach raises exactly one incident")
	inc := res.State.Incidents[0]
	assert.Equal(t, engine.IncidentCompensationStall, inc.Kind)
	assert.Empty(t, inc.TokenID, "the incident is walk-scoped: a stalled walk holds no tokens")
	assert.Equal(t, cmdID, inc.CommandID, "it names the command that went silent")
	assert.Equal(t, "step3", inc.NodeID)
	assert.Equal(t, "compensation action stalled", inc.Error)
	assert.Equal(t, at, inc.CreatedAt)

	assert.Equal(t, cursorBefore, engine.CompensationCursorView(&res.State),
		"the cursor must be untouched: the walk has not moved, only become visible")
	assert.Empty(t, stallTimerRecords(res.State),
		"the fired record is dropped; it has already done its job")
}

// TestStallIncidentIsRaisedOnADyingWalk is T4 — the P1 test.
//
// A cancel-started walk is walkAdmin, so walkTerminates is true and
// spawnsNewWork() is FALSE: this instance is already dying. Detection has to
// work here or it is useless, because the terminating walks are exactly the ones
// an operator finds wedged.
//
// The fixture asserts its own dying-ness rather than assuming it — otherwise a
// future change to spawnsNewWork() would quietly turn this into a test about a
// live instance and nobody would notice.
func TestStallIncidentIsRaisedOnADyingWalk(t *testing.T) {
	state, _, timerID := startedStallWalk(t)
	require.False(t, engine.SpawnsNewWork(&state),
		"fixture precondition: a cancel-started walk is DYING (spawnsNewWork == false)")

	res, err := engine.Step(t.Context(), threeCompensableDef(), state,
		engine.NewTimerFired(time.Date(2026, 6, 21, 11, 40, 0, 0, time.UTC), timerID),
		engine.StepOptions{CompensationStallAfter: stallWindow})
	require.NoError(t, err)

	require.Len(t, res.State.Incidents, 1,
		"path 4 sits AHEAD of the !spawnsNewWork() guard, so a dying walk still reports")
	assert.Equal(t, engine.IncidentCompensationStall, res.State.Incidents[0].Kind)
	assert.Empty(t, res.Commands)
}

// TestLateStallTimerFireIsDropped is T5: a stall record whose CommandID no
// longer matches the cursor raises nothing.
//
// Without the comparison, a timer the scheduler still holds from a command the
// walk already completed would raise a bogus incident against the command
// currently in flight — telling the operator a healthy dispatch had stalled, and
// inviting them to skip or abandon it.
func TestLateStallTimerFireIsDropped(t *testing.T) {
	state, activeCmdID, _ := startedStallWalk(t)
	cursorBefore := engine.CompensationCursorView(&state)

	// A record left over from an earlier command in the same walk.
	engine.AppendStallTimerRecord(&state, "stale-tmr", "step3", "", "a-previous-command")
	require.NotEqual(t, "a-previous-command", activeCmdID)

	res, err := engine.Step(t.Context(), threeCompensableDef(), state,
		engine.NewTimerFired(time.Date(2026, 6, 21, 11, 40, 0, 0, time.UTC), "stale-tmr"),
		engine.StepOptions{CompensationStallAfter: stallWindow})
	require.NoError(t, err)

	assert.Empty(t, res.State.Incidents, "a late fire must raise nothing")
	assert.Empty(t, res.Commands)
	assert.Equal(t, cursorBefore, engine.CompensationCursorView(&res.State))

	for _, tr := range stallTimerRecords(res.State) {
		assert.NotEqual(t, "stale-tmr", tr.TimerID, "the stale record is dropped")
	}
}

// TestStallIncidentRetiredWhenWalkMovesOn is T10 and T10b: an action that
// reports back LATE — after the stall incident was raised — both advances the
// walk and retires the incident.
//
// Both triggers route through stepCompensationAdvance, which is why the sweep
// lives there and not in handleActionCompleted: a sweep placed in the completion
// handler would miss the ActionFailed row entirely (ADR-0034 Decision 4 makes a
// failed compensation a best-effort skip that still advances).
func TestStallIncidentRetiredWhenWalkMovesOn(t *testing.T) {
	type testCase struct {
		name    string
		trigger func(at time.Time, cmdID string) engine.Trigger
		assert  func(t *testing.T, res engine.StepResult)
	}

	cases := []testCase{
		{
			name: "late ActionCompleted advances and retires",
			trigger: func(at time.Time, cmdID string) engine.Trigger {
				return engine.NewActionCompleted(at, cmdID, nil)
			},
			assert: func(t *testing.T, res engine.StepResult) {
				assert.NotNil(t, invokeActionNamed(res.Commands, "c2"),
					"the walk advances to the next record")
			},
		},
		{
			name: "late ActionFailed advances and retires",
			trigger: func(at time.Time, cmdID string) engine.Trigger {
				return engine.NewActionFailed(at, cmdID, "worker gave up", false)
			},
			assert: func(t *testing.T, res engine.StepResult) {
				assert.NotNil(t, invokeActionNamed(res.Commands, "c2"),
					"a failed compensation is a best-effort skip that still advances")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, cmdID, timerID := startedStallWalk(t)
			opt := engine.StepOptions{CompensationStallAfter: stallWindow}
			fireAt := time.Date(2026, 6, 21, 11, 40, 0, 0, time.UTC)

			// Breach the window first, so an incident is open.
			fired, err := engine.Step(t.Context(), threeCompensableDef(), state,
				engine.NewTimerFired(fireAt, timerID), opt)
			require.NoError(t, err)
			require.Len(t, fired.State.Incidents, 1, "precondition: the stall is visible")

			res, err := engine.Step(t.Context(), threeCompensableDef(), fired.State,
				tc.trigger(fireAt.Add(time.Minute), cmdID), opt)
			require.NoError(t, err)

			assert.Empty(t, res.State.Incidents,
				"a walk that moved on must not keep reporting itself as stalled")
			tc.assert(t, res)
		})
	}
}

// TestNormalTerminalFinishLeavesNoStallIncident is T10c — the A-4 measurement.
//
// A walk that stalls, is noticed, and then RECOVERS on its own must terminate
// with its real cause of death. Without the retirement sweep this was measured
// as incidents=1 with Incidents[0].Error == "compensation action stalled":
// runtime/outbox.go's terminalEventErr and runtime/processdriver_action.go's
// terminalErr both return Incidents[0] unconditionally, so the recovered walk
// would publish "compensation action stalled" instead of "cancelled" — to the
// outbox, and to a call-activity parent.
//
// This asserts the ENGINE-side precondition those two readers consume: zero
// stall incidents, with FinalErr intact.
func TestNormalTerminalFinishLeavesNoStallIncident(t *testing.T) {
	state, cmdID, timerID := startedStallWalk(t)
	def := threeCompensableDef()
	opt := engine.StepOptions{CompensationStallAfter: stallWindow}
	at := time.Date(2026, 6, 21, 11, 40, 0, 0, time.UTC)

	// Breach the window, then let the walk recover and run to its terminal finish.
	res, err := engine.Step(t.Context(), def, state, engine.NewTimerFired(at, timerID), opt)
	require.NoError(t, err)
	require.Len(t, res.State.Incidents, 1, "precondition: the stall was noticed")

	for i, name := range []string{"c3", "c2", "c1"} {
		if i > 0 {
			undo := invokeActionNamed(res.Commands, name)
			require.NotNil(t, undo, "walk must dispatch %s", name)
			cmdID = undo.CommandID
		}
		res, err = engine.Step(t.Context(), def, res.State,
			engine.NewActionCompleted(at.Add(time.Duration(i+1)*time.Minute), cmdID, nil), opt)
		require.NoError(t, err)
	}

	require.Equal(t, engine.StatusTerminated, res.State.Status)
	assert.Empty(t, res.State.Incidents,
		"a recovered walk must not publish \"compensation action stalled\" as its cause of death")
	assert.Contains(t, res.Commands, engine.FailInstance{Err: "cancelled"},
		"the instance dies of the cancel that started the walk")
}

// TestForceTerminationSweepsOpenStallIncident covers endInstance's REMAINDER
// sweep, on a real production route rather than a constructed state.
//
// stepCompensationAdvance retires the incident for every walk that MOVES ON. A
// walk killed mid-flight never advances, so nothing retires it there. The
// measured abandonment route (ADR-0173) is a force-termination end event: the
// throw walk is live, a sibling branch's recovery task completes into an
// EndTerminate, and forceTerminate → endInstance ends the instance while the
// stalled command is still outstanding.
//
// Without the sweep the stall incident becomes Incidents[0] on the terminal
// instance, which terminalEventErr publishes as the cause of death.
func TestForceTerminationSweepsOpenStallIncident(t *testing.T) {
	ctx := t.Context()
	opt := engine.StepOptions{CompensationStallAfter: stallWindow}
	def := teardownDef(teardownBodyOpts{
		saga: []string{"A", "B"}, terminateAfterRecovery: true, recoveryIsUserTask: true,
	})
	at := scopeDrainT0
	next := func() time.Time { at = at.Add(time.Second); return at }

	res, err := engine.Step(ctx, def, engine.InstanceState{InstanceID: "stall-ft"},
		engine.NewStartInstance(scopeDrainT0, nil), opt)
	require.NoError(t, err)
	for _, name := range []string{"A", "B"} {
		cmdID := firstInvokeCmd(res.Commands, "do"+name)
		require.NotEmpty(t, cmdID, "control: do%s invoked", name)
		res, err = engine.Step(ctx, def, res.State,
			engine.NewActionCompleted(next(), cmdID, nil), opt)
		require.NoError(t, err)
	}
	require.NotEmpty(t, res.State.Compensating.ActiveCmdID, "control: walk in flight")

	failCmd := firstInvokeCmd(res.Commands, "doFails")
	require.NotEmpty(t, failCmd)
	res, err = engine.Step(ctx, def, res.State,
		engine.NewActionFailed(next(), failCmd, "E1", false), opt)
	require.NoError(t, err)
	human := firstAwaitHuman(res.Commands)
	require.NotNil(t, human, "control: the recovery branch parks")

	// Breach the stall window while the walk is still in flight.
	recs := stallTimerRecords(res.State)
	require.Len(t, recs, 1, "control: the live walk armed a stall timer")
	res, err = engine.Step(ctx, def, res.State, engine.NewTimerFired(next(), recs[0].TimerID), opt)
	require.NoError(t, err)
	require.Len(t, res.State.Incidents, 1, "precondition: the stall is visible")

	// Force-terminate: the recovery task completes into the terminate end event.
	res, err = engine.Step(ctx, def, res.State,
		engine.NewHumanCompleted(next(), human.TaskID, engine.CompletionInput{}, authz.Actor{ID: "u1"}), opt)
	require.NoError(t, err)
	require.Equal(t, engine.StatusTerminated, res.State.Status,
		"control: the force-termination end event abandoned the walk")

	assert.Empty(t, res.State.Incidents,
		"endInstance must sweep the stall incident of a walk it kills mid-flight, "+
			"or it becomes the terminal instance's published cause of death")
}
