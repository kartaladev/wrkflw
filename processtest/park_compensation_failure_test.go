package processtest_test

// ADR-0179 — a WALK-SCOPED incident must not suppress a park the harness can
// still legitimately drive forward.
//
// The engine now raises an engine.IncidentCompensationFailed on every failed
// compensation action, for every consumer, with the retry policy switched OFF.
// Because Classify ranks its incident rung above its timer rung, a single such
// record used to flip an otherwise ordinary timer park from "timer" to
// "incident", at which point AutoTimers() stopped acting on it and drive
// reported ErrUnhandledPark.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/processtest"
)

// armedTimerToken returns a token parked on a plain timer intermediate catch.
// AwaitTimer is what makes engine.InstanceState.HasArmedTimers report true from a
// hand-built literal (the token source of TimerWaiters, ADR-0177), and the
// matching AwaitCommand is what makes that timer the PRIMARY park rather than a
// secondary attachment — see processtest.primaryTimerPark.
func armedTimerToken() engine.Token {
	return engine.Token{
		ID:           "tok-timer",
		NodeID:       "wait-timer",
		State:        engine.TokenWaiting,
		AwaitTimer:   "tm-1",
		AwaitCommand: "tm-1",
	}
}

// walkScopedIncident returns an incident of kind with the shape both compensation
// kinds share: TokenID empty (the walk owns it, no token does) and keyed by the
// compensation CommandID.
func walkScopedIncident(kind engine.IncidentKind) engine.Incident {
	return engine.Incident{
		ID:        "inc-walk",
		Kind:      kind,
		NodeID:    "charge",
		CommandID: "i1-c3",
		Error:     "refund declined",
	}
}

// compensableSagaDefinition is the two-record saga both engine-built fixtures
// below drive: a → b, each with a compensation action, then a receive task so the
// instance is still alive when the walk starts.
func compensableSagaDefinition(t *testing.T, id string) *model.ProcessDefinition {
	t.Helper()

	def, err := definition.NewBuilder(id, 1).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("a", activity.WithTaskAction("doA"), activity.WithCompensateAction("undoA"))).
		Add(activity.NewServiceTask("b", activity.WithTaskAction("doB"), activity.WithCompensateAction("undoB"))).
		Add(activity.NewReceiveTask("wait", "go")).
		Add(event.NewEnd("end")).
		Connect("start", "a").
		Connect("a", "b").
		Connect("b", "wait").
		Connect("wait", "end").
		Build()
	require.NoError(t, err)
	return def
}

// compensationWalkToDispatch drives def to a cancel-started compensation walk
// under opt and returns the step whose commands carry the walk's first
// compensation InvokeAction.
func compensationWalkToDispatch(t *testing.T, def *model.ProcessDefinition, at time.Time, opt engine.StepOptions) engine.StepResult {
	t.Helper()

	res, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), opt)
	require.NoError(t, err)
	for i, name := range []string{"doA", "doB"} {
		cmdID := invokedCommandID(t, res.Commands, name)
		res, err = engine.Step(t.Context(), def, res.State,
			engine.NewActionCompleted(at.Add(time.Duration(i+1)*time.Second), cmdID, nil), opt)
		require.NoError(t, err)
	}

	res, err = engine.Step(t.Context(), def, res.State, engine.NewCancelRequested(at.Add(time.Minute)), opt)
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, res.State.Status, "control: a walk is in flight")
	return res
}

// compensationStallDetectedState drives the saga to a walk whose dispatched
// compensation action goes silent and whose stall window then elapses, returning
// the snapshot carrying an open, walk-scoped engine.IncidentCompensationStall and
// nothing else to park on.
func compensationStallDetectedState(t *testing.T) engine.InstanceState {
	t.Helper()

	def := compensableSagaDefinition(t, "stall-detected-park")
	at := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	opt := engine.StepOptions{CompensationStallAfter: 30 * time.Minute}

	res := compensationWalkToDispatch(t, def, at, opt)
	waiters := res.State.TimerWaiters()
	require.Len(t, waiters, 1, "control: the walk armed exactly one timer")
	require.Equal(t, engine.TimerCompensationStall, waiters[0].Kind)

	res, err := engine.Step(t.Context(), def, res.State,
		engine.NewTimerFired(at.Add(time.Hour), waiters[0].TimerID), opt)
	require.NoError(t, err)

	state := res.State
	require.Len(t, state.Incidents, 1, "control: the stall window really did elapse")
	require.Equal(t, engine.IncidentCompensationStall, state.Incidents[0].Kind)
	require.Empty(t, state.Incidents[0].TokenID, "control: the incident is walk-scoped")
	require.False(t, state.HasArmedTimers(), "control: nothing is left for the harness to fire")
	return state
}

