package engine

// step_terminal_dispatch_test.go — ADR-0165 §5.2: the BEHAVIOURAL
// exhaustiveness table for the single terminal guard in dispatch.
//
// Relationship to its sibling tables — three properties, deliberately not fused:
//
//   - trigger_terminal_policy_test.go pins WHICH POLICY each trigger declares.
//     It reads terminalPolicy() and never calls Step, so it cannot tell whether
//     dispatch honours the declaration at all.
//   - step_terminal_policy_test.go (package engine_test) pins the six concrete
//     RESURRECTION ROUTES this delivery closes, each on a fixture built to make
//     that specific route reachable, with route-specific post-conditions.
//   - THIS table pins the mapping BETWEEN them: for every one of the sealed
//     interface's variants, dispatch produces the OUTCOME its class prescribes.
//     It asserts outcomes (error / silent no-op / handler ran), never policy
//     values, so it stays honest if the enum is renumbered.
//
// Why package engine (white-box) rather than engine_test: the table is driven
// off allTriggerVariants, the unexported one-of-each-variant list that
// TestValidateTriggerKindsAreExhaustive and TestTriggerTerminalPolicies already
// consume. Driving all three off the SAME list is what makes a 16th trigger
// impossible to slip past any of them; duplicating the list in engine_test
// would create a fourth hand-maintained trigger inventory that can silently go
// stale. Step itself is exported, so the assertions are still black-box in
// substance.
//
// No `ctx` case modifier (table-test skill rule 3): Step documents that ctx is
// used ONLY for trace-correlated logging, carries no cancellation semantics and
// is never inspected for control flow — a cancelled-context case would assert
// nothing about this SUT.
//
// ⚠ Vacuity accounting, MEASURED rather than reasoned — an earlier version of
// this comment reasoned it out and got it wrong, in the table's own favour, by
// writing off five rows that are in fact load-bearing.
//
// Method: disable ALL NINE terminal guards at once (this one in dispatch, the
// seven in step_triggers.go, and stepCompensateRequested's) and count the rows
// that go RED. Result: **13 of 15**.
//
//   - 12 refusal rows go RED — including ActionCompleted, ActionFailed,
//     SubInstanceCompleted, SubInstanceFailed and HumanCandidatesResolved, whose
//     placeholder ids ("c", "h") do NOT save them: they reach a handler that
//     mutates the terminal state through other paths.
//   - CompensateRequested goes RED for the opposite reason — it is the
//     allowOnTerminal row, and removing the guards breaks the walk it asserts.
//   - **TimerFired is the ONE genuinely vacuous refusal row**, and it is vacuous
//     for a structural reason rather than a fixture weakness: a terminal state
//     can never carry a live boundary arm. endInstance calls
//     cancelAllScheduledWork, which calls cancelAllArmsAndBoundaries
//     (engine/state_arms.go), which emits a CancelTimer for every timer arm and
//     then sets s.Boundaries = nil. So no state Step can produce holds an armed
//     timer, and a late TimerFired matches nothing whatever the guard does.
//     Driving a fixture cannot change that; only hand-injecting an arm the engine
//     never leaves behind could, and that would assert against an unreachable
//     state. Left vacuous deliberately.
//
//     That sweep has TWO halves, and only one of them is covered — measured, by
//     mutating each half separately:
//       - The CancelTimer EMISSION is covered load-bearingly by
//         TestCancelRequestedTerminates/service-task-with-boundary-timer
//         (engine/step_errors_test.go:801). cancelWithTimerDef really arms a
//         boundary timer on a ServiceTask host, CancelRequested then takes the
//         immediate-termination branch (which never calls
//         removeBoundaryArmsForHost), so that CancelTimer can only come from this
//         function's loop over s.Boundaries. Deleting the loop turns it RED.
//       - `s.Boundaries = nil` itself has NO semantic cover anywhere in the
//         package. Deleting that one line leaves every boundary-oriented test
//         green; the only thing that goes RED is this table's own staysSilent
//         byte-identity rows, and purely as a nil-vs-empty-slice diff
//         ([]boundaryArm{} vs nil), which is an artifact rather than a statement
//         about sweeping.
//     ⚠ Do NOT cite end_force_termination_test.go's
//     assert.Empty(res.State.Boundaries) as cover: its forceDef declares no
//     boundary node, so that assertion cannot fail either.
//
// ⚠⚠ FIVE of those RED rows are masked TODAY by the per-handler guards that
// Task 4 deletes: with only THIS guard disabled just 8 rows go RED. Do not read
// that smaller number as "the other rows are decorative" — they become the sole
// cover for their handlers the moment Task 4 lands. Task 1's
// step_terminal_policy_test.go carries the per-route reproductions; this table
// carries the exhaustiveness property, so a 16th trigger, or a reclassified
// existing one, cannot land without a row stating its outcome.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
)

