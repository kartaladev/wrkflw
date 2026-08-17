package engine

// step_compensation_retry_test.go — ADR-0179 Decisions 2, 3 and 7: a failed
// compensation action is RE-DISPATCHED when the consumer opts in, after a
// backoff timer of the new TimerCompensationRetry kind.
//
// White-box, for the same two reasons the sibling
// step_compensation_failure_visibility_test.go gives, plus a third of its own:
// the backoff state machine lives entirely on the unexported compensationCursor
// (RetryAttempts / RetryTimerID), and its per-record reset is only observable by
// reading those fields across an advance.
//
// The single-case tests below (redelivery, per-record budget, the stall-cancel
// and the HasArmedTimers control) are deliberately not folded into the tables
// above: each drives a DIFFERENT multi-Step sequence to reach its state, so they
// share no call shape with the tables or with each other.
//
// No t.Parallel anywhere in this file. TestFailedCompensationActionIsVisible in
// the sibling file swaps the process-global slog.Default via
// installCaptureHandler and documents itself as sequential by construction;
// these tests emit the same WARN message, so keeping them sequential too removes
// any chance of one file's log line being harvested by the other's handler.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
)

// scheduledTimerOfKind returns the single ScheduleTimer of kind k in cmds, or
// nil when there is none.
func scheduledTimerOfKind(cmds []Command, k TimerKind) *ScheduleTimer {
	for _, c := range cmds {
		if st, ok := c.(ScheduleTimer); ok && st.Kind == k {
			return &st
		}
	}
	return nil
}

// timerRecordsOfKind returns every record in s.Timers whose Kind is k.
func timerRecordsOfKind(s InstanceState, k TimerKind) []timerRecord {
	var out []timerRecord
	for _, tr := range s.Timers {
		if tr.Kind == k {
			out = append(out, tr)
		}
	}
	return out
}

// TestFailedCompensationArmsARetryBackoff covers the DECISION half of ADR-0179
// Decision 3: which failures take the retry branch and which fall through to the
// skip-and-advance ADR-0034 Decision 4 has always done.
//
// What makes each row fail today: the retry branch does not exist at all, so
// EVERY row currently takes the advance path. The three fall-through rows are
// therefore not vacuous by accident — they are the regression guard that the new
// branch is narrow, and they are mutation-verified in the report by widening the
// condition.
func TestFailedCompensationArmsARetryBackoff(t *testing.T) {
	failedAt := time.Date(2026, 8, 17, 11, 0, 0, 0, time.UTC)

	type testCase struct {
		name      string
		policy    *model.RetryPolicy
		retryable bool
		assert    func(t *testing.T, cmdID string, res StepResult)
	}

	cases := []testCase{
		{
			name:      "a retryable failure under a policy arms the backoff and does not advance",
			policy:    &model.RetryPolicy{MaxAttempts: 5, InitialInterval: 2 * time.Second},
			retryable: true,
			assert: func(t *testing.T, cmdID string, res StepResult) {
				assert.Empty(t, invokeActionName(res.Commands),
					"the walk must NOT advance: the record is being retried, not skipped")

				st := scheduledTimerOfKind(res.Commands, TimerCompensationRetry)
				require.NotNil(t, st, "a retry backoff timer must be scheduled")

				cur := res.State.Compensating
				assert.Equal(t, cmdID, cur.ActiveCmdID,
					"ActiveCmdID SURVIVES the failure and keeps naming the failed command")
				assert.Equal(t, 1, cur.NextIndex, "the cursor does not move")
				assert.Equal(t, 1, cur.RetryAttempts, "one attempt has been spent")
				assert.Equal(t, st.TimerID, cur.RetryTimerID,
					"RetryTimerID names the armed backoff — the ONLY thing that makes a "+
						"redelivered ActionFailed for the still-active command idempotent")

				recs := timerRecordsOfKind(res.State, TimerCompensationRetry)
				require.Len(t, recs, 1, "exactly one retry record")
				assert.Equal(t, st.TimerID, recs[0].TimerID)
				assert.Equal(t, cmdID, recs[0].CommandID,
					"the record carries the FAILED command so the fired handler can "+
						"make its late-fire check")
				assert.Equal(t, "beta", recs[0].NodeID)

				require.Len(t, res.State.Incidents, 1)
				assert.Equal(t, IncidentCompensationFailed, res.State.Incidents[0].Kind,
					"the failure is visible whether or not it is retried")
			},
		},
		{
			name:      "no policy skips and advances, exactly as before ADR-0179",
			policy:    nil,
			retryable: true,
			assert: func(t *testing.T, _ string, res StepResult) {
				// The REGRESSION GUARD for every existing consumer: with retry off the
				// whole command stream is one InvokeAction, exactly as measured on this
				// branch's base. Asserting the length as well as the name is what makes
				// it a guard rather than a smoke test — a stray ScheduleTimer or
				// CancelTimer leaking into the default path would otherwise pass.
				require.Len(t, res.Commands, 1,
					"default-off must keep ADR-0034 Decision 4's command stream byte-for-byte")
				assert.Equal(t, "undo-alpha", invokeActionName(res.Commands))
				assert.Empty(t, res.State.Timers, "and arms nothing")
				assert.Empty(t, res.State.Compensating.RetryTimerID)
				assert.Zero(t, res.State.Compensating.RetryAttempts)
			},
		},
		{
			name:      "a non-retryable failure skips and advances even under a policy",
			policy:    &model.RetryPolicy{MaxAttempts: 5, InitialInterval: 2 * time.Second},
			retryable: false,
			assert: func(t *testing.T, _ string, res StepResult) {
				assert.Equal(t, "undo-alpha", invokeActionName(res.Commands))
				assert.Nil(t, scheduledTimerOfKind(res.Commands, TimerCompensationRetry))
				assert.Empty(t, res.State.Compensating.RetryTimerID)
			},
		},
		{
			name: "a NonRetryableErrors match skips and advances",
			policy: &model.RetryPolicy{
				MaxAttempts:        5,
				InitialInterval:    2 * time.Second,
				NonRetryableErrors: []string{"blew up"},
			},
			retryable: true,
			assert: func(t *testing.T, _ string, res StepResult) {
				assert.Equal(t, "undo-alpha", invokeActionName(res.Commands),
					"IsNonRetryable is honoured the way the token path in the same "+
						"function honours it")
				assert.Nil(t, scheduledTimerOfKind(res.Commands, TimerCompensationRetry))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := twoCompensableDef()
			state, cmdID := driveToCompensationWalk(t, def, failedAt.Add(-time.Minute), StepOptions{})

			res, err := Step(t.Context(), def, state,
				NewActionFailed(failedAt, cmdID, "undo-beta blew up", tc.retryable),
				StepOptions{CompensationRetryPolicy: tc.policy})
			require.NoError(t, err)

			tc.assert(t, cmdID, res)
		})
	}
}

