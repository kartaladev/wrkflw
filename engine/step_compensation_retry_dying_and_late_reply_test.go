package engine_test

// step_compensation_retry_dying_and_late_reply_test.go — the retry-on-dying and
// late-reply surfaces the rest of the delivery could not reach.
//
// Both tests here are CONTROLS rather than red-first tests: the predicate split
// (TimerKind.firesOnDyingInstance) and the dispatched-id ring shipped in earlier
// steps of this same delivery, and each was exercised there only on a hand-built
// record. What was missing is the END-TO-END case — a real walk, a real arm, the
// real handler — and, in both cases, the FIXTURE that makes the assertion capable
// of failing. Each is mutation-verified in the delivery report instead.
//
// The two are separate TestXxx functions, not one table: one fires a timer on a
// dying instance and the other replies to a superseded command on a live one, so
// they share no call shape.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
)

// TestCompensationRetryTimerFiresOnADyingWalk covers the end-to-end fire of a
// compensation retry timer on a dying walk.
//
// ⚠ THE FIXTURE MUST BE A CANCEL-STARTED WALK. A cancel walk is walkAdmin, so
// walkTerminates is true and spawnsNewWork() is FALSE — the instance is already
// dying, and the guard in handleTimerFired's path 4 refuses every fired
// timer record whose kind does not answer firesOnDyingInstance(). A compensation
// THROW walk measures spawnsNewWork()==TRUE, so the guard is not consulted at
// all and the test would pass no matter how the predicate answered — the
// opposite half of the constraint on the retirement test.
//
// The fixture asserts its own dying-ness rather than assuming it, so a future
// change to spawnsNewWork() cannot quietly turn this into a test about a healthy
// instance.
//
// Measured today: dying=true, and the fire re-dispatches c3 under a fresh id. It
// is the guard's exemption that produces that, not the absence of a guard — the
// mutation that reddens it is narrowing firesOnDyingInstance() back to the stall
// kind, which is the state this delivery started from.
func TestCompensationRetryTimerFiresOnADyingWalk(t *testing.T) {
	at := time.Date(2026, 8, 17, 13, 0, 0, 0, time.UTC)
	def := threeCompensableDef()
	opt := compensationRetryOn()

	state, failedCmdID := driveToCompensationFailure(t, at, opt)
	require.False(t, engine.SpawnsNewWork(&state),
		"fixture precondition: a cancel-started walk is DYING — on a throw walk "+
			"spawnsNewWork() is true and the guard is never consulted")
	armed := retryTimerRecords(state)
	require.Len(t, armed, 1, "control: a real backoff, armed by armCompensationRetryTimer")

	res, err := engine.Step(t.Context(), def, state,
		engine.NewTimerFired(at.Add(2*time.Second), armed[0].TimerID), opt)
	require.NoError(t, err)

	redispatched := invokeActionNamed(res.Commands, "c3")
	require.NotNil(t, redispatched,
		"a compensation retry is the walk's OWN work, not the instance's forward "+
			"work: refusing it on a dying instance would strand exactly the walks an "+
			"operator most needs to see complete")
	assert.NotEqual(t, failedCmdID, redispatched.CommandID, "under a fresh command id")
	assert.Equal(t, engine.StatusCompensating, res.State.Status,
		"and the walk is still draining, not refused into silence")
	assert.Empty(t, retryTimerRecords(res.State),
		"the consumed backoff record is gone — the refusal path would have retired "+
			"it too, so this alone does not distinguish the two")
}

// TestLateReplyToASupersededRetryCommandIsBenign covers the retry site, the
// fifth dispatch site, which would otherwise have shipped untested.
//
// A retry re-dispatches under a FRESH command id, so an at-least-once worker's
// redelivery of that command's reply arrives after the walk has moved past it.
// Unless the retry site records its id in the dispatched ring, the reply falls
// through to the tokenAwaiting lookup (a walk parks no token on its dispatch),
// returns ErrTokenNotFound and reaches the consumer as a 422 on a perfectly
// healthy walk.
//
// ⚠ THE FIXTURE IS MID-WALK, ON A LIVE INSTANCE. A post-finish cancel fixture is
// vacuous: dispatch's structural guard answers first on a terminated instance and
// returns err=<nil> even for a command id that was never dispatched at all, so
// the assertion would hold with the ring removed.
//
// Mutation-verified in the report by deleting retryFailedCompensation's
// recordCompensationDispatch call: both rows then return ErrTokenNotFound.
func TestLateReplyToASupersededRetryCommandIsBenign(t *testing.T) {
	at := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)

	type testCase struct {
		name string
		// reply builds the LATE duplicate reply to the superseded retry command.
		reply func(at time.Time, retryCmdID string) engine.Trigger
	}

	cases := []testCase{
		{
			name: "a redelivered ActionCompleted",
			reply: func(at time.Time, retryCmdID string) engine.Trigger {
				return engine.NewActionCompleted(at, retryCmdID, nil)
			},
		},
		{
			name: "a redelivered ActionFailed",
			reply: func(at time.Time, retryCmdID string) engine.Trigger {
				return engine.NewActionFailed(at, retryCmdID, "late failure", true)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := threeCompensableDef()
			opt := compensationRetryOn()

			state, _ := driveToCompensationFailure(t, at, opt)
			armed := retryTimerRecords(state)
			require.Len(t, armed, 1)

			fired, err := engine.Step(t.Context(), def, state,
				engine.NewTimerFired(at.Add(2*time.Second), armed[0].TimerID), opt)
			require.NoError(t, err)
			retryCmd := invokeActionNamed(fired.Commands, "c3")
			require.NotNil(t, retryCmd, "control: the record was re-dispatched")

			// The retry SUCCEEDS, so the walk advances and that command id is now
			// superseded — while the instance stays live and compensating.
			advanced, err := engine.Step(t.Context(), def, fired.State,
				engine.NewActionCompleted(at.Add(3*time.Second), retryCmd.CommandID, nil), opt)
			require.NoError(t, err)
			require.NotNil(t, invokeActionNamed(advanced.Commands, "c2"),
				"control: mid-walk — the instance is NOT terminal, so the structural "+
					"guard cannot be what makes the redelivery benign")
			require.Equal(t, engine.StatusCompensating, advanced.State.Status)

			res, err := engine.Step(t.Context(), def, advanced.State,
				tc.reply(at.Add(4*time.Second), retryCmd.CommandID), opt)

			require.NoError(t, err,
				"a redelivered reply to a superseded RETRY command is benign, not a 422")
			assert.Empty(t, res.Commands, "and it moves nothing")
			assert.Len(t, res.State.Incidents, len(advanced.State.Incidents),
				"nor does it raise an incident: the walk moved past this record, and "+
					"the failure that mattered was recorded when it was still active")
			assert.Equal(t, engine.StatusCompensating, res.State.Status)
			assert.Equal(t, engine.CompensationCursorView(&advanced.State),
				engine.CompensationCursorView(&res.State),
				"the cursor is byte-identical: the walk did not move")
		})
	}
}