// ⚠ TWO fixtures, not one, and the reason is a real engine property rather
// than convenience: an unhandled error on an instance that already holds a root
// compensation record does NOT fail the instance — it starts a compensation
// walk and lands on StatusCompensating, which is not terminal. So "terminal
// with live waiters" and "terminal with live compensation records" are mutually
// exclusive states, and the table needs both:
//
//   - survivorsDef  → StatusFailed with two parked catch branches. Every row
//     that must be REFUSED runs here, because this is the state in which a
//     refusal is observable.
//   - compensableDef → StatusTerminated with both compensation records intact
//     (forceTerminate drops tokens but never touches RootCompensations). Only
//     the one allowOnTerminal row runs here, because this is the state in which
//     a plain full rollback has work to do.

// survivorsDef is
//
//	start → fork ⇉ { sig("s") → end-a ; msg("m", key "") → end-b ; doomed }
//
// The catch ids are chosen to match the corresponding keys in
// allTriggerVariants: the signal catch is named "s" and the message correlator
// ("m", "") is uncorrelated, which is exactly what NewSignalReceived(at, "s",
// …) and NewMessageReceived(at, "m", "", …) address. Without that the two
// broadcast rows would match no waiter and pass before any guard exists — the
// "test that cannot fail" shape this delivery is specifically hunting.
//
// "doomed" carries no retry policy, no boundary and no outgoing flow, so a
// non-retryable failure takes handleUnhandledError's fail-fast branch: it fails
// the instance WITHOUT dropping a token, leaving the other branches parked.
func survivorsDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-terminal-dispatch-survivors", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			event.NewIntermediateCatch("sig", event.WithSignalName("s")),
			event.NewIntermediateCatch("msg", event.WithMessageCorrelator("m", "")),
			activity.NewServiceTask("doomed", activity.WithTaskAction("doomed-action")),
			event.NewEnd("end-a"),
			event.NewEnd("end-b"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start-fork", Source: "start", Target: "fork"},
			{ID: "f-fork-sig", Source: "fork", Target: "sig"},
			{ID: "f-sig-end", Source: "sig", Target: "end-a"},
			{ID: "f-fork-msg", Source: "fork", Target: "msg"},
			{ID: "f-msg-end", Source: "msg", Target: "end-b"},
			{ID: "f-fork-doomed", Source: "fork", Target: "doomed"},
		},
	}
}

// compensableDef is
//
//	start → svc(compensable) → after(compensable) → end(force termination)
//
// the ADR-0164 shape: driven to its end it leaves StatusTerminated with BOTH
// compensation records intact.
func compensableDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-terminal-dispatch-compensable", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc",
				activity.WithTaskAction("charge-action"),
				activity.WithCompensateAction("refund-action"),
			),
			activity.NewServiceTask("after",
				activity.WithTaskAction("ship-action"),
				activity.WithCompensateAction("unship-action"),
			),
			event.NewEnd("end", event.WithForceTermination("abort", event.OutcomeAbort)),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start-svc", Source: "start", Target: "svc"},
			{ID: "f-svc-after", Source: "svc", Target: "after"},
			{ID: "f-after-end", Source: "after", Target: "end"},
		},
	}
}

// invokeIDForAction returns the CommandID of the pending InvokeAction that
// dispatched the named action.
func invokeIDForAction(t *testing.T, cmds []Command, name string) string {
	t.Helper()
	for _, c := range cmds {
		if ia, ok := c.(InvokeAction); ok && !ia.FireAndForget && ia.Name == name {
			return ia.CommandID
		}
	}
	require.FailNowf(t, "action was not dispatched", "no pending InvokeAction named %q", name)
	return ""
}

// driveToFailedWithParkedWaiters walks survivorsDef to StatusFailed with both
// catch branches still parked. Every post-condition it asserts is a control: if
// the fail path ever starts dropping tokens, the broadcast rows would go
// vacuously green without them.
func driveToFailedWithParkedWaiters(t *testing.T, def *model.ProcessDefinition, at time.Time) InstanceState {
	t.Helper()

	r1, err := Step(t.Context(), def, InstanceState{InstanceID: "i-dispatch-survivors"},
		NewStartInstance(at, nil), StepOptions{})
	require.NoError(t, err)

	r2, err := Step(t.Context(), def, r1.State,
		NewActionFailed(at.Add(time.Second), invokeIDForAction(t, r1.Commands, "doomed-action"), "boom", false),
		StepOptions{})
	require.NoError(t, err)

	st := r2.State
	require.Equal(t, StatusFailed, st.Status,
		"setup: the unhandled error must have failed the instance")
	require.Len(t, st.Tokens, 3,
		"setup: the fail-fast path must leave all three tokens, or the broadcast rows are vacuous")
	require.NotEmpty(t, st.SignalWaiters(),
		`setup: a token must still await signal "s", or the SignalReceived row cannot fail`)
	require.NotEmpty(t, st.MessageWaiters(),
		`setup: a token must still await message "m", or the MessageReceived row cannot fail`)

	// No boundary-arm control assertion here on purpose. survivorsDef declares no
	// boundary node, so st.Boundaries is nil whether or not the terminal sweep
	// runs — an assertion on it could not fail, and one carrying a comment that
	// promised it would is worse than none, because it stops the next reader
	// looking. The reason the TimerFired row is vacuous is recorded in the header
	// instead, where it is a claim rather than a fake test.

	return st
}