// driveToRetryBackoff drives twoCompensableDef to a cancel-started walk whose
// beta record has failed once under pol, leaving a TimerCompensationRetry armed.
// It returns the state, the FAILED command id and the armed timer id.
//
// Every require is a control: without them a later assertion about the
// re-dispatch could pass over a state that never armed a backoff at all.
func driveToRetryBackoff(t *testing.T, def *model.ProcessDefinition, at time.Time, opt StepOptions) (InstanceState, string, string) {
	t.Helper()

	// opt is threaded into the WALK DRIVE as well, not only into the failure:
	// beginCompensation is where the first stall guard is armed, so a caller
	// setting CompensationStallAfter here is the only way to reach a failure that
	// has a live stall record to cancel.
	state, cmdID := driveToCompensationWalk(t, def, at.Add(-time.Minute), opt)
	res, err := Step(t.Context(), def, state,
		NewActionFailed(at, cmdID, "undo-beta blew up", true), opt)
	require.NoError(t, err)

	st := scheduledTimerOfKind(res.Commands, TimerCompensationRetry)
	require.NotNil(t, st, "control: the fixture must actually arm a retry backoff")
	require.Equal(t, st.TimerID, res.State.Compensating.RetryTimerID)
	require.Equal(t, cmdID, res.State.Compensating.ActiveCmdID,
		"control: the failed command must still be the active one")

	return res.State, cmdID, st.TimerID
}

// TestCompensationRetryFiredRedispatchesUnderAFreshCommandID covers
// retryFailedCompensation, the TimerCompensationRetry fire handler.
//
// What makes the happy row fail before the handler exists: handleTimerFired's
// path-4 Kind switch has no case for the kind, so the record falls through to
// path 5, finds no token parked on the timer id, and returns a clean no-op —
// measured `re-dispatched undo-beta again? false`. The three guard rows fail in
// the same way for the opposite reason: without a handler nothing consumes the
// leaked record either.
func TestCompensationRetryFiredRedispatchesUnderAFreshCommandID(t *testing.T) {
	firedAt := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	retryOn := StepOptions{
		CompensationRetryPolicy: &model.RetryPolicy{MaxAttempts: 5, InitialInterval: time.Second},
	}

	type testCase struct {
		name string
		// opt is passed to the TimerFired Step. It is a per-case field and not a
		// shared constant deliberately: the stall-re-arm assertion below is silently
		// vacuous under StepOptions{}, because armCompensationStallTimer is a no-op
		// when stallAfter is zero — measured, the row went green-then-red on exactly
		// that.
		opt    StepOptions
		setup  func(t *testing.T, def *model.ProcessDefinition) (InstanceState, string, string)
		assert func(t *testing.T, failedCmdID string, res StepResult, err error)
	}

	cases := []testCase{
		{
			name: "the record is re-dispatched under a fresh command id",
			opt: StepOptions{
				CompensationRetryPolicy: retryOn.CompensationRetryPolicy,
				CompensationStallAfter:  30 * time.Second,
			},
			setup: func(t *testing.T, def *model.ProcessDefinition) (InstanceState, string, string) {
				return driveToRetryBackoff(t, def, firedAt.Add(-time.Minute), StepOptions{
					CompensationRetryPolicy: retryOn.CompensationRetryPolicy,
					CompensationStallAfter:  30 * time.Second,
				})
			},
			assert: func(t *testing.T, failedCmdID string, res StepResult, err error) {
				require.NoError(t, err)

				assert.Equal(t, "undo-beta", invokeActionName(res.Commands),
					"the SAME record is tried again — the walk did not advance to undo-alpha")
				fresh := invokeIDForAction(t, res.Commands, "undo-beta")
				assert.NotEqual(t, failedCmdID, fresh, "under a FRESH command id")

				cur := res.State.Compensating
				assert.Equal(t, fresh, cur.ActiveCmdID)
				assert.Equal(t, 1, cur.NextIndex, "still the same record")
				assert.Empty(t, cur.RetryTimerID, "the backoff is over — its id must be cleared")
				assert.Equal(t, 1, cur.RetryAttempts,
					"the attempt was counted when the backoff was ARMED, not when it fired")

				assert.Contains(t, res.State.RecentCompensationCmdIDs, fresh,
					"the fifth dispatch site must record its command id, or a late reply "+
						"to the RETRY command still returns ErrTokenNotFound")

				assert.Empty(t, timerRecordsOfKind(res.State, TimerCompensationRetry),
					"the consumed retry record is removed, so a duplicate fire is a no-op")
				assert.Len(t, timerRecordsOfKind(res.State, TimerCompensationStall), 1,
					"the stall guard is re-armed against the new dispatch, as at every "+
						"other dispatch site")
			},
		},
		{
			name: "a late fire against a superseded command is dropped",
			setup: func(t *testing.T, def *model.ProcessDefinition) (InstanceState, string, string) {
				state, cmdID, timerID := driveToRetryBackoff(t, def, firedAt.Add(-time.Minute), retryOn)
				// The walk moved on under the timer: a different command is active now.
				cur := state.Compensating
				cur.ActiveCmdID = "i-comp-fail-cSUPERSEDED"
				state.Compensating = cur
				return state, cmdID, timerID
			},
			assert: func(t *testing.T, _ string, res StepResult, err error) {
				require.NoError(t, err)
				assert.Empty(t, res.Commands, "a late fire dispatches nothing")
				assert.Empty(t, timerRecordsOfKind(res.State, TimerCompensationRetry),
					"the stale record is dropped")
				assert.Equal(t, "i-comp-fail-cSUPERSEDED", res.State.Compensating.ActiveCmdID,
					"and the walk's own cursor is left alone")
			},
		},
		{
			name: "a fire on an instance that is no longer compensating is dropped",
			setup: func(t *testing.T, def *model.ProcessDefinition) (InstanceState, string, string) {
				state, cmdID, timerID := driveToRetryBackoff(t, def, firedAt.Add(-time.Minute), retryOn)
				// The walk finished under the timer: the cursor is the zero cursor and
				// the status is back to running. Indexing cursorRecords with the stale
				// NextIndex here is the ADR-0171 panic shape.
				state.Status = StatusRunning
				state.Compensating = compensationCursor{}
				return state, cmdID, timerID
			},
			assert: func(t *testing.T, _ string, res StepResult, err error) {
				require.NoError(t, err)
				assert.Empty(t, res.Commands)
				assert.Empty(t, timerRecordsOfKind(res.State, TimerCompensationRetry))
			},
		},
		{
			name: "a vanished record source routes to the walk's finish rather than panicking",
			setup: func(t *testing.T, _ *model.ProcessDefinition) (InstanceState, string, string) {
				// A cursor persisted before ADR-0171 (no pinned Records) whose scope has
				// since been closed: cursorRecords returns nothing while NextIndex says
				// 1. records[NextIndex] panics inside the pure core on this shape.
				state := InstanceState{
					InstanceID: "i-comp-retry-vanished",
					DefID:      "p-compensation-failure-visibility",
					DefVersion: 1,
					Status:     StatusCompensating,
					StartedAt:  firedAt,
					Compensating: compensationCursor{
						ScopeID:          "vanished-s1",
						ResumeNode:       "end",
						StartRecordCount: 2,
						NextIndex:        1,
						ActiveCmdID:      "vanished-c9",
						RetryAttempts:    1,
						RetryTimerID:     "vanished-t9",
					},
					Timers: []timerRecord{{
						TimerID:   "vanished-t9",
						Kind:      TimerCompensationRetry,
						NodeID:    "beta",
						ScopeID:   "vanished-s1",
						CommandID: "vanished-c9",
					}},
				}
				require.Empty(t, cursorRecords(&state, state.Compensating),
					"control: len(records)==0 while NextIndex==1 — the shape that panics")
				return state, "vanished-c9", "vanished-t9"
			},
			assert: func(t *testing.T, _ string, res StepResult, err error) {
				require.NoError(t, err)
				assert.Empty(t, invokeActionName(res.Commands),
					"there is nothing left to re-dispatch")
				assert.NotEqual(t, StatusCompensating, res.State.Status,
					"the walk is finished, not left wedged on a source that vanished")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := twoCompensableDef()
			state, failedCmdID, timerID := tc.setup(t, def)

			var res StepResult
			var err error
			require.NotPanics(t, func() {
				res, err = Step(t.Context(), def, state, NewTimerFired(firedAt, timerID), tc.opt)
			})
			tc.assert(t, failedCmdID, res, err)
		})
	}
}

