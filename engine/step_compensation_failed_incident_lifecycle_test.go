package engine_test

// step_compensation_failed_incident_lifecycle_test.go —
// IncidentCompensationFailed has a lifecycle, not just a birth.
//
// ⚠ The design as written was self-contradictory: it said the incident is
// "retired when the walk advances past the record … keyed on CommandID" AND that
// "the exhaustion incident is kept". On the failure path the incident was just
// raised with CommandID == ActiveCmdID, so a retirement keyed on ActiveCmdID at
// advance time would delete the very record that must survive. The resolution
// implemented here — controller adjudication — splits
// the retirement across the two routes by which a record stops being owed:
//
//   - retryFailedCompensation retires the OLD command id as it re-dispatches:
//     that attempt is superseded, and its incident is not the durable record.
//   - handleActionCompleted's compensation short-circuit retires ActiveCmdID:
//     the record ultimately SUCCEEDED, so nothing is left behind.
//   - the failure/exhaustion path retires NOTHING.
//
// which yields the stated bound exactly: one incident per exhausted
// record, not one per attempt.
//
// Measured before the change, on a cancel-started three-record walk:
//   - after a retry re-dispatch:            incidents=1  (the superseded attempt)
//   - after a late success mid-backoff:     incidents=1
//   - after fail → retry → fail (exhausted): incidents=2 (one PER ATTEMPT)

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// driveToCompensationFailure drives threeCompensableDef to a cancel-started walk
// whose first dispatched record (c3) has failed once under opt, and returns the
// state and the failed command id.
//
// Every require is a control: without them a later assertion about retirement
// could pass over a state that never raised an incident at all.
func driveToCompensationFailure(t *testing.T, at time.Time, opt engine.StepOptions) (engine.InstanceState, string) {
	t.Helper()
	def := threeCompensableDef()

	started, err := engine.Step(t.Context(), def, runThreeCompensableActivities(t),
		engine.NewCancelRequested(at), opt)
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, started.State.Status)
	c3 := invokeActionNamed(started.Commands, "c3")
	require.NotNil(t, c3, "control: reverse order dispatches c3 first")

	failed, err := engine.Step(t.Context(), def, started.State,
		engine.NewActionFailed(at.Add(time.Second), c3.CommandID, "c3 blew up", true), opt)
	require.NoError(t, err)
	require.Len(t, failed.State.Incidents, 1,
		"control: the failure must have raised exactly one incident")
	require.Equal(t, c3.CommandID, failed.State.Incidents[0].CommandID,
		"control: keyed on the command that failed")

	return failed.State, c3.CommandID
}

// oneRetryPermitted allows a single retry per record: the budget term is
// RetryAttempts+1 >= MaxAttempts, so a record that has spent one attempt is
// exhausted.
func oneRetryPermitted() engine.StepOptions {
	return engine.StepOptions{
		CompensationRetryPolicy: &model.RetryPolicy{MaxAttempts: 2, InitialInterval: time.Second},
	}
}