// compensationFailureUnderStallGuardState drives the saga to a walk whose first
// compensation action FAILS with no retry policy configured, while stall
// detection is ON. The walk then skips and continues (ADR-0034 Decision 4), so
// the returned snapshot carries the walk-scoped
// engine.IncidentCompensationFailed for the record just abandoned AND a freshly
// armed engine.TimerCompensationStall for the record now in flight.
//
// It is the row where the two predicates interact: an armed timer exists, but its
// kind is detection-only, so engine.InstanceState.HasArmedTimers reports false and
// the incident rung must still fire.
func compensationFailureUnderStallGuardState(t *testing.T) engine.InstanceState {
	t.Helper()

	def := compensableSagaDefinition(t, "failure-under-stall-guard")
	at := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	opt := engine.StepOptions{CompensationStallAfter: 30 * time.Minute}

	res := compensationWalkToDispatch(t, def, at, opt)
	undoB := invokedCommandID(t, res.Commands, "undoB")
	res, err := engine.Step(t.Context(), def, res.State,
		engine.NewActionFailed(at.Add(2*time.Minute), undoB, "refund declined", false), opt)
	require.NoError(t, err)

	state := res.State
	require.Len(t, state.Incidents, 1, "control: the failure raised exactly one incident")
	require.Equal(t, engine.IncidentCompensationFailed, state.Incidents[0].Kind)
	require.Empty(t, state.Incidents[0].TokenID, "control: the incident is walk-scoped")

	waiters := state.TimerWaiters()
	require.Len(t, waiters, 1, "control: a timer IS armed — the next record's stall guard")
	require.Equal(t, engine.TimerCompensationStall, waiters[0].Kind)
	require.False(t, state.HasArmedTimers(),
		"control: but the stall kind is detection-only, so nothing here is firable")
	return state
}

// compensationRetryBackoffState drives a two-record saga to a cancel-started
// compensation walk whose first compensation action FAILS under a retry policy,
// returning the mid-backoff snapshot: an open walk-scoped
// engine.IncidentCompensationFailed plus an armed engine.TimerCompensationRetry
// and no tokens at all.
//
// Built by the ENGINE rather than hand-assembled because timerRecord is
// unexported: a consumer package cannot fabricate a compensation-retry record,
// which is precisely why the harness has to read the engine's own
// HasArmedTimers.
func compensationRetryBackoffState(t *testing.T) engine.InstanceState {
	t.Helper()

	def := compensableSagaDefinition(t, "retry-backoff-park")
	at := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	opt := engine.StepOptions{
		CompensationRetryPolicy: &model.RetryPolicy{MaxAttempts: 5, InitialInterval: time.Minute},
	}

	res := compensationWalkToDispatch(t, def, at, opt)
	// The walk runs in reverse order, so undoB is the record now in flight. Fail it
	// retryably: that is what arms the backoff.
	undoB := invokedCommandID(t, res.Commands, "undoB")
	res, err := engine.Step(t.Context(), def, res.State,
		engine.NewActionFailed(at.Add(2*time.Minute), undoB, "refund declined", true), opt)
	require.NoError(t, err)

	state := res.State
	require.Len(t, state.Incidents, 1, "control: the failure raised exactly one incident")
	require.Equal(t, engine.IncidentCompensationFailed, state.Incidents[0].Kind)
	require.Empty(t, state.Incidents[0].TokenID, "control: the incident is walk-scoped")
	require.True(t, state.HasArmedTimers(), "control: the retry backoff is armed and firable")
	require.Empty(t, state.Tokens, "control: a walk holds no token of its own")
	return state
}