// TestRedeliveredActionFailedDuringBackoffDoesNotDoubleArm pins the idempotency
// guard of ADR-0179 Decision 3.
//
// ⚠ Why the dispatched-id ring does NOT cover this: isBenignCompensationDuplicate
// carries a `!= ActiveCmdID` term, and must — without it every normal reply would
// be classified as a duplicate and the walk would never advance. A redelivered
// ActionFailed for the STILL-ACTIVE command is therefore let through to the
// short-circuit on purpose, and cur.RetryTimerID != "" is the only thing standing
// between it and a second incident, a second retry timer, a doubled attempt count
// and two timers dispatching the same record.
//
// What makes it fail without the guard: measured, the second delivery raised
// incidents 1→2, retry timer records 1→2 and RetryAttempts 1→2.
func TestRedeliveredActionFailedDuringBackoffDoesNotDoubleArm(t *testing.T) {
	at := time.Date(2026, 8, 17, 13, 0, 0, 0, time.UTC)
	def := twoCompensableDef()
	opt := StepOptions{
		CompensationRetryPolicy: &model.RetryPolicy{MaxAttempts: 5, InitialInterval: time.Second},
	}

	state, cmdID, timerID := driveToRetryBackoff(t, def, at, opt)
	require.Len(t, state.Incidents, 1, "control: the first failure raised exactly one incident")

	res, err := Step(t.Context(), def, state,
		NewActionFailed(at.Add(time.Second), cmdID, "undo-beta blew up", true), opt)
	require.NoError(t, err)

	assert.Empty(t, res.Commands, "a redelivery is a clean no-op: no second ScheduleTimer")
	assert.Len(t, res.State.Incidents, 1, "exactly one incident, not one per redelivery")
	assert.Len(t, timerRecordsOfKind(res.State, TimerCompensationRetry), 1,
		"exactly one armed backoff — two would dispatch the same record twice, the "+
			"double-refund hazard ADR-0034's post-acceptance fix exists to prevent")
	assert.Equal(t, timerID, res.State.Compensating.RetryTimerID, "the same backoff")
	assert.Equal(t, 1, res.State.Compensating.RetryAttempts,
		"one attempt increment, not one per redelivery")
	assert.Equal(t, cmdID, res.State.Compensating.ActiveCmdID)
}