// driveToTerminatedWithCompensations walks compensableDef through both
// ServiceTask completions to its force-termination end.
func driveToTerminatedWithCompensations(t *testing.T, def *model.ProcessDefinition, at time.Time) InstanceState {
	t.Helper()

	r1, err := Step(t.Context(), def, InstanceState{InstanceID: "i-dispatch-compensable"},
		NewStartInstance(at, nil), StepOptions{})
	require.NoError(t, err)

	r2, err := Step(t.Context(), def, r1.State,
		NewActionCompleted(at.Add(time.Second), invokeIDForAction(t, r1.Commands, "charge-action"), nil),
		StepOptions{})
	require.NoError(t, err)

	r3, err := Step(t.Context(), def, r2.State,
		NewActionCompleted(at.Add(2*time.Second), invokeIDForAction(t, r2.Commands, "ship-action"), nil),
		StepOptions{})
	require.NoError(t, err)

	st := r3.State
	require.Equal(t, StatusTerminated, st.Status,
		"setup: the force-termination end must have run")
	require.Len(t, st.RootCompensations, 2,
		"setup: both compensation records must survive, or the allowOnTerminal row is vacuous")

	return st
}

// TestTerminalDispatchOutcomes drives one of EVERY Trigger variant against a
// terminal instance and asserts the outcome its declared class prescribes. It
// is the test that would catch a guard wired into the wrong place — omitted
// from Step, placed after the type switch, or applied to only some arms.
func TestTerminalDispatchOutcomes(t *testing.T) {
	t.Parallel()

	at := time.Unix(0, 0).UTC()
	// Deliver every trigger from strictly after the instance died, so no row can
	// be explained away as a race with the terminal transition itself.
	deliveredAt := at.Add(time.Hour)

	survivors := survivorsDef()
	survivorsState := driveToFailedWithParkedWaiters(t, survivors, at)
	compensable := compensableDef()
	compensableState := driveToTerminatedWithCompensations(t, compensable, at)

	type outcomeFunc func(t *testing.T, before InstanceState, r StepResult, err error)

	// rejectsWithError: the trigger's caller is a synchronous external API that
	// must be TOLD its request was refused. Step returns the zero StepResult on
	// error, so the caller keeps the state it already held.
	rejectsWithError := func(t *testing.T, _ InstanceState, r StepResult, err error) {
		t.Helper()
		assert.ErrorIs(t, err, ErrInstanceTerminal,
			"a synchronous caller must be told the instance is terminal")
		assert.ErrorIs(t, err, ErrInvalidTransition,
			"ErrInstanceTerminal must keep wrapping ErrInvalidTransition, or service/ stops mapping it to a conflict")
		assert.Empty(t, r.Commands, "a rejected trigger must dispatch nothing")
		assert.Zero(t, r.State.Status, "Step must return the zero StepResult on error")
	}

	// staysSilent: the trigger is delivered by the engine's own asynchronous
	// machinery (scheduler, broadcast fan-out, worker relay), whose caller cannot
	// distinguish a no-op from success and must not retry. The state must come
	// back byte-identical: a silent DROP, not a silent APPLY.
	staysSilent := func(t *testing.T, before InstanceState, r StepResult, err error) {
		t.Helper()
		require.NoError(t, err,
			"an asynchronously-delivered trigger must be dropped silently, not surfaced to a caller that cannot act on it")
		assert.Empty(t, r.Commands, "a silently-dropped trigger must dispatch nothing")
		assert.Equal(t, before, r.State,
			"a silently-dropped trigger must leave the terminal state byte-identical")
	}

	// runsHandler: the trigger deliberately operates ON a terminal instance.
	// Asserting the walk really started (and dispatched) is what stops a blanket
	// guard from quietly swallowing the one case that must survive it.
	runsHandler := func(t *testing.T, _ InstanceState, r StepResult, err error) {
		t.Helper()
		require.NoError(t, err,
			"a plain full rollback is a legitimate admin action on a terminal instance (ADR-0164 carve-out)")
		assert.Equal(t, StatusCompensating, r.State.Status,
			"the compensation walk must actually start")
		assert.NotEmpty(t, r.Commands,
			"the walk must dispatch a compensation InvokeAction, not silently swallow the trigger")
	}

	// expected pairs the outcome a trigger's class prescribes with the terminal
	// state that makes that outcome observable — see the two-fixture note above.
	type expected struct {
		def    *model.ProcessDefinition
		before InstanceState
		assert outcomeFunc
	}
	refusedOn := func(outcome outcomeFunc) expected {
		return expected{def: survivors, before: survivorsState, assert: outcome}
	}

	// One entry per concrete Trigger, keyed by triggerTypeName (which is %T, so
	// the keys carry the "engine." qualifier exactly as validatedTriggerKinds
	// does). Deliberately a LOOKUP rather than a literal case list: the rows are
	// then driven off allTriggerVariants below, so a 16th trigger fails here
	// without anyone remembering this file exists.
	outcomes := map[string]expected{
		// Synchronous external callers.
		"engine.StartInstance":   refusedOn(rejectsWithError),
		"engine.HumanClaimed":    refusedOn(rejectsWithError),
		"engine.HumanReassigned": refusedOn(rejectsWithError),
		"engine.HumanCompleted":  refusedOn(rejectsWithError),
		"engine.ResolveIncident": refusedOn(rejectsWithError),
		// ADR-0175: the escape verbs act on a COMPENSATING instance. Once it is
		// terminal the walk is already over, so there is nothing left to escape and
		// the operator must be told so rather than left believing it worked.
		"engine.ResolveCompensationStall": refusedOn(rejectsWithError),

		// Engine-internal asynchronous delivery.
		"engine.ActionCompleted": refusedOn(staysSilent),
		"engine.ActionFailed":    refusedOn(staysSilent),
		// ⚠ A WEAK pin, stated so nobody reads it as more than it is. It is the
		// sole cover for handleHumanCandidatesResolved's deleted guard, but it
		// reddens for an INCIDENTAL reason: survivorsDef declares no user task at
		// all, so an unguarded handler dies at `task == nil` with ErrTokenNotFound
		// rather than exercising the property the guard documented (no candidate
		// rewrite, no UpdateTask published against a dead instance).
		//
		// The real scenario was built and measured rather than assumed — a
		// parallel fork carrying a user task alongside the doomed service task —
		// and the guard turns out to have been genuinely redundant there: the
		// terminal transition cancels the task, so `!task.IsOpen()` no-ops the
		// handler with or without it. Nothing is at risk, and a test for that
		// scenario would assert a property the task-lifetime check already
		// enforces, so none was added: it would be vacuous, which is the exact
		// shape this delivery is hunting.
		"engine.HumanCandidatesResolved": refusedOn(staysSilent),
		"engine.TimerFired":              refusedOn(staysSilent),
		"engine.SignalReceived":          refusedOn(staysSilent),
		"engine.MessageReceived":         refusedOn(staysSilent),
		"engine.SubInstanceCompleted":    refusedOn(staysSilent),
		"engine.SubInstanceFailed":       refusedOn(staysSilent),
		// CancelRequested is the audit's own route and the one silent row that is
		// NOT vacuous on survivorsState: unguarded it force-terminates a
		// StatusFailed instance, stamping StatusTerminated over it and dropping
		// every surviving token.
		"engine.CancelRequested": refusedOn(staysSilent),

		// allTriggerVariants carries the PLAIN CompensateRequested (both ToNode and
		// ReverseNode empty), which is the one allowOnTerminal shape, and it is the
		// only row that runs on the compensable fixture. Its resume-shaped siblings
		// are rejectWithError and are covered by TestTerminalResumeGuard; the
		// policy split itself is pinned by TestTriggerTerminalPolicies.
		"engine.CompensateRequested": {def: compensable, before: compensableState, assert: runsHandler},
	}

	type testCase struct {
		name    string
		def     *model.ProcessDefinition
		before  InstanceState
		trigger Trigger
		assert  outcomeFunc
	}

	variants := allTriggerVariants(deliveredAt)
	cases := make([]testCase, 0, len(variants))
	for _, trg := range variants {
		name := triggerTypeName(trg)
		exp, ok := outcomes[name]
		require.True(t, ok,
			"trigger %s declares a terminalPolicy but no row here pins the OUTCOME dispatch must produce for it", name)
		cases = append(cases, testCase{
			name: name, def: exp.def, before: exp.before, trigger: trg, assert: exp.assert,
		})
	}
	require.Len(t, outcomes, len(variants),
		"every Trigger variant must appear exactly once; a leftover row names a trigger that no longer exists")

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.True(t, tc.before.Status.IsTerminal(),
				"the fixture this row runs against must be terminal, or the row asserts nothing about the guard")

			r, err := Step(t.Context(), tc.def, tc.before, tc.trigger, StepOptions{})
			tc.assert(t, tc.before, r, err)
		})
	}
}