// invokedCommandID returns the CommandID of the InvokeAction for the named action
// in cmds, failing the test when the action was not invoked at all.
func invokedCommandID(t *testing.T, cmds []engine.Command, name string) string {
	t.Helper()

	for _, c := range cmds {
		if invoke, ok := c.(engine.InvokeAction); ok && invoke.Name == name {
			return invoke.CommandID
		}
	}
	require.FailNowf(t, "action not invoked", "no InvokeAction for %q", name)
	return ""
}

// TestClassifyWalkScopedIncidentDoesNotSuppressDrivableParks pins the rung
// PREDICATE ADR-0179 changes: the incident rung fires only for an incident that
// genuinely parks the instance — a token in engine.TokenIncident, or an incident
// naming a token — so a walk-scoped record falls through to the remaining rungs
// instead of masking them.
//
// The rung ORDER is deliberately untouched: a token-parked engine.IncidentAction
// must still outrank an armed timer, which is what the third and fourth rows
// assert (both fixtures carry an armed timer, so the row can discriminate the
// order at all).
func TestClassifyWalkScopedIncidentDoesNotSuppressDrivableParks(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		build  func(t *testing.T) engine.InstanceState
		assert func(t *testing.T, p processtest.Park)
	}

	cases := []testCase{
		{
			name: "walk-scoped compensation-failed incident yields to an armed timer",
			build: func(*testing.T) engine.InstanceState {
				return engine.InstanceState{
					Status:    engine.StatusRunning,
					Incidents: []engine.Incident{walkScopedIncident(engine.IncidentCompensationFailed)},
					Tokens:    []engine.Token{armedTimerToken()},
				}
			},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonTimer, p.Reason)
				assert.Equal(t, "wait-timer", p.Node)
				// The visibility surface is untouched: which rung fires changed,
				// what Park reports did not.
				require.Len(t, p.Incidents, 1)
				assert.Equal(t, engine.IncidentCompensationFailed, p.Incidents[0].Kind)

				d, err := processtest.AutoTimers()(t.Context(), p)
				require.NoError(t, err)
				assert.Equal(t, processtest.AdvanceTimers(), d,
					"the shipped timer handler must still drive this park")
			},
		},
		{
			name: "walk-scoped compensation-stall incident yields to an armed timer",
			build: func(*testing.T) engine.InstanceState {
				return engine.InstanceState{
					Status:    engine.StatusRunning,
					Incidents: []engine.Incident{walkScopedIncident(engine.IncidentCompensationStall)},
					Tokens:    []engine.Token{armedTimerToken()},
				}
			},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonTimer, p.Reason,
					"the pre-existing walk-scoped kind has the identical shape")

				d, err := processtest.AutoTimers()(t.Context(), p)
				require.NoError(t, err)
				assert.Equal(t, processtest.AdvanceTimers(), d)
			},
		},
		{
			name: "an incident naming a token still outranks an armed timer",
			build: func(*testing.T) engine.InstanceState {
				return engine.InstanceState{
					Status: engine.StatusRunning,
					Incidents: []engine.Incident{{
						ID:      "inc-action",
						Kind:    engine.IncidentAction,
						TokenID: "tok-failed",
						NodeID:  "call-api",
						Error:   "boom",
					}},
					Tokens: []engine.Token{armedTimerToken()},
				}
			},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonIncident, p.Reason,
					"REGRESSION GUARD: reordering the rungs instead of changing the "+
						"predicate would let any armed timer mask an operator-actionable incident")
				assert.Equal(t, "call-api", p.Node)
				assert.True(t, p.HasArmedTimers, "the fixture really does carry an armed timer")

				d, err := processtest.AutoTimers()(t.Context(), p)
				require.NoError(t, err)
				assert.Equal(t, processtest.Pass(), d,
					"and the timer handler must not fire that timer")
			},
		},
		{
			name: "a walk-scoped incident ahead of a token-scoped one does not steal the node",
			build: func(*testing.T) engine.InstanceState {
				return engine.InstanceState{
					Status: engine.StatusRunning,
					Incidents: []engine.Incident{
						walkScopedIncident(engine.IncidentCompensationFailed),
						{
							ID:      "inc-action",
							Kind:    engine.IncidentAction,
							TokenID: "tok-failed",
							NodeID:  "call-api",
							Error:   "boom",
						},
					},
					Tokens: []engine.Token{armedTimerToken()},
				}
			},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonIncident, p.Reason)
				assert.Equal(t, "call-api", p.Node,
					"Node must name the incident that RAISED the reason, not slice position 0")
				require.Len(t, p.Incidents, 2, "both are still reported")
			},
		},
		{
			// Precedence between the two things that can name the node: a token
			// genuinely stuck is the actionable one, so it beats the walk record
			// even though the walk record is at Incidents[0].
			name: "a stuck token names the node ahead of a walk-scoped incident",
			build: func(*testing.T) engine.InstanceState {
				return engine.InstanceState{
					Status:    engine.StatusRunning,
					Incidents: []engine.Incident{walkScopedIncident(engine.IncidentCompensationFailed)},
					Tokens: []engine.Token{
						{ID: "tok-inc", NodeID: "call-api", State: engine.TokenIncident},
					},
				}
			},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonIncident, p.Reason)
				assert.Equal(t, "call-api", p.Node,
					"the stuck token, not the walk record sitting at Incidents[0]")
			},
		},
		{
			name: "a token parked in the incident state still outranks an armed timer",
			build: func(*testing.T) engine.InstanceState {
				return engine.InstanceState{
					Status: engine.StatusRunning,
					Tokens: []engine.Token{
						armedTimerToken(),
						{ID: "tok-inc", NodeID: "call-api", State: engine.TokenIncident},
					},
				}
			},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonIncident, p.Reason)
				assert.Equal(t, "call-api", p.Node)
				assert.True(t, p.HasArmedTimers, "the fixture really does carry an armed timer")
			},
		},
		{
			name: "a walk-scoped incident with no firable timer keeps the incident rung",
			build: func(*testing.T) engine.InstanceState {
				return engine.InstanceState{
					Status:    engine.StatusCompensating,
					Incidents: []engine.Incident{walkScopedIncident(engine.IncidentCompensationFailed)},
				}
			},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonIncident, p.Reason)
				assert.Equal(t, "charge", p.Node,
					"and it names the walk-scoped incident's own node")
				require.Len(t, p.Incidents, 1)
			},
		},
		{
			// ⚠ THE ROW WHERE THE TWO PREDICATES INTERACT. A timer IS armed here —
			// the next record's stall guard — but engine.TimerKind.detectionOnly
			// keeps it out of HasArmedTimers, so the "no firable timer" disjunct
			// holds and the incident rung fires. Confirmed by execution, not by
			// reasoning: the fixture asserts TimerWaiters() has exactly one
			// TimerCompensationStall entry while HasArmedTimers() is false.
			name:  "an armed STALL timer is not a firable timer, so the incident rung still fires",
			build: compensationFailureUnderStallGuardState,
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonIncident, p.Reason)
				assert.False(t, p.HasArmedTimers)
				require.Len(t, p.Incidents, 1)
				assert.Equal(t, engine.IncidentCompensationFailed, p.Incidents[0].Kind)

				d, err := processtest.AutoTimers()(t.Context(), p)
				require.NoError(t, err)
				assert.Equal(t, processtest.Pass(), d,
					"and the harness must not fire a detection deadline by itself")
			},
		},
		{
			// ADR-0175 PRESERVED. Its spec records "processtest.Classify reports
			// ReasonIncident where it reported ReasonUnknown" as an intended
			// consequence, and ReasonIncident's own doc told consumers to handle a
			// stall there. An earlier revision of this rung regressed it to
			// reason="unknown" node="" (measured); the "no firable timer" disjunct
			// is what keeps that promise.
			name:  "a stall-detected compensation walk park still classifies as an incident",
			build: compensationStallDetectedState,
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonIncident, p.Reason)
				assert.Equal(t, "b", p.Node,
					"and names the node the stalled record was compensating")
				require.Len(t, p.Incidents, 1)
				assert.Equal(t, engine.IncidentCompensationStall, p.Incidents[0].Kind)
				assert.False(t, p.HasArmedTimers,
					"control: the detection timer is spent, so no rung below could have matched")
			},
		},
		{
			name:  "an engine-built compensation-retry backoff parks as a timer",
			build: compensationRetryBackoffState,
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonTimer, p.Reason)
				assert.True(t, p.HasArmedTimers)
				require.Len(t, p.Incidents, 1)
				assert.Equal(t, engine.IncidentCompensationFailed, p.Incidents[0].Kind)

				d, err := processtest.AutoTimers()(t.Context(), p)
				require.NoError(t, err)
				assert.Equal(t, processtest.AdvanceTimers(), d,
					"the backoff is forward work: the harness must fire it for the walk to resume")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, processtest.Classify(tc.build(t)))
		})
	}
}