// twoAttemptRetry is a policy allowing ONE retry per record: the budget check is
// RetryAttempts+1 < MaxAttempts, so a record that has already spent one attempt
// is exhausted.
func twoAttemptRetry() StepOptions {
	return StepOptions{
		CompensationRetryPolicy: &model.RetryPolicy{MaxAttempts: 2, InitialInterval: time.Second},
	}
}

// TestCompensationRetryExhaustionSkipsAndContinues pins ADR-0179 Decision 7: on
// exhaustion the walk SKIPS AND CONTINUES. It never parks — parking would
// reverse ADR-0034's safety argument that a failed compensation never strands
// the instance.
//
// What makes it fail before the budget check exists: measured, the second
// failure armed a SECOND backoff (`i-comp-fail-tm2`) and dispatched nothing, so
// undo-alpha was never reached and the record would retry forever.
func TestCompensationRetryExhaustionSkipsAndContinues(t *testing.T) {
	at := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	def := twoCompensableDef()
	opt := twoAttemptRetry()

	state, _, timerID := driveToRetryBackoff(t, def, at, opt)
	require.Equal(t, 1, state.Compensating.RetryAttempts,
		"control: the one permitted retry has been spent")

	fired, err := Step(t.Context(), def, state, NewTimerFired(at.Add(time.Second), timerID), opt)
	require.NoError(t, err)
	retryCmdID := invokeIDForAction(t, fired.Commands, "undo-beta")

	res, err := Step(t.Context(), def, fired.State,
		NewActionFailed(at.Add(2*time.Second), retryCmdID, "undo-beta blew up again", true), opt)
	require.NoError(t, err)

	assert.Equal(t, "undo-alpha", invokeActionName(res.Commands),
		"exhausted: the walk skips this record and CONTINUES to the next one")
	assert.Nil(t, scheduledTimerOfKind(res.Commands, TimerCompensationRetry),
		"no further backoff is armed once the budget is spent")
	assert.Equal(t, StatusCompensating, res.State.Status,
		"the walk does not park and does not terminate — it is still draining")
	assert.Equal(t, 0, res.State.Compensating.NextIndex, "it moved on to the alpha record")
	// ⚠ This asserted TWO incidents ("one per failed dispatch, both kept") when it
	// was written, one step before the incident lifecycle existed — which is the
	// bound ADR-0179 Decision 6 explicitly REFUSES ("one per exhausted record, not
	// one per attempt"). The re-dispatch now retires the superseded attempt's
	// incident, so what survives here is the LAST attempt's alone. See
	// TestCompensationFailedIncidentLifecycle, which pins all three directions.
	require.Len(t, res.State.Incidents, 1,
		"one incident per EXHAUSTED RECORD, not one per attempt (Decision 6)")
	assert.Equal(t, IncidentCompensationFailed, res.State.Incidents[0].Kind)
	assert.Equal(t, "undo-beta blew up again", res.State.Incidents[0].Error,
		"and it is the attempt that exhausted the budget, not the first one")
}

// TestCompensationRetryBudgetIsPerRecord pins the reset ADR-0179 Decision 3
// requires: RetryAttempts is zeroed wherever stepCompensationAdvance moves
// NextIndex — the only advance site in the package (the other two NextIndex
// writes, beginCompensation and startCompensationWalk, START a walk from a
// cursor stepCompensationFinish has already zeroed).
//
// Two failing records, each entitled to its own attempts. Without the reset the
// first poison record burns the budget and every later record gets zero retries:
// measured, alpha was skipped outright and the walk TERMINATED (status
// terminated, no backoff armed) instead of retrying it.
func TestCompensationRetryBudgetIsPerRecord(t *testing.T) {
	at := time.Date(2026, 8, 17, 15, 0, 0, 0, time.UTC)
	def := twoCompensableDef()
	opt := twoAttemptRetry()

	// Record 1 (beta): fail, retry, fail again → budget spent → skip to alpha.
	state, _, timerID := driveToRetryBackoff(t, def, at, opt)
	fired, err := Step(t.Context(), def, state, NewTimerFired(at.Add(time.Second), timerID), opt)
	require.NoError(t, err)
	betaRetryCmdID := invokeIDForAction(t, fired.Commands, "undo-beta")

	advanced, err := Step(t.Context(), def, fired.State,
		NewActionFailed(at.Add(2*time.Second), betaRetryCmdID, "undo-beta blew up again", true), opt)
	require.NoError(t, err)
	alphaCmdID := invokeIDForAction(t, advanced.Commands, "undo-alpha")
	require.Equal(t, 0, advanced.State.Compensating.NextIndex,
		"control: the walk must have advanced to the SECOND record")

	// Record 2 (alpha): its first failure must get its own retry.
	res, err := Step(t.Context(), def, advanced.State,
		NewActionFailed(at.Add(3*time.Second), alphaCmdID, "undo-alpha blew up", true), opt)
	require.NoError(t, err)

	st := scheduledTimerOfKind(res.Commands, TimerCompensationRetry)
	require.NotNil(t, st,
		"the SECOND record gets its own attempts — the first record's exhaustion "+
			"must not have spent them")
	assert.Equal(t, 1, res.State.Compensating.RetryAttempts,
		"one attempt spent on THIS record, counted from zero")
	assert.Equal(t, 0, res.State.Compensating.NextIndex, "still the alpha record")
	assert.Equal(t, StatusCompensating, res.State.Status)

	recs := timerRecordsOfKind(res.State, TimerCompensationRetry)
	require.Len(t, recs, 1)
	assert.Equal(t, "alpha", recs[0].NodeID, "the backoff guards the alpha record")
	assert.Equal(t, alphaCmdID, recs[0].CommandID)
}