// TestCompensationFailedIncidentLifecycle covers the incident's retirement
// routes.
func TestCompensationFailedIncidentLifecycle(t *testing.T) {
	at := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name   string
		opt    engine.StepOptions
		setup  func(t *testing.T, opt engine.StepOptions) (engine.InstanceState, engine.Trigger)
		assert func(t *testing.T, res engine.StepResult)
	}

	cases := []testCase{
		{
			// Measured before: incidents=1 — the superseded attempt's incident
			// survived the re-dispatch, so a record retried five times would
			// accumulate five, against the "one per exhausted record" bound.
			name: "the re-dispatch retires the superseded attempt's incident",
			opt:  compensationRetryOn(),
			setup: func(t *testing.T, opt engine.StepOptions) (engine.InstanceState, engine.Trigger) {
				state, _ := driveToCompensationFailure(t, at, opt)
				armed := retryTimerRecords(state)
				require.Len(t, armed, 1, "control: a backoff must be armed to fire")
				return state, engine.NewTimerFired(at.Add(2*time.Second), armed[0].TimerID)
			},
			assert: func(t *testing.T, res engine.StepResult) {
				require.NotNil(t, invokeActionNamed(res.Commands, "c3"),
					"control: the record was re-dispatched, which is when the previous "+
						"attempt becomes superseded")
				assert.Empty(t, res.State.Incidents,
					"the superseded attempt's incident is retired as the retry "+
						"re-dispatches — the durable record is the EXHAUSTION incident, "+
						"not one per attempt")
			},
		},
		{
			// Measured before: incidents=1 — a record that ultimately SUCCEEDED left
			// a "compensation action failed" incident behind on the instance, which
			// on a terminal walk is also what the cause-of-death readers publish.
			name: "a late success during a live backoff retires the incident",
			opt:  compensationRetryOn(),
			setup: func(t *testing.T, opt engine.StepOptions) (engine.InstanceState, engine.Trigger) {
				state, failedCmdID := driveToCompensationFailure(t, at, opt)
				// The worker answers late: the action actually succeeded.
				return state, engine.NewActionCompleted(at.Add(2*time.Second), failedCmdID, nil)
			},
			assert: func(t *testing.T, res engine.StepResult) {
				require.NotNil(t, invokeActionNamed(res.Commands, "c2"),
					"control: the walk advanced past the record")
				assert.Empty(t, res.State.Incidents,
					"the record ultimately succeeded, so nothing is left behind")
			},
		},
		{
			// Measured before: incidents=2 — one per ATTEMPT. This row is what pins
			// the retirement as NARROW: it must not reach the failure path, or the
			// durable record of an unrecoverable compensation disappears.
			name: "an exhausted record keeps exactly one incident, not one per attempt",
			opt:  oneRetryPermitted(),
			setup: func(t *testing.T, opt engine.StepOptions) (engine.InstanceState, engine.Trigger) {
				def := threeCompensableDef()
				state, _ := driveToCompensationFailure(t, at, opt)
				armed := retryTimerRecords(state)
				require.Len(t, armed, 1)

				redispatched, err := engine.Step(t.Context(), def, state,
					engine.NewTimerFired(at.Add(2*time.Second), armed[0].TimerID), opt)
				require.NoError(t, err)
				retryCmd := invokeActionNamed(redispatched.Commands, "c3")
				require.NotNil(t, retryCmd, "control: the one permitted retry was dispatched")

				// The retry fails too, and the budget is spent.
				return redispatched.State,
					engine.NewActionFailed(at.Add(3*time.Second), retryCmd.CommandID, "c3 blew up again", true)
			},
			assert: func(t *testing.T, res engine.StepResult) {
				require.NotNil(t, invokeActionNamed(res.Commands, "c2"),
					"control: exhausted, so the walk skips and continues")

				require.Len(t, res.State.Incidents, 1,
					"exactly ONE incident survives per exhausted record — the retirement "+
						"must not reach the failure path, and must not spare the "+
						"superseded attempts either")
				inc := res.State.Incidents[0]
				assert.Equal(t, engine.IncidentCompensationFailed, inc.Kind)
				assert.Equal(t, "c3 blew up again", inc.Error,
					"and it is the LAST attempt's incident, the one that exhausted the budget")
				assert.Equal(t, "step3", inc.NodeID)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := threeCompensableDef()
			state, trig := tc.setup(t, tc.opt)

			res, err := engine.Step(t.Context(), def, state, trig, tc.opt)
			require.NoError(t, err)

			tc.assert(t, res)
		})
	}
}

// The two tests below are deliberately NOT folded into the table above: each
// drives a structurally different sequence (an operator verb; a walk driven to
// termination) and asserts on a different surface, so they share no call shape
// with the retirement rows or with each other.