// TestHarnessDrivesPastAFailedCompensationAction is the end-to-end form, driven
// through the shipped Harness and the shipped AutoTimers recipe with NO retry
// policy configured — which is the point. ADR-0179's incident is raised for every
// consumer whether or not they opt into compensation retry, so this is the shape
// that broke existing consumer harnesses.
//
// The definition compensates a completed activity mid-process and then parks on
// an ordinary timer catch. The compensation action fails, the walk skips and
// continues (ADR-0034 Decision 4), the process resumes, and the instance is left
// parked on a timer while carrying one walk-scoped incident.
func TestHarnessDrivesPastAFailedCompensationAction(t *testing.T) {
	t.Parallel()

	def, err := definition.NewBuilder("compensation-failure-then-timer", 1).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("charge",
			activity.WithTaskAction("chargeCard"),
			activity.WithCompensateAction("refundCard"))).
		Add(event.NewCompensateThrow("undo")).
		Add(event.NewIntermediateCatch("wait", event.WithCatchTimer(schedule.AfterExpr(`"1h"`)))).
		Add(event.NewEnd("end")).
		Connect("start", "charge").
		Connect("charge", "undo").
		Connect("undo", "wait").
		Connect("wait", "end").
		Build()
	require.NoError(t, err)

	h, err := processtest.New(
		processtest.WithCatalogActionFunc("chargeCard", func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"charged": true}, nil
		}),
		processtest.WithCatalogActionFunc("refundCard", func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return nil, errors.New("refund declined")
		}),
	)
	require.NoError(t, err)

	_, err = h.Start(t.Context(), def, "inst-comp-fail", nil)
	require.NoError(t, err)

	final, err := h.DriveToCompletion(t.Context(), def, "inst-comp-fail", processtest.AutoTimers())
	require.NoError(t, err, "a walk-scoped incident must not make an ordinary timer park unhandled")
	assert.Equal(t, engine.StatusCompleted, final.Status)

	require.NotEmpty(t, final.Incidents,
		"control: the run really did raise the walk-scoped incident this test is about")
	assert.Equal(t, engine.IncidentCompensationFailed, final.Incidents[0].Kind)
	assert.Empty(t, final.Incidents[0].TokenID)
}