// TestNoStallIncidentDuringACompensationRetryBackoff pins the CANCEL half of
// ADR-0179 Decision 3: the stall timer guarding the failed command is cancelled
// when the retry backoff is armed. Its job is done — the action replied.
//
// What makes it fail against the naive (arm-without-cancel) design: the stall
// record survives with CommandID still equal to the cursor's ActiveCmdID, so
// BOTH of handleCompensationStallFired's guards pass and it raises a FALSE
// "compensation action stalled" incident about an action that already replied —
// a visibility regression in the ADR whose headline is visibility. It also opens
// a CompensationEscape{Retry} race against the scheduled retry, since
// handleResolveCompensationStall accepts the same still-active command id.
//
// Measured on the naive design: after the failure the state carried 1 stall
// record and 1 retry record, and firing the stall timer produced
// incidents=[IncidentCompensationFailed IncidentCompensationStall].
func TestNoStallIncidentDuringACompensationRetryBackoff(t *testing.T) {
	at := time.Date(2026, 8, 17, 16, 0, 0, 0, time.UTC)
	def := twoCompensableDef()
	opt := StepOptions{
		CompensationRetryPolicy: &model.RetryPolicy{MaxAttempts: 5, InitialInterval: time.Second},
		CompensationStallAfter:  30 * time.Second,
	}

	state, cmdID := driveToCompensationWalk(t, def, at.Add(-time.Minute), opt)
	stalls := timerRecordsOfKind(state, TimerCompensationStall)
	require.Len(t, stalls, 1,
		"control: the walk must have armed a stall guard, or this test asserts nothing")
	stallTimerID := stalls[0].TimerID
	require.Equal(t, cmdID, stalls[0].CommandID,
		"control: the stall guard names the command that is about to fail — which is "+
			"what makes handleCompensationStallFired's late-fire guard pass")

	failed, err := Step(t.Context(), def, state,
		NewActionFailed(at, cmdID, "undo-beta blew up", true), opt)
	require.NoError(t, err)
	require.NotNil(t, scheduledTimerOfKind(failed.Commands, TimerCompensationRetry),
		"control: a backoff must be in flight, or there is no window to be wrong about")

	assert.Empty(t, timerRecordsOfKind(failed.State, TimerCompensationStall),
		"the stall guard for the failed command is retired: the action REPLIED")
	assert.Contains(t, failed.Commands, CancelTimer{TimerID: stallTimerID},
		"and the scheduler is told, or the job outlives the record it guarded")

	// Belt and braces: even if the scheduler delivers the cancelled stall timer
	// anyway, it must not manufacture a stall incident during a healthy backoff.
	fired, err := Step(t.Context(), def, failed.State,
		NewTimerFired(at.Add(time.Second), stallTimerID), opt)
	require.NoError(t, err)
	for _, inc := range fired.State.Incidents {
		assert.NotEqual(t, IncidentCompensationStall, inc.Kind,
			"an action that replied ActionFailed and is being retried is not STALLED")
	}
}

// TestArmedTimerVisibilityAcrossTheWalkScopedKinds is the mandatory end-to-end
// control ADR-0179 Decision 4 requires: with a REAL backoff armed by this
// package's own code, HasArmedTimers() must report true, so a harness drives the
// park instead of reporting ErrUnhandledPark.
//
// ⚠ It is a CONTROL, not a red-first test: the predicate split
// (firesOnDyingInstance / detectionOnly) shipped one step earlier, and P1-B could
// only exercise it on a hand-built timerRecord — it explicitly deferred the
// end-to-end case here. It is mutation-verified in the report instead (widen
// detectionOnly to cover TimerCompensationRetry — the exact mistake the pre-fold
// design made — and the first row goes red).
//
// The stall row is the other half of the split and is what makes the first row
// mean something: both kinds belong to the walk, and they answer this question
// differently.
func TestArmedTimerVisibilityAcrossTheWalkScopedKinds(t *testing.T) {
	at := time.Date(2026, 8, 17, 17, 0, 0, 0, time.UTC)
	stallAndRetry := StepOptions{
		CompensationRetryPolicy: &model.RetryPolicy{MaxAttempts: 5, InitialInterval: time.Second},
		CompensationStallAfter:  30 * time.Second,
	}

	type testCase struct {
		name   string
		setup  func(t *testing.T, def *model.ProcessDefinition) InstanceState
		assert func(t *testing.T, state InstanceState)
	}

	cases := []testCase{
		{
			name: "a live retry backoff is forward work the harness must fire",
			setup: func(t *testing.T, def *model.ProcessDefinition) InstanceState {
				state, _, _ := driveToRetryBackoff(t, def, at, stallAndRetry)
				return state
			},
			assert: func(t *testing.T, state InstanceState) {
				require.Len(t, timerRecordsOfKind(state, TimerCompensationRetry), 1,
					"control: the backoff is real, armed by armCompensationRetryTimer")
				assert.True(t, state.HasArmedTimers(),
					"a compensation-retry backoff is DRIVABLE: hiding it makes every "+
						"consumer who opts in get ErrUnhandledPark, and the shipped path "+
						"is never exercised by the harness this repo ships")

				var kinds []TimerKind
				for _, w := range state.TimerWaiters() {
					kinds = append(kinds, w.Kind)
				}
				assert.Contains(t, kinds, TimerCompensationRetry,
					"and it is enumerated unconditionally by TimerWaiters")
			},
		},
		{
			name: "a stall guard alone is detection only and stays hidden",
			setup: func(t *testing.T, def *model.ProcessDefinition) InstanceState {
				state, _ := driveToCompensationWalk(t, def, at.Add(-time.Minute), stallAndRetry)
				return state
			},
			assert: func(t *testing.T, state InstanceState) {
				require.Len(t, timerRecordsOfKind(state, TimerCompensationStall), 1,
					"control: a stall guard is armed and is the ONLY timer record")
				require.Empty(t, timerRecordsOfKind(state, TimerCompensationRetry))
				assert.False(t, state.HasArmedTimers(),
					"firing a detection deadline manufactures the very incident the "+
						"window exists to detect (ADR-0175) — walk-scoped is NOT the filter")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t, tc.setup(t, twoCompensableDef()))
		})
	}
}