// TestCompensationFailedIncidentIsNotResolvable covers the other half: the kind
// stays non-resolvable, consistent with IncidentCompensationStall.
//
// ⚠ It is a CONTROL, not a red-first test, and the "add no new case if none is
// needed" instruction was the right one — executed before writing any code,
// handleResolveIncident's existing whitelist already refused it:
//
//	err=workflow-engine: this incident is not resolvable with resolve-incident;
//	    use the compensation-walk verbs (retry, skip, abandon):
//	    workflow-engine: invalid state transition
//	cmds=[] incidents=0
//
// (The message was measured as "compensation-stall verbs" and reworded to
// "compensation-walk verbs" later in this same delivery, once the sentinel had a
// second walk-scoped kind to refuse; nothing else about the observation moved.)
//
// So NO production line was added for it. The guard is `inc.Kind !=
// IncidentAction`, a whitelist rather than a per-kind blacklist, which is why a
// kind introduced later inherits the refusal for free. Mutation-verified
// in the report by turning that whitelist into a stall-only blacklist: the call
// then returns err=<nil> having eaten the incident.
func TestCompensationFailedIncidentIsNotResolvable(t *testing.T) {
	at := time.Date(2026, 8, 17, 11, 0, 0, 0, time.UTC)
	def := threeCompensableDef()
	opt := compensationRetryOn()

	state, _ := driveToCompensationFailure(t, at, opt)
	inc := state.Incidents[0]
	require.Equal(t, engine.IncidentCompensationFailed, inc.Kind)
	require.Equal(t, engine.StatusCompensating, state.Status,
		"fixture precondition: NOT terminal, or ErrInstanceTerminal answers first "+
			"and this test would pass for the wrong reason")

	res, err := engine.Step(t.Context(), def, state,
		engine.NewResolveIncident(at.Add(2*time.Second), inc.ID, 1), opt)

	require.Error(t, err, "resolve-incident must refuse a compensation-failure incident")
	assert.ErrorIs(t, err, engine.ErrIncidentNotResolvable)
	assert.Empty(t, res.Commands, "and it must re-invoke nothing")

	// The load-bearing half: the refusal must not CONSUME the incident. Without
	// the guard the handler removes it, finds no token for TokenID "" and returns
	// a clean no-op — the failure would be deleted as well as unresolved.
	replay, err := engine.Step(t.Context(), def, state,
		engine.NewResolveIncident(at.Add(3*time.Second), inc.ID, 1), opt)
	require.ErrorIs(t, err, engine.ErrIncidentNotResolvable,
		"the incident must still be there to refuse a second time")
	assert.Empty(t, replay.Commands)
}

// TestCompensationFailedIncidentSurvivesEndInstance pins that the durable record
// must survive BOTH of endInstance's incident sweeps.
//
// ⚠ It is a CONTROL — measured before any code was written, the incident already
// survived (`AFTER TERMINATE: status=terminated incidents=1`). Nothing in the
// design said so and nothing tested it, which is the point.
//
// ⚠ THE FIXTURE IS WHAT MAKES IT NON-VACUOUS. The natural drive — fail, advance,
// drain the walk — leaves the incident naming a command the cursor has long
// moved past, so endInstance's retireCompensationStallIncidents(ActiveCmdID)
// could not match it whatever its kind, and the KIND term would be untested. The
// walk is therefore ABANDONED while the failed command is still active, so both
// abandonCompensationWalk's own sweep and endInstance's remainder sweep are
// called with exactly the command id this incident carries, and only the kind
// term stands between them. Mutation-verified in the report by widening that
// sweep's predicate to the failure kind.
//
// The second sweep, removeOrphanedIncidents, is answered by TokenID: the record
// carries "" and an empty key names no token, asserted below.
func TestCompensationFailedIncidentSurvivesEndInstance(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	def := threeCompensableDef()
	opt := compensationRetryOn()

	state, failedCmdID := driveToCompensationFailure(t, at, opt)
	inc := state.Incidents[0]
	require.Equal(t, failedCmdID, inc.CommandID)
	require.Equal(t, failedCmdID, engine.ActiveCompensationCmdID(&state),
		"control: the incident names the STILL-ACTIVE command, so both endInstance "+
			"sweeps are aimed straight at it")
	require.Empty(t, inc.TokenID,
		"control: walk-scoped, which is what removeOrphanedIncidents keys on")

	// Abandon terminates the instance with the walk still on that command.
	res, err := engine.Step(t.Context(), def, state,
		engine.NewAbandonCompensationWalk(at.Add(2*time.Second), failedCmdID, ""), opt)
	require.NoError(t, err)
	require.True(t, res.State.Status.IsTerminal(),
		"control: the terminal transition must actually have run")

	require.Len(t, res.State.Incidents, 1,
		"the durable record of an unrecoverable compensation must outlive the "+
			"instance: it is all an operator has after the fact")
	assert.Equal(t, engine.IncidentCompensationFailed, res.State.Incidents[0].Kind)
	assert.Equal(t, inc.ID, res.State.Incidents[0].ID)
}