// walkScopedIncidentWithCommandWaitState drives a service task carrying a timer
// BOUNDARY to the point its token parks on AwaitCommand, then stamps a
// walk-scoped incident onto the snapshot. It is the finding-3 shape: an armed
// timer exists and engine.InstanceState.HasArmedTimers reports true, but the
// timer is a SECONDARY attachment so processtest.primaryTimerPark rejects it and
// the timer rung declines the park.
//
// Engine-built rather than hand-assembled: the boundary arm is what makes
// HasArmedTimers true without any token carrying AwaitTimer, and that
// combination is not reachable from a literal (the arm slices on
// engine.InstanceState have unexported element types).
func walkScopedIncidentWithCommandWaitState(t *testing.T) engine.InstanceState {
	t.Helper()

	def, err := definition.NewBuilder("walk-incident-command-wait", 1).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("svc", activity.WithTaskAction("work"))).
		Add(event.NewBoundary("bnd", "svc", event.WithBoundaryTimer(schedule.AfterExpr(`"3h"`)))).
		Add(event.NewEnd("end")).
		Add(event.NewEnd("timed-out")).
		Connect("start", "svc").
		Connect("svc", "end").
		Connect("bnd", "timed-out").
		Build()
	require.NoError(t, err)

	res, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-cmdwait"},
		engine.NewStartInstance(time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC), nil),
		engine.StepOptions{})
	require.NoError(t, err)

	state := res.State
	require.True(t, state.HasArmedTimers(), "control: the boundary arm IS a firable timer")
	require.Len(t, state.Tokens, 1)
	require.NotEmpty(t, state.Tokens[0].AwaitCommand, "control: the token parks on the action")
	state.Incidents = append(state.Incidents, walkScopedIncident(engine.IncidentCompensationFailed))
	return state
}