// TestOperatorRetryDuringABackoffResetsTheRetryCursor is the regression guard for
// the code-review HIGH on ADR-0179: retryStalledCompensation (ADR-0175's `retry`
// verb) rewrote cur.ActiveCmdID and left BOTH new cursor fields naming the
// superseded attempt.
//
// What makes each row fail before the fix:
//
//   - "the retry cursor is reset": retryStalledCompensation writes ActiveCmdID
//     only, so RetryTimerID keeps naming the timer record its own
//     armCompensationStallTimer call has just swept out of s.Timers, and
//     RetryAttempts keeps the superseded attempt's count. Measured before:
//     RetryTimerID="i-comp-fail-tm1" RetryAttempts=1 with timers holding zero
//     records of that id.
//   - "the re-dispatched command can still fail": with RetryTimerID stale and
//     non-empty, handleActionFailed's idempotency guard answers the NEXT genuine
//     ActionFailed as a redelivery — no incident, no backoff, no advance — and the
//     instance stays StatusCompensating with nothing armed to move it. Measured
//     before: cmds=[] incidents=1 (the pre-existing one) timers=0.
func TestOperatorRetryDuringABackoffResetsTheRetryCursor(t *testing.T) {
	at := time.Date(2026, 8, 17, 18, 0, 0, 0, time.UTC)
	retryOn := StepOptions{
		CompensationRetryPolicy: &model.RetryPolicy{MaxAttempts: 5, InitialInterval: time.Second},
	}

	// operatorRetryDuringBackoff drives to a live backoff and then applies the
	// operator's `retry` verb against the still-active command.
	operatorRetryDuringBackoff := func(t *testing.T) (StepResult, *model.ProcessDefinition, string) {
		t.Helper()
		def := twoCompensableDef()
		state, failedCmdID, backoffTimerID := driveToRetryBackoff(t, def, at.Add(-time.Minute), retryOn)
		require.Len(t, timerRecordsOfKind(state, TimerCompensationRetry), 1,
			"control: a backoff record is genuinely armed before the operator intervenes")
		require.Equal(t, backoffTimerID, state.Compensating.RetryTimerID)

		res, err := Step(t.Context(), def, state,
			NewResolveCompensationStall(at, failedCmdID, "", CompensationRetry), retryOn)
		require.NoError(t, err)
		return res, def, invokeIDForAction(t, res.Commands, "undo-beta")
	}

	t.Run("the retry cursor is reset alongside ActiveCmdID", func(t *testing.T) {
		res, _, freshCmdID := operatorRetryDuringBackoff(t)

		cur := res.State.Compensating
		require.Equal(t, freshCmdID, cur.ActiveCmdID,
			"control: the verb re-dispatched under a fresh command id")
		assert.Empty(t, cur.RetryTimerID,
			"the backoff record was swept by armCompensationStallTimer's cancel half; "+
				"leaving the cursor naming it wedges handleActionFailed's idempotency guard")
		assert.Zero(t, cur.RetryAttempts,
			"the operator's retry is a fresh attempt at this record, not a continuation "+
				"of the budget the superseded dispatch had spent")
		assert.Empty(t, timerRecordsOfKind(res.State, TimerCompensationRetry),
			"control: no retry record survives for RetryTimerID to legitimately name")
	})

	t.Run("the re-dispatched command can still fail and be retried", func(t *testing.T) {
		res, def, freshCmdID := operatorRetryDuringBackoff(t)
		before := len(res.State.Incidents)

		failed, err := Step(t.Context(), def, res.State,
			NewActionFailed(at.Add(time.Minute), freshCmdID, "undo-beta blew up again", true), retryOn)
		require.NoError(t, err)

		assert.Len(t, failed.State.Incidents, before+1,
			"the second failure is a REAL failure, not a redelivery of the one the "+
				"operator superseded")
		st := scheduledTimerOfKind(failed.Commands, TimerCompensationRetry)
		require.NotNil(t, st, "and a fresh backoff is armed, so the walk still moves")
		assert.Equal(t, st.TimerID, failed.State.Compensating.RetryTimerID)
		assert.Equal(t, 1, failed.State.Compensating.RetryAttempts,
			"counted from the reset, so the per-record budget is not silently short")
	})
}

// TestOperatorRetryRetiresTheSupersededFailureIncident is the second half of the
// code-review HIGH on retryStalledCompensation: ADR-0175's `retry` verb retired
// only the STALL kind, because it was written before IncidentCompensationFailed
// existed and was never revisited when it did.
//
// ADR-0179 Decision 6 bounds the record at ONE PER EXHAUSTED RECORD, not one per
// attempt, and retryFailedCompensation upholds that by retiring the superseded
// attempt's incident as it re-dispatches. The operator verb re-dispatches the
// same record under a fresh command id — the identical "this attempt is
// superseded" event — so it owes the identical retirement. ADR-0175's verb has no
// cap, so without it the count grows without bound.
//
// What makes each row fail before the fix:
//
//   - "two operator retries": retryStalledCompensation calls only
//     retireCompensationStallIncidents, so each superseded attempt's
//     IncidentCompensationFailed is left open. Measured before: 3 incidents
//     (one per attempt) where Decision 6 promises 1.
//   - "the exhaustion record survives": passes before AND after — it is the
//     REGRESSION GUARD that the new retirement is scoped to the SUPERSEDED
//     command id and not to the kind. Retiring by kind, or retiring after the
//     cursor is overwritten, would delete the durable record of an
//     unrecoverable compensation, which is the one outcome ADR-0179 exists to
//     make visible.
func TestOperatorRetryRetiresTheSupersededFailureIncident(t *testing.T) {
	at := time.Date(2026, 8, 17, 19, 0, 0, 0, time.UTC)

	// failedIncidents returns the open IncidentCompensationFailed records, which is
	// the quantity Decision 6 bounds — a stall record coexisting would otherwise
	// inflate a plain len(Incidents).
	failedIncidents := func(s InstanceState) []Incident {
		var out []Incident
		for _, inc := range s.Incidents {
			if inc.Kind == IncidentCompensationFailed {
				out = append(out, inc)
			}
		}
		return out
	}

	// operatorRetry applies ADR-0175's `retry` verb against the walk's active
	// command and returns the state plus the FRESH command id it re-dispatched
	// under.
	operatorRetry := func(t *testing.T, def *model.ProcessDefinition, s InstanceState, when time.Time, opt StepOptions) (InstanceState, string) {
		t.Helper()
		res, err := Step(t.Context(), def, s,
			NewResolveCompensationStall(when, s.Compensating.ActiveCmdID, "", CompensationRetry), opt)
		require.NoError(t, err)
		return res.State, invokeIDForAction(t, res.Commands, "undo-beta")
	}

	// failActive fails the walk's active command.
	failActive := func(t *testing.T, def *model.ProcessDefinition, s InstanceState, cmdID string, when time.Time, opt StepOptions) InstanceState {
		t.Helper()
		res, err := Step(t.Context(), def, s,
			NewActionFailed(when, cmdID, "undo-beta blew up", true), opt)
		require.NoError(t, err)
		return res.State
	}

	t.Run("two operator retries leave exactly one open failure incident", func(t *testing.T) {
		opt := StepOptions{
			CompensationRetryPolicy: &model.RetryPolicy{MaxAttempts: 5, InitialInterval: time.Second},
		}
		def := twoCompensableDef()
		state, cmdID, _ := driveToRetryBackoff(t, def, at.Add(-time.Minute), opt)
		require.Len(t, failedIncidents(state), 1, "control: the first failure is on record")
		require.Equal(t, cmdID, failedIncidents(state)[0].CommandID)

		for i := range 2 {
			when := at.Add(time.Duration(i) * time.Minute)
			var fresh string
			state, fresh = operatorRetry(t, def, state, when, opt)
			assert.Empty(t, failedIncidents(state),
				"the superseded attempt's incident is retired AS the retry re-dispatches")
			state = failActive(t, def, state, fresh, when.Add(30*time.Second), opt)
			cmdID = fresh
		}

		open := failedIncidents(state)
		require.Len(t, open, 1,
			"ONE per record, not one per attempt — ADR-0179 Decision 6's bound, which "+
				"ADR-0175's uncapped verb would otherwise grow without limit")
		assert.Equal(t, cmdID, open[0].CommandID,
			"and it is the LATEST attempt's record, not a stale one")
	})

	t.Run("the exhaustion record survives the retries that preceded it", func(t *testing.T) {
		// MaxAttempts 2: the first failure of a record arms a backoff, the second
		// exhausts the budget and the walk skips and advances (Decision 7). The
		// operator retry in between zeroes RetryAttempts, so the budget genuinely
		// restarts — which is what makes the exhaustion happen on a command the
		// retirement has already had the chance to delete.
		opt := StepOptions{
			CompensationRetryPolicy: &model.RetryPolicy{MaxAttempts: 2, InitialInterval: time.Second},
		}
		def := twoCompensableDef()
		state, _, _ := driveToRetryBackoff(t, def, at.Add(-time.Minute), opt)

		state, afterRetry := operatorRetry(t, def, state, at, opt)
		require.Empty(t, failedIncidents(state), "control: the superseded record is gone")

		// Attempt 1 of the restarted budget: arms a backoff, does NOT advance.
		state = failActive(t, def, state, afterRetry, at.Add(time.Minute), opt)
		require.Len(t, failedIncidents(state), 1)
		require.NotEmpty(t, state.Compensating.RetryTimerID, "control: a backoff is armed")

		// Fire it, then fail again: RetryAttempts is now 1, so 1+1 >= MaxAttempts
		// and armCompensationRetryTimer declines. The walk skips and continues.
		fired, err := Step(t.Context(), def, state,
			NewTimerFired(at.Add(2*time.Minute), state.Compensating.RetryTimerID), opt)
		require.NoError(t, err)
		state = fired.State
		exhaustedCmdID := invokeIDForAction(t, fired.Commands, "undo-beta")

		res, err := Step(t.Context(), def, state,
			NewActionFailed(at.Add(3*time.Minute), exhaustedCmdID, "undo-beta blew up", true), opt)
		require.NoError(t, err)

		assert.Equal(t, "undo-alpha", invokeActionName(res.Commands),
			"control: the budget is spent, so the walk SKIPS the record and continues")
		open := failedIncidents(res.State)
		require.Len(t, open, 1,
			"exactly one durable record of an unrecoverable compensation survives")
		assert.Equal(t, exhaustedCmdID, open[0].CommandID,
			"raised by the FINAL failure — the retirement must be scoped to the "+
				"superseded command id, never to the kind")
		assert.Equal(t, "beta", open[0].NodeID)
	})
}

// cancelledTimerIDs returns the TimerID of every CancelTimer in cmds.
//
// The sibling engine_test file has a cancelTimerIDs of its own; this is the
// white-box twin, needed here because these tests read the unexported cursor in
// the same breath (engine/ mixes package engine and package engine_test).
func cancelledTimerIDs(cmds []Command) []string {
	var out []string
	for _, c := range cmds {
		if ct, ok := c.(CancelTimer); ok {
			out = append(out, ct.TimerID)
		}
	}
	return out
}