// TestClassifyWalkScopedIncidentIsTheLowestRung is the truth table for the rung
// SPLIT that closes the two code-review MEDIUMs on ADR-0179.
//
// The shipped rung was one predicate carrying a HasArmedTimers yield term, and
// its POSITION — above signal, message, timer and async-child — was the defect
// the yield term was patching around. It is now two rungs: a TOKEN-SCOPED
// incident keeps the high position it has always had, and a WALK-SCOPED one sits
// immediately above ReasonUnknown, below every reason a harness can actually act
// on. The yield term is gone; with the walk-scoped rung placed last there is
// nothing left for it to yield to.
//
// What makes each row fail before the split, measured on the shipped predicate
// (`hasIncidentToken || tokenScopedIncident != nil || (len(Incidents) > 0 && !HasArmedTimers)`):
//
//   - "a signal park": reason=incident node="charge". A resumed instance (a
//     throw-targeted or partial rollback) keeps its walk-scoped record forever —
//     nothing retires it — so a healthy Running instance parked on a signal
//     classified as an incident a Reason-switching harness has no verb for, and
//     drive reported ErrUnhandledPark.
//   - "a message park": reason=incident node="charge", identically.
//   - "a command wait with NO armed timer": reason=incident node="charge".
//   - The three "still" rows and the human-task row pass before AND after: they
//     are the regression guards that the split moved only what it meant to move.
//     The stall row in particular is ADR-0175's recorded consequence (c) and is
//     why the walk-scoped rung sits ABOVE ReasonUnknown rather than being deleted.
//   - "a command wait WITH a secondary timer" is a CHARACTERIZATION row, not a
//     red one: measured reason=async-child before the split as well. Finding 3 is
//     a coherence defect — the yield term said HasArmedTimers while the timer rung
//     additionally required primaryTimerPark||!hasCommandWait, so the incident
//     yielded to a timer that then declined the park — and the outcome it produced
//     by accident is the one the split produces by construction. The row pins that
//     it stays true once the accident is removed.
func TestClassifyWalkScopedIncidentIsTheLowestRung(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		build  func(t *testing.T) engine.InstanceState
		assert func(t *testing.T, p processtest.Park)
	}

	cases := []testCase{
		{
			name:  "a walk-scoped incident beside an armed RETRY timer parks as a timer",
			build: compensationRetryBackoffState,
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonTimer, p.Reason,
					"the backoff is forward work; the incident is only the record of why")
				require.Len(t, p.Incidents, 1)
			},
		},
		{
			// ADR-0175 consequence (c), and the reason the walk-scoped rung sits
			// ABOVE ReasonUnknown rather than being deleted outright: with nothing
			// else parked, the incident IS the most informative thing to report, and
			// ReasonUnknown would be a silent retraction of what ADR-0175 shipped.
			name:  "a stalled walk with nothing else parked still classifies as an incident",
			build: compensationStallDetectedState,
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonIncident, p.Reason)
				assert.Equal(t, "b", p.Node, "naming the record the walk stalled on")
			},
		},
		{
			// THE FORBIDDEN REORDER. Moving the token-scoped half down with the
			// walk-scoped half would let any armed timer paper over a token the
			// harness must resolve with ResolveIncident.
			name: "a token-parked incident still outranks an armed timer",
			build: func(*testing.T) engine.InstanceState {
				return engine.InstanceState{
					Status: engine.StatusRunning,
					Incidents: []engine.Incident{{
						ID: "inc-action", Kind: engine.IncidentAction,
						TokenID: "tok-failed", NodeID: "call-api", Error: "boom",
					}},
					Tokens: []engine.Token{armedTimerToken()},
				}
			},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonIncident, p.Reason)
				assert.Equal(t, "call-api", p.Node)
				assert.True(t, p.HasArmedTimers, "control: a timer really is armed to be masked")
			},
		},
		{
			name: "a walk-scoped incident yields to a signal park",
			build: func(*testing.T) engine.InstanceState {
				return engine.InstanceState{
					Status:    engine.StatusRunning,
					Incidents: []engine.Incident{walkScopedIncident(engine.IncidentCompensationFailed)},
					Tokens: []engine.Token{
						{ID: "tok-sig", NodeID: "wait-sig", State: engine.TokenWaiting, AwaitSignal: "go"},
					},
				}
			},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonSignal, p.Reason,
					"the signal is what the harness must publish to unblock the instance; "+
						"the incident is a record it has no verb for")
				assert.Equal(t, "wait-sig", p.Node)
				assert.False(t, p.HasArmedTimers,
					"control: no timer is involved, so this row is about the rung ORDER "+
						"and not about the deleted yield term")
				require.Len(t, p.Incidents, 1, "and the record is still reported in full")
				assert.Equal(t, []string{"go"}, p.AwaitingSignals)

				// The shipped signal handler matches on AwaitingSignals rather than on
				// Reason, so it drove this park before the split too. What the split
				// fixes is the harness that switches on Reason — the shape ADR-0166's
				// own README recipe uses — which used to fall through to a reason it
				// has no case for and get ErrUnhandledPark.
				h, err := processtest.New()
				require.NoError(t, err)
				d, err := h.PublishSignal("go", nil)(t.Context(), p)
				require.NoError(t, err)
				assert.NotEqual(t, processtest.Pass(), d)
			},
		},
		{
			name: "a walk-scoped incident yields to a message park",
			build: func(*testing.T) engine.InstanceState {
				return engine.InstanceState{
					Status:    engine.StatusRunning,
					Incidents: []engine.Incident{walkScopedIncident(engine.IncidentCompensationFailed)},
					Tokens: []engine.Token{
						{ID: "tok-msg", NodeID: "wait-msg", State: engine.TokenWaiting, AwaitMessage: "invoice"},
					},
				}
			},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonMessage, p.Reason)
				assert.Equal(t, "wait-msg", p.Node)
				require.Len(t, p.AwaitingMessages, 1)
			},
		},
		{
			name: "a walk-scoped incident yields to a command wait with no armed timer",
			build: func(*testing.T) engine.InstanceState {
				return engine.InstanceState{
					Status:    engine.StatusRunning,
					Incidents: []engine.Incident{walkScopedIncident(engine.IncidentCompensationFailed)},
					Tokens: []engine.Token{
						{ID: "tok-cmd", NodeID: "svc", State: engine.TokenWaiting, AwaitCommand: "i-c1"},
					},
				}
			},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonAsyncChild, p.Reason)
				assert.Equal(t, "svc", p.Node)
			},
		},
		{
			// CHARACTERIZATION, not RED: measured async-child before the split too.
			// See the function doc for why finding 3 is a coherence defect whose
			// observable outcome does not move.
			name:  "a walk-scoped incident beside a SECONDARY timer parks as async-child",
			build: walkScopedIncidentWithCommandWaitState,
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonAsyncChild, p.Reason,
					"the boundary timer is an attachment to the action, not the park")
				assert.Equal(t, "svc", p.Node)
				assert.True(t, p.HasArmedTimers,
					"control: the term the deleted yield read is TRUE here, which is exactly "+
						"why it could not agree with the timer rung it was yielding to")
			},
		},
		{
			name: "a walk-scoped incident yields to an open human task",
			build: func(*testing.T) engine.InstanceState {
				return engine.InstanceState{
					Status:    engine.StatusRunning,
					Incidents: []engine.Incident{walkScopedIncident(engine.IncidentCompensationFailed)},
					Tasks: []humantask.HumanTask{
						{TaskID: "t1", NodeID: "approve", State: humantask.Unclaimed},
					},
				}
			},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonHumanTask, p.Reason,
					"unchanged: the human-task rung was already above the incident rung")
				assert.Equal(t, "approve", p.Node)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, processtest.Classify(tc.build(t)))
		})
	}
}