// TestStallVerbsDisposeOfALiveRetryBackoff pins the interaction class the delivery
// gate found a HIGH in: an ADR-0175 operator verb applied DURING an ADR-0179
// retry backoff. `retry` is covered by the two tests above; this is `skip` and
// `abandon`, the two verbs no phase of ADR-0179 ever looked at.
//
// ⚠ These verbs are correct INCIDENTALLY, not by design. Neither owns any cursor
// or timer logic: `skip` delegates to stepCompensationAdvance and `abandon` to
// stepCompensationFinish, and ADR-0179 happened to revise both. Nothing else pins
// that, and the gate's own finding was that the ONE verb path with bespoke
// handling (retryStalledCompensation) was never revisited when the ADR landed. So
// the property under test is not "the verb does the right thing", it is "the
// shared function it leans on still zeroes the cursor and cancels the backoff" —
// which is why the mutations verifying these rows break the SHARED functions and
// not the verb entry points.
//
// ⚠ Both rows assert the CancelTimer COMMAND, not merely that the record left
// s.Timers. A record dropped without its command leaves the scheduler holding a
// job armed against a walk that has moved on — a state assertion alone cannot
// tell the two apart, and that exact gap shipped as a HIGH on a sibling path.
//
// ⚠⚠ THE TWO ROWS ARE NOT EQUALLY STRONG, and the difference is measured, not
// assumed. Mutation matrix (each mutation applied to the SHARED function, alone
// unless combined):
//
//	mutation                                                     skip  abandon
//	A  drop the cursor zeroing in stepCompensationAdvance         RED   green
//	B  narrow cancelCompensationWalkTimers to the stall kind      RED   green
//	C  leak the retry fields through stepCompensationFinish       green green ⚠
//	D  leak the retry fields through endInstance                  green green ⚠
//	C+D                                                           green RED
//	E  make cancelAllTimers skip TimerCompensationRetry           green green ⚠
//	B+E                                                           RED   RED
//
// The SKIP row is single-site discriminating: each property it asserts has
// exactly one guarantor, and breaking that guarantor reds it.
//
// The ABANDON row is NOT. Both properties it asserts are guaranteed TWICE on the
// terminate path — stepCompensationFinish zeroes the cursor and cancels
// walk-scoped timers, then endInstance zeroes the cursor again and cancels every
// remaining timer. The assertions are genuinely falsifiable (C+D and B+E both red
// them, so they are not vacuous), but no SINGLE-site regression can red them.
// Recorded rather than papered over: whoever changes stepCompensationFinish alone
// must not read this row staying green as evidence that abandon is unaffected.
// The redundancy is real defence in depth, not a bug — it is the row's
// sensitivity that is limited, and only on the terminate path.
func TestStallVerbsDisposeOfALiveRetryBackoff(t *testing.T) {
	at := time.Date(2026, 8, 17, 21, 0, 0, 0, time.UTC)
	retryOn := StepOptions{
		CompensationRetryPolicy: &model.RetryPolicy{MaxAttempts: 5, InitialInterval: time.Second},
	}

	type testCase struct {
		name   string
		verb   func(at time.Time, commandID, incidentID string) ResolveCompensationStall
		assert func(t *testing.T, failedCmdID, backoffTimerID string, res StepResult)
	}

	cases := []testCase{
		{
			name: "skip advances the walk and cancels the backoff it was parked on",
			verb: NewSkipStalledCompensation,
			assert: func(t *testing.T, failedCmdID, backoffTimerID string, res StepResult) {
				assert.Contains(t, cancelledTimerIDs(res.Commands), backoffTimerID,
					"the SCHEDULER must be told: dropping the record without the command "+
						"leaves a job armed against a walk that has moved on")
				assert.Empty(t, timerRecordsOfKind(res.State, TimerCompensationRetry),
					"and the record is gone from the snapshot too")

				cur := res.State.Compensating
				assert.Empty(t, cur.RetryTimerID,
					"zeroed by stepCompensationAdvance — a stale value would make the NEXT "+
						"record's first genuine failure look like a redelivery")
				assert.Zero(t, cur.RetryAttempts,
					"the budget is PER RECORD, and skip moves to a new one")

				assert.Equal(t, StatusCompensating, res.State.Status, "the walk goes on")
				assert.Equal(t, "undo-alpha", invokeActionName(res.Commands),
					"skip takes the byte-identical path a returned ActionFailed takes")
				assert.Equal(t, 0, cur.NextIndex, "the cursor really did move off the record")
				assert.NotEqual(t, failedCmdID, cur.ActiveCmdID, "under a fresh command id")

				require.Len(t, res.State.Incidents, 1)
				assert.Equal(t, IncidentCompensationFailed, res.State.Incidents[0].Kind)
				assert.Equal(t, failedCmdID, res.State.Incidents[0].CommandID,
					"⚠ the skipped record's failure incident SURVIVES, by design: this is "+
						"the exhaustion/skip route where the incident is the durable evidence "+
						"(ADR-0179 Decision 6/7), unlike the retry routes that supersede it")
			},
		},
		{
			name: "abandon terminates the instance and cancels the backoff",
			verb: NewAbandonCompensationWalk,
			assert: func(t *testing.T, failedCmdID, backoffTimerID string, res StepResult) {
				assert.Contains(t, cancelledTimerIDs(res.Commands), backoffTimerID,
					"a terminating walk must not leave the scheduler holding its backoff")
				assert.Empty(t, timerRecordsOfKind(res.State, TimerCompensationRetry))

				assert.Equal(t, StatusTerminated, res.State.Status)
				cur := res.State.Compensating
				assert.Empty(t, cur.ActiveCmdID, "stepCompensationFinish zeroes the whole cursor")
				assert.Empty(t, cur.RetryTimerID, "including both ADR-0179 fields")
				assert.Zero(t, cur.RetryAttempts)

				assert.Empty(t, invokeActionName(res.Commands),
					"abandon dispatches no further compensation")

				require.Len(t, res.State.Incidents, 1)
				assert.Equal(t, IncidentCompensationFailed, res.State.Incidents[0].Kind,
					"the failure record survives the abandon, consistent with skip; runtime's "+
						"causeOfDeathIncident allow-list admits IncidentAction only, so it is "+
						"never published as the cause of death")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := twoCompensableDef()
			state, failedCmdID, backoffTimerID := driveToRetryBackoff(t, def, at.Add(-time.Minute), retryOn)
			require.Len(t, timerRecordsOfKind(state, TimerCompensationRetry), 1,
				"control: the verb really is applied to a LIVE backoff")

			res, err := Step(t.Context(), def, state,
				tc.verb(at, failedCmdID, ""), retryOn)
			require.NoError(t, err)

			tc.assert(t, failedCmdID, backoffTimerID, res)
		})
	}
}
