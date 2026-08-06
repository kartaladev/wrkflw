package engine_test

// step_terminal_test.go — ADR-0164 Decision 1 and 3: every terminal transition
// runs through the one endInstance path, which clears the compensation cursor
// and retires the incidents whose token the transition dropped.
//
// The tail of the file pins the two behaviour NORMALIZATIONS the ADR declares —
// normal completion now retires the scheduled work it used to leave armed, and
// the one site that emitted its terminal command first now emits it in the
// canonical position — plus the third completion site (the no-outgoing-flow exit
// of a nested event sub-process), whose rerouting through endInstance was
// otherwise 0-covered. Each is written against a scenario pinned to ONE exit
// path, and each was verified to go RED under a mutation of exactly the
// production line it claims to pin.
//
// The two incident tests are a PAIR and only mean something together: a
// wholesale `s.Incidents = nil` satisfies the orphan test and must FAIL the
// surviving-token test. That is the entire reason both exist — the narrow sweep
// and the wholesale clear coincide at the two token-dropping sites, so the sites
// that keep their tokens are the only place the distinction is observable.

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
)

var terminalT0 = time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)

// TestForceTerminationClearsCompensationCursor pins ADR-0164's stale-cursor
// defect: startCompensationWalk stamps s.Compensating, and before this change no
// terminal transition cleared it. A step that starts a walk and then
// force-terminates therefore committed StatusTerminated WITH a live cursor
// carrying a stale ResumeNode, which a later plain CompensateRequested inherited
// in beginCompensation — resurrecting the instance at that node.
//
// Branch declaration order is load-bearing: the throw branch must be declared
// FIRST so the walk starts before forceTerminate halts drive.
func TestForceTerminationClearsCompensationCursor(t *testing.T) {
	t.Parallel()

	// start → charge(compensable) → fork ⇒ { f1: throw → end1 ; f2: halt }.
	def := compensableThenThrowDef(
		[]model.Node{
			gateway.NewParallel("fork"),
			event.NewCompensateThrow("throw"),
			event.NewEnd("end1"),
			event.NewEnd("halt", event.WithForceTermination("kill", event.OutcomeAbort)),
		},
		[]flow.SequenceFlow{
			{ID: "f-charge-fork", Source: "charge", Target: "fork"},
			{ID: "f1", Source: "fork", Target: "throw"},
			{ID: "f2", Source: "fork", Target: "halt"},
			{ID: "f-throw-end", Source: "throw", Target: "end1"},
		},
	)

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-cursor"},
		engine.NewStartInstance(terminalT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	chargeCmd := firstInvokeCommandID(t, r1.Commands)

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(terminalT0.Add(time.Second), chargeCmd, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Positive control: the sibling branch really did force-terminate. Without it
	// the zero-cursor assertion would also pass for a fixture that never started a
	// walk and never terminated.
	require.True(t, r2.State.Status.IsTerminal(),
		"control: the sibling branch must have force-terminated the instance")
	var reachedThrow bool
	for _, v := range r2.State.History {
		if v.NodeID == "throw" {
			reachedThrow = true
		}
	}
	require.True(t, reachedThrow,
		"control: the throw branch must have run, or no walk ever stamped a cursor")

	assert.Zero(t, r2.State.Compensating,
		"a terminal transition must leave no compensation cursor behind")
}

// terminalIncidentFork builds start → fork ⇒ { flaky → end1 ; tail… }, where
// "flaky" is a ServiceTask with a one-attempt retry policy: a terminal
// ActionFailed against it exhausts the budget, matches no boundary, and so the
// raiseIncident policy parks its token on an incident while the instance keeps
// running. The tail is the branch that later ends the instance.
func terminalIncidentFork(tail []model.Node, tailFlows []flow.SequenceFlow) *model.ProcessDefinition {
	nodes := []model.Node{
		event.NewStart("start"),
		gateway.NewParallel("fork"),
		activity.NewServiceTask("flaky", activity.WithTaskAction("flaky-action"),
			activity.WithRetryPolicy(&model.RetryPolicy{MaxAttempts: 1})),
		event.NewEnd("end1"),
	}
	flows := []flow.SequenceFlow{
		{ID: "f-start-fork", Source: "start", Target: "fork"},
		{ID: "f-fork-flaky", Source: "fork", Target: "flaky"},
		{ID: "f-flaky-end", Source: "flaky", Target: "end1"},
	}
	return &model.ProcessDefinition{
		ID:      "p-terminal-incident",
		Version: 1,
		Nodes:   append(nodes, tail...),
		Flows:   append(flows, tailFlows...),
	}
}

// raiseIncidentOnFlaky drives the fixture to the state both incident tests start
// from: "flaky" has exhausted its single attempt and its token is parked on an
// incident, the instance is still running, and the other branch is untouched.
// It returns the state and the map of action name → CommandID from the first step.
func raiseIncidentOnFlaky(t *testing.T, def *model.ProcessDefinition, instanceID string) (engine.InstanceState, map[string]string) {
	t.Helper()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: instanceID},
		engine.NewStartInstance(terminalT0, nil), engine.StepOptions{})
	require.NoError(t, err)

	cmdIDs := map[string]string{}
	for _, c := range r1.Commands {
		if ia, ok := c.(engine.InvokeAction); ok {
			cmdIDs[ia.Name] = ia.CommandID
		}
	}
	require.Contains(t, cmdIDs, "flaky-action", "setup: the flaky branch must have been invoked")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionFailed(terminalT0.Add(time.Minute), cmdIDs["flaky-action"], "boom", true),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.State.Incidents, 1, "setup: the exhausted branch must raise an incident")
	require.False(t, r2.State.Status.IsTerminal(), "setup: an incident must not terminate the instance")

	return r2.State, cmdIDs
}

// TestTerminalFailureKeepsIncidentOfSurvivingToken is the guard against
// over-delivering on ADR-0163's invariant. handleUnhandledError's immediate
// branch leaves s.Tokens in place, so the incident's token survives the terminal
// transition and the incident must survive with it: runtime/outbox.go's
// terminalEventErr reads Incidents[0].Error to report the concrete failure in
// instance.failed, and the service/ audit view renders them after the instance
// is terminal (ADR-0164 Decision 3).
//
// A wholesale s.Incidents = nil in endInstance MUST fail this test.
func TestTerminalFailureKeepsIncidentOfSurvivingToken(t *testing.T) {
	t.Parallel()

	// The second branch is a plain ServiceTask with NO retry policy, so its
	// terminal ActionFailed takes the failFast policy → propagateError finds no
	// boundary → handleUnhandledError's immediate branch fails the instance
	// without dropping a single token.
	def := terminalIncidentFork(
		[]model.Node{
			activity.NewServiceTask("doomed", activity.WithTaskAction("doomed-action")),
			event.NewEnd("end2"),
		},
		[]flow.SequenceFlow{
			{ID: "f-fork-doomed", Source: "fork", Target: "doomed"},
			{ID: "f-doomed-end", Source: "doomed", Target: "end2"},
		},
	)

	st, cmdIDs := raiseIncidentOnFlaky(t, def, "i-keep-incident")
	incidentTokenID := st.Incidents[0].TokenID
	require.Contains(t, cmdIDs, "doomed-action", "setup: the doomed branch must have been invoked")

	r, err := engine.Step(t.Context(), def, st,
		engine.NewActionFailed(terminalT0.Add(time.Hour), cmdIDs["doomed-action"], "fatal", false),
		engine.StepOptions{})
	require.NoError(t, err)

	require.Equal(t, engine.StatusFailed, r.State.Status,
		"control: the unhandled error must have failed the instance")
	require.NotEmpty(t, r.State.Tokens,
		"control: this terminal path must leave the tokens in place, or the test is vacuous")

	var found bool
	for _, tok := range r.State.Tokens {
		if tok.ID == incidentTokenID {
			found = true
		}
	}
	require.True(t, found, "control: the incident's token must have survived the terminal transition")

	require.Len(t, r.State.Incidents, 1,
		"an incident whose token survives the terminal transition must be kept")
	assert.Equal(t, incidentTokenID, r.State.Incidents[0].TokenID,
		"the kept incident must be the one naming the surviving token")
}

// TestForceTerminationClearsOrphanedIncident is the other half of the pair.
// forceTerminate drops every token without going through cancelTokenWaits, so
// the incident raised against the dropped token is orphaned: nothing can resolve
// it and it describes a token that no longer exists (ADR-0164 Decision 3).
func TestForceTerminationClearsOrphanedIncident(t *testing.T) {
	t.Parallel()

	// The second branch parks on a user task so the incident is raised BEFORE the
	// force-termination end is reached; completing the task drives it to "halt".
	def := terminalIncidentFork(
		[]model.Node{
			activity.NewUserTask("review", activity.WithEligibleRoles("r")),
			event.NewEnd("halt", event.WithForceTermination("kill", event.OutcomeAbort)),
		},
		[]flow.SequenceFlow{
			{ID: "f-fork-review", Source: "fork", Target: "review"},
			{ID: "f-review-halt", Source: "review", Target: "halt"},
		},
	)

	st, _ := raiseIncidentOnFlaky(t, def, "i-orphan-incident")
	require.Len(t, st.Tasks, 1, "setup: the review task must be minted")

	r, err := engine.Step(t.Context(), def, st,
		engine.NewHumanCompleted(terminalT0.Add(time.Hour), st.Tasks[0].TaskID,
			engine.CompletionInput{}, authz.Actor{ID: "alice"}),
		engine.StepOptions{})
	require.NoError(t, err)

	require.Equal(t, engine.StatusTerminated, r.State.Status,
		"control: the review branch must have force-terminated the instance")
	require.Empty(t, r.State.Tokens,
		"control: force-termination must have dropped every token")

	assert.Empty(t, r.State.Incidents,
		"an incident whose token was dropped is orphaned and must be retired")
}

// TestTerminalTransitionKeepsIncidentWithEmptyTokenID pins the empty-key rule on
// the orphaned-incident sweep. tokenByID("") returns nil, so a naive
// "no token ⇒ orphaned" test deletes an incident that names NO token at all —
// the exact inversion removeIncidentsForToken guards against with its own
// `if tokenID == ""` early return (ADR-0152: an empty key names nothing, and
// admitting it would let a blank ID wipe the incident list).
//
// No production path builds such an incident today: the only construction site
// is handleUnhandledError under the raiseIncident policy, whose sole caller
// passes tok.ID. The state is therefore assembled directly and driven through
// the engine.EndInstance shim, because the asymmetry between the two sweeps is a
// trap for the next terminal site rather than a live defect.
func TestTerminalTransitionKeepsIncidentWithEmptyTokenID(t *testing.T) {
	t.Parallel()

	// No tokens at all, so a TokenID-linked incident WOULD be orphaned here —
	// which is what makes the empty-key case the only variable under test.
	s := engine.InstanceState{
		InstanceID: "i-empty-token-incident",
		Incidents: []engine.Incident{
			{ID: "inc-empty", TokenID: "", NodeID: "svc", Error: "boom", Attempts: 1},
		},
	}

	engine.EndInstance(&s, engine.StatusFailed, terminalT0, engine.FailInstance{Err: "boom"})

	require.Len(t, s.Incidents, 1,
		"an incident with an empty TokenID names no token, so it is never orphaned (ADR-0152)")
	assert.Equal(t, "inc-empty", s.Incidents[0].ID)
}

// resumableForceTerminatedDef returns:
//
//	start → svc(charge/refund) → after(ship/unship) → end(WithForceTermination)
//
// Two compensable ServiceTasks in a row, then a force-termination end. Driving
// it to completion (see driveToForceTerminatedWithBothRecords) leaves BOTH
// compensation records intact: forceTerminate drops tokens but never touches
// RootCompensations, unlike a compensation-throw walk which would consume them.
func resumableForceTerminatedDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-terminal-resume", Version: 1,
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

// driveToForceTerminatedWithBothRecords drives def (built by
// resumableForceTerminatedDef) through both ServiceTask completions so the
// token reaches the force-termination end with both compensation records
// intact, then returns the resulting terminal state.
func driveToForceTerminatedWithBothRecords(t *testing.T, def *model.ProcessDefinition, instanceID string) engine.InstanceState {
	t.Helper()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: instanceID},
		engine.NewStartInstance(terminalT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	svcCmd := firstInvokeCommandID(t, r1.Commands)

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(terminalT0.Add(time.Second), svcCmd, nil), engine.StepOptions{})
	require.NoError(t, err)
	afterCmd := firstInvokeCommandID(t, r2.Commands)

	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewActionCompleted(terminalT0.Add(2*time.Second), afterCmd, nil), engine.StepOptions{})
	require.NoError(t, err)

	require.True(t, r3.State.Status.IsTerminal(),
		"setup: the force-termination end must have run")
	require.Len(t, r3.State.RootCompensations, 2,
		"setup: both compensation records must be intact")

	return r3.State
}

// TestTerminalResumeGuard pins ADR-0164 Decision 2/4: a CompensateRequested
// shape that would RESUME an already-terminal instance (a non-empty ToNode,
// whether from a raw partial rollback or the NewReverseToNode facade path, both
// of which leave ReverseNode empty) must be rejected, while a PLAIN full
// rollback (both ToNode and ReverseNode empty) must still walk — that carve-out
// is deliberate: internal cancel/error paths re-deliver a plain full rollback
// against an already-terminal instance, and compensating a completed instance
// whose records are still present is a legitimate admin action.
//
// ⚠ ADR-0165 moved WHERE that rejection happens without changing WHETHER it
// happens. CompensateRequested is the one trigger whose terminalPolicy reads its
// receiver: rejectWithError when ReverseNode or ToNode is set, allowOnTerminal
// otherwise. dispatch's single guard therefore refuses the first two cases
// before stepCompensateRequested is ever entered, and waves the third one
// through — so all three properties are unchanged and only the SENTINEL moved.
// The cases below assert engine.ErrInstanceTerminal instead of the old
// "cannot resume a terminal instance" string, which no longer exists; the
// wrapped ErrInvalidTransition classification (409/422) is identical either way.
// This test remains ADR-0109's only pin and carve-out #1's only regression
// cover, which is why it was rewritten rather than dropped.
//
// All three cases share one fixture (resumableForceTerminatedDef) and one
// drive helper (driveToForceTerminatedWithBothRecords) — engine.Step clones
// its input state, so reusing the same terminal st across subtests is safe.
func TestTerminalResumeGuard(t *testing.T) {
	t.Parallel()

	def := resumableForceTerminatedDef()
	st := driveToForceTerminatedWithBothRecords(t, def, "i-terminal-resume-guard")

	type testCase struct {
		name    string
		trigger engine.CompensateRequested
		assert  func(t *testing.T, r engine.StepResult, err error)
	}

	cases := []testCase{
		{
			name: "partial_rollback_rejected",
			// A raw partial rollback (non-empty ToNode, ReverseNode empty) slipped
			// past the old ReverseNode-only guard.
			trigger: engine.NewCompensateRequested(terminalT0.Add(time.Hour), "svc"),
			assert: func(t *testing.T, r engine.StepResult, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, engine.ErrInstanceTerminal)
				assert.ErrorIs(t, err, engine.ErrInvalidTransition,
					"the rejection must stay classifiable as an invalid transition")
				assert.Empty(t, r.Commands, "a rejected resume must dispatch no compensation action")
			},
		},
		{
			name: "targeted_reverse_rejected",
			// NewReverseToNode (behind ProcessDriver.ReverseInstance(...,
			// WithTargetNode(n))) sets ToNode and RestoreTargetVars but leaves
			// ReverseNode empty, so it also slipped past the old guard — the case
			// that makes the ADR-0109 correction honest.
			trigger: engine.NewReverseToNode(terminalT0.Add(time.Hour), "svc"),
			assert: func(t *testing.T, r engine.StepResult, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, engine.ErrInstanceTerminal)
				assert.ErrorIs(t, err, engine.ErrInvalidTransition,
					"the rejection must stay classifiable as an invalid transition")
				assert.Empty(t, r.Commands, "a rejected resume must dispatch no compensation action")
			},
		},
		{
			// Unchanged by ADR-0165, and deliberately so: this shape's policy is
			// allowOnTerminal, so dispatch's guard falls through to the handler and
			// the walk runs exactly as before. It is the row that stops the new
			// blanket guard from swallowing the one CompensateRequested shape that
			// must survive it.
			name:    "plain_full_rollback_allowed",
			trigger: engine.NewCompensateRequested(terminalT0.Add(time.Hour), ""),
			assert: func(t *testing.T, r engine.StepResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusCompensating, r.State.Status,
					"a plain full rollback must still walk, not error, on a terminal instance")
				assert.NotEmpty(t, r.Commands,
					"the walk must actually dispatch a compensation InvokeAction, not silently swallow the trigger")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r, err := engine.Step(t.Context(), def, st, tc.trigger, engine.StepOptions{})
			tc.assert(t, r, err)
		})
	}
}

// TestActionCompletedOnTerminalInstanceIsNoOp pins audit finding O1
// (pre-existing): a CompensateThrow consumes only its OWN token
// (startCompensationWalk's consumeToken), so a sibling branch can complete the
// instance while the walk's InvokeAction is still in flight. When that
// action's ActionCompleted finally arrives, no token awaits it, and a terminal
// instance can never consume a resumption trigger — the stray command must be
// tolerated as a no-op, exactly like handleResolveIncident already tolerates a
// missing token, rather than surfaced as ErrTokenNotFound.
//
// The sibling branch reaches a PLAIN end, not a force-termination end: force
// termination drops tokens for an unrelated reason (forceTerminate) and would
// not exercise "no token awaits this command against an otherwise-normal
// completion" — the scenario this test targets.
//
// The sibling branch is a ServiceTask ("hold"), not a bare flow straight to
// end2: dropStaleTokenCommands' liveAwaiters treats a terminal instance as
// having NO live awaiters at all (step_stale_commands.go), so if both branches
// resolved inside the SAME Step call the walk's own InvokeAction would be
// filtered out of that step's Commands before this test ever saw it — the
// walk would never look "in flight" the way a real dispatched-and-pending
// action does. Parking the sibling on its own action forces the walk-start and
// the instance-completion into two SEPARATE Step calls, exactly as the brief
// requires, so the walk's InvokeAction is observed live (non-terminal state,
// not filtered) before the instance ever goes terminal.
func TestActionCompletedOnTerminalInstanceIsNoOp(t *testing.T) {
	t.Parallel()

	// start → charge(compensable) → fork ⇒
	//   { f1: throw → end1 ; f2: hold(ServiceTask) → end2(plain) }.
	// Branch declaration order is load-bearing, exactly as in
	// TestForceTerminationClearsCompensationCursor: the throw branch must be
	// declared FIRST so the walk starts (and consumes its own token) before
	// drive reaches "hold" on the sibling branch.
	def := compensableThenThrowDef(
		[]model.Node{
			gateway.NewParallel("fork"),
			event.NewCompensateThrow("throw"),
			event.NewEnd("end1"),
			activity.NewServiceTask("hold", activity.WithTaskAction("hold-action")),
			event.NewEnd("end2"),
		},
		[]flow.SequenceFlow{
			{ID: "f-charge-fork", Source: "charge", Target: "fork"},
			{ID: "f1", Source: "fork", Target: "throw"},
			{ID: "f2", Source: "fork", Target: "hold"},
			{ID: "f-throw-end", Source: "throw", Target: "end1"},
			{ID: "f-hold-end", Source: "hold", Target: "end2"},
		},
	)

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-stranded-compensation"},
		engine.NewStartInstance(terminalT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	chargeCmd := firstInvokeCommandID(t, r1.Commands)

	// Step 2: branch1 (throw) starts the compensation walk — consuming its own
	// token and emitting the refund InvokeAction — and, because
	// startCompensationWalk returns stop=false, drive keeps going and branch2
	// parks on "hold"'s InvokeAction. Neither branch has completed the
	// instance yet: the walk is genuinely in flight against a still-running
	// instance.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(terminalT0.Add(time.Second), chargeCmd, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Positive control: the walk really did start and is really still live, or
	// the rest of this test would prove nothing.
	require.False(t, r2.State.Status.IsTerminal(),
		"control: the instance must still be running while the walk is in flight")
	require.NotEmpty(t, r2.State.Compensating.ActiveCmdID,
		"control: the compensation walk must have a live cursor")
	var walkCmd string
	for _, c := range r2.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "refund-action" {
			walkCmd = ia.CommandID
		}
	}
	require.NotEmpty(t, walkCmd, "control: the walk must have dispatched the refund InvokeAction")
	var holdCmd string
	for _, c := range r2.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "hold-action" {
			holdCmd = ia.CommandID
		}
	}
	require.NotEmpty(t, holdCmd, "setup: the sibling branch must have parked on hold's InvokeAction")
	require.NotEqual(t, walkCmd, holdCmd,
		"setup: hold's own command must be distinct from the stranded walk command")

	// Step 3: the sibling branch's own action completes, driving it through
	// end2. That is the LAST remaining token (the throw's own token was
	// already consumed in step 2), so the instance completes here — WITHOUT
	// ever touching the walk's still-outstanding command.
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewActionCompleted(terminalT0.Add(2*time.Second), holdCmd, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Positive control: the instance really is terminal now, and really has no
	// token left to await anything — including the stranded walk command.
	require.True(t, r3.State.Status.IsTerminal(),
		"control: the sibling branch must have completed the instance")
	require.Empty(t, r3.State.Tokens,
		"control: no token must remain to await the stranded walk command")

	// Step 4 (the brief's "third step"): the walk's ActionCompleted finally
	// arrives against the now-terminal instance, which awaits nothing.
	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewActionCompleted(terminalT0.Add(3*time.Second), walkCmd, nil), engine.StepOptions{})
	require.NoError(t, err)

	// "Unchanged state" is asserted by comparing the FULL pre-step-4 state
	// (r3.State — a real completed instance with non-empty History and a
	// terminal Status, not a zero value) against the FULL post-step-4 state
	// (r4.State). If the IsTerminal() branch were deleted, step four would
	// return an error and require.NoError above would already fail; if it
	// were instead mutated to return the wrong (e.g. zero-value) StepResult on
	// a nil error, this equality would catch that too.
	//
	// Tokens is normalized to nil when empty on both sides first: r3.State's
	// Tokens is a non-nil zero-length leftover from consuming end2's token
	// in-place, while Step's internal clone of a zero-length slice produces a
	// nil one — a representation artifact of how Step copies its input, not a
	// real difference this test is meant to police.
	r3State, r4State := r3.State, r4.State
	if len(r3State.Tokens) == 0 {
		r3State.Tokens = nil
	}
	if len(r4State.Tokens) == 0 {
		r4State.Tokens = nil
	}
	assert.Equal(t, r3State, r4State,
		"a stray ActionCompleted against a terminal instance must be a pure no-op")
	assert.Empty(t, r4.Commands,
		"a pure no-op must not emit any command either")
}

// TestActionCompletedOnFailedInstanceWithSurvivingSiblingIsNoOp pins the OTHER
// half of the resumption guard — the half a guard placed inside the
// `tok == nil` branch cannot reach.
//
// TestActionCompletedOnTerminalInstanceIsNoOp above covers the stranded
// command whose token is GONE. This one covers the stranded command whose
// token SURVIVES: handleUnhandledError's immediate-fail branch sets
// StatusFailed without dropping s.Tokens, so every sibling keeps its
// AwaitCommand intact (step_stale_commands.go says exactly this). tokenAwaiting
// matches on AwaitCommand alone with no status check, so the sibling's own
// in-flight ActionCompleted — dispatched in an EARLIER step, hence untouched by
// ADR-0161's same-step liveAwaiters filter — still resolves to a live token.
// Without a guard at the TOP of the handler, that token is merged and driven
// forward on a dead instance, reaches its end event as the last remaining
// token, and exitRootScope flips a FAILED instance to Completed. The second
// terminal outbox event is then suppressed by terminalOutboxEvent's
// prevStatus.IsTerminal() check, so the persisted status silently disagrees
// with the instance.failed event already published.
//
// The two service tasks are load-bearing: both must park on their OWN
// InvokeAction in step 1 so the surviving command is genuinely in flight across
// steps, exactly as a real dispatched action is.
func TestActionCompletedOnFailedInstanceWithSurvivingSiblingIsNoOp(t *testing.T) {
	t.Parallel()

	// start → fork ⇒ { fa: worka(ServiceTask) → enda ;
	//                  fb: workb(ServiceTask) → boom(error end, no boundary) }.
	def := &model.ProcessDefinition{
		ID: "p-terminal-surviving-sibling", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewServiceTask("worka", activity.WithTaskAction("a-action")),
			event.NewEnd("enda"),
			activity.NewServiceTask("workb", activity.WithTaskAction("b-action")),
			event.NewEnd("boom", event.WithErrorCode("E9")),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "fork"},
			{ID: "fa", Source: "fork", Target: "worka"},
			{ID: "fb", Source: "fork", Target: "workb"},
			{ID: "f-worka-end", Source: "worka", Target: "enda"},
			{ID: "f-workb-boom", Source: "workb", Target: "boom"},
		},
	}

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-surviving-sibling"},
		engine.NewStartInstance(terminalT0, nil), engine.StepOptions{})
	require.NoError(t, err)

	aCmd := invokeCommandIDForAction(t, r1.Commands, "a-action")
	bCmd := invokeCommandIDForAction(t, r1.Commands, "b-action")
	require.NotEqual(t, aCmd, bCmd, "setup: the two branches must hold distinct commands")

	// Step 2: branch b completes and runs into an error end event with no
	// matching boundary and no compensation records, so the instance fails
	// immediately — WITHOUT dropping branch a's token.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(terminalT0.Add(time.Second), bCmd, nil), engine.StepOptions{})
	require.NoError(t, err)

	require.Equal(t, engine.StatusFailed, r2.State.Status,
		"control: the unhandled error must have FAILED the instance")
	var survivor bool
	for _, tok := range r2.State.Tokens {
		if tok.AwaitCommand == aCmd {
			survivor = true
		}
	}
	require.True(t, survivor,
		"control: branch a's token must survive the failure with its AwaitCommand intact — "+
			"that survival is the whole premise of this test")

	// Step 3: branch a's own action, dispatched back in step 1, finally
	// completes against the now-FAILED instance.
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewActionCompleted(terminalT0.Add(2*time.Second), aCmd, nil), engine.StepOptions{})
	require.NoError(t, err)

	assert.Equal(t, engine.StatusFailed, r3.State.Status,
		"a failed instance must not be resurrected — let alone flipped to Completed — "+
			"by a sibling's in-flight ActionCompleted")
	assert.Empty(t, r3.Commands,
		"a terminal instance awaits nothing, so the stray completion must emit no command")
}

// TestActionFailedOnFailedInstanceWithSurvivingSiblingIsNoOp is the symmetric
// twin of the ActionCompleted test above, and pins the FOURTH resurrection
// route.
//
// handleActionFailed carries the identical unguarded tokenAwaiting lookup, so
// the same surviving-sibling setup reaches it: an in-flight ActionFailed whose
// token outlived a terminal transition is dispatched normally. When the
// sibling's node carries an error boundary, propagateError routes to it, the
// recovery path runs to a normal end as the last remaining token, and
// exitRootScope flips a FAILED instance to Completed — exactly the C1 defect,
// through a different handler. (Without a boundary the same lookup instead
// emits a DUPLICATE FailInstance and re-runs endInstance on an
// already-terminal instance.)
//
// The boundary is what makes this the sharper of the two variants: it produces
// the silent status flip rather than a duplicate command, and it proves the
// stray trigger is driven through real routing logic, not merely mishandled at
// the edge.
func TestActionFailedOnFailedInstanceWithSurvivingSiblingIsNoOp(t *testing.T) {
	t.Parallel()

	// start → fork ⇒ { fa: worka(ServiceTask) → enda, with an error boundary
	//                      "bnd" (E-RECOVER) → recovered(plain end) ;
	//                  fb: workb(ServiceTask) → boom(error end, no boundary) }.
	def := &model.ProcessDefinition{
		ID: "p-terminal-surviving-sibling-failed", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewServiceTask("worka", activity.WithTaskAction("a-action")),
			event.NewBoundary("bnd", "worka", event.WithBoundaryErrorCode("E-RECOVER")),
			event.NewEnd("recovered"),
			event.NewEnd("enda"),
			activity.NewServiceTask("workb", activity.WithTaskAction("b-action")),
			event.NewEnd("boom", event.WithErrorCode("E9")),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "fork"},
			{ID: "fa", Source: "fork", Target: "worka"},
			{ID: "fb", Source: "fork", Target: "workb"},
			{ID: "f-worka-end", Source: "worka", Target: "enda"},
			{ID: "f-bnd-recovered", Source: "bnd", Target: "recovered"},
			{ID: "f-workb-boom", Source: "workb", Target: "boom"},
		},
	}

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-surviving-sibling-failed"},
		engine.NewStartInstance(terminalT0, nil), engine.StepOptions{})
	require.NoError(t, err)

	aCmd := invokeCommandIDForAction(t, r1.Commands, "a-action")
	bCmd := invokeCommandIDForAction(t, r1.Commands, "b-action")
	require.NotEqual(t, aCmd, bCmd, "setup: the two branches must hold distinct commands")

	// Step 2: branch b runs into an error end with no matching boundary and no
	// compensation records — immediate failure that KEEPS every token.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(terminalT0.Add(time.Second), bCmd, nil), engine.StepOptions{})
	require.NoError(t, err)

	require.Equal(t, engine.StatusFailed, r2.State.Status,
		"control: the unhandled error must have FAILED the instance")
	var survivor bool
	for _, tok := range r2.State.Tokens {
		if tok.AwaitCommand == aCmd {
			survivor = true
		}
	}
	require.True(t, survivor,
		"control: branch a's token must survive the failure with its AwaitCommand intact")

	// Step 3: branch a's own action, dispatched back in step 1, finally FAILS
	// against the now-FAILED instance — with an error its boundary catches.
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewActionFailed(terminalT0.Add(2*time.Second), aCmd, "E-RECOVER", false), engine.StepOptions{})
	require.NoError(t, err)

	assert.Equal(t, engine.StatusFailed, r3.State.Status,
		"a failed instance must not be resurrected — let alone flipped to Completed — "+
			"by a sibling's in-flight ActionFailed routed through its error boundary")
	assert.Empty(t, r3.Commands,
		"a terminal instance awaits nothing, so the stray failure must emit no command — "+
			"neither a recovery route nor a duplicate FailInstance")
}

// invokeCommandIDForAction returns the CommandID of the single InvokeAction in
// cmds whose action name is name, failing the test when there is not exactly
// one. Unlike firstInvokeCommandID it discriminates between concurrently
// dispatched branches, which is required whenever a fork parks two tokens on
// two different actions in the same step.
func invokeCommandIDForAction(t *testing.T, cmds []engine.Command, name string) string {
	t.Helper()

	var found []string
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == name {
			found = append(found, ia.CommandID)
		}
	}
	require.Len(t, found, 1, "expected exactly one InvokeAction for %q in %v", name, commandTypeNames(cmds))
	return found[0]
}

// nonInterruptingRootTimerEventSubprocessDef builds
//
//	root:  start → hold(UserTask) → root-end
//	       [root-level NON-INTERRUPTING event sub-process "root-esp", timer "1h"]
//	root-esp: esp-start(timer "1h", non-interrupting) → esp-end
//
// The event sub-process body drains in the same Step it fires in, WHILE the root
// scope still holds the "hold" token — which is the whole point: that drain hits
// exitRootEventSubprocessScope's `tokensInScope("") > 0` early return, so nothing
// on the event-sub-process exit path ever retires the arm. Completing "hold"
// later drives the root token to root-end, and the instance completes through
// exitRootScope — the path that swept nothing before ADR-0164.
func nonInterruptingRootTimerEventSubprocessDef() *model.ProcessDefinition {
	espBody := &model.ProcessDefinition{
		ID: "ni-root-timer-esp-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("esp-start",
				event.WithStartTimer(schedule.AfterExpr(`"1h"`)),
				event.WithNonInterrupting()),
			event.NewEnd("esp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "ef1", Source: "esp-start", Target: "esp-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "ni-root-timer-esp", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("hold", activity.WithEligibleRoles("ops")),
			event.NewEnd("root-end"),
			activity.NewSubProcess("root-esp", espBody),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "hold"},
			{ID: "f2", Source: "hold", Target: "root-end"},
		},
	}
}

// TestCompletionRetiresNonInterruptingRootEventSubprocessArm pins ADR-0164's
// normalization of NORMAL completion: since every terminal site routes through
// endInstance, whose cancelAllScheduledWork retires every arm the instance still
// owns, a completed instance no longer carries a live root-level
// non-interrupting event-sub-process arm (nor its scheduled timer). ADR-0124's
// repeatability decision is untouched — the arm stays armed for as long as the
// instance runs; only its survival INTO a terminal snapshot is withdrawn.
//
// The scenario is pinned to the exitRootScope completion path, and that pinning
// is the test's whole value. exitRootEventSubprocessScope's own completion tail
// already retired root-scope arms before this delivery, so an instance completed
// through THAT path would show an empty arm list with the endInstance sweep
// deleted — a test that certifies nothing. Here the event sub-process fires and
// drains while the root scope still holds the "hold" token, so its exit takes the
// `tokensInScope("") > 0` early return and leaves the arm alone (asserted below,
// as the load-bearing control); the instance only completes one Step later, when
// the root token reaches root-end via exitRootScope.
func TestCompletionRetiresNonInterruptingRootEventSubprocessArm(t *testing.T) {
	t.Parallel()

	def := nonInterruptingRootTimerEventSubprocessDef()
	actor := authz.Actor{ID: "alice", Roles: []string{"ops"}}

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-ni-root-esp"},
		engine.NewStartInstance(terminalT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.EventTriggeredSubprocesses, 1,
		"setup: the root-level event sub-process arm must be armed")
	var armTimerID string
	for _, c := range r1.Commands {
		if st, ok := c.(engine.ScheduleTimer); ok {
			armTimerID = st.TimerID
		}
	}
	require.NotEmpty(t, armTimerID, "setup: the timer arm must have been scheduled")
	require.Len(t, r1.State.Tasks, 1, "setup: the root scope must be parked on the hold task")

	// Fire the arm. Non-interrupting: the root token is untouched, a child scope
	// runs the body, and the body drains within this same Step.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewTimerFired(terminalT0.Add(time.Hour), armTimerID), engine.StepOptions{})
	require.NoError(t, err)

	// THE control that pins the path. If the event-sub-process exit had retired
	// the arm here, the completion assertion below would pass with the endInstance
	// sweep deleted and this test would certify nothing.
	require.Len(t, r2.State.EventTriggeredSubprocesses, 1,
		"control: the non-interrupting arm must SURVIVE its own fire — the root scope still holds a token, so exitRootEventSubprocessScope returns early and retires nothing")
	require.Equal(t, engine.StatusRunning, r2.State.Status,
		"control: the instance must still be running after the body drains")
	require.Empty(t, r2.State.Scopes,
		"control: the event sub-process child scope must already be closed, so the completion below cannot come from an event-sub-process exit")
	for _, c := range r2.Commands {
		if ct, ok := c.(engine.CancelTimer); ok {
			require.NotEqual(t, armTimerID, ct.TimerID,
				"control: nothing on the event-sub-process exit path may cancel the arm's timer")
		}
	}

	// Complete the hold task: the root token advances to root-end and the
	// instance completes through exitRootScope.
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewHumanCompleted(terminalT0.Add(2*time.Hour), r2.State.Tasks[0].TaskID,
			engine.CompletionInput{}, actor), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, r3.State.Status,
		"control: the root token reaching root-end must complete the instance")

	assert.Empty(t, r3.State.EventTriggeredSubprocesses,
		"a completed instance must not carry a live event-sub-process arm")
	var cancelled bool
	for _, c := range r3.Commands {
		if ct, ok := c.(engine.CancelTimer); ok && ct.TimerID == armTimerID {
			cancelled = true
		}
	}
	assert.True(t, cancelled,
		"completion must emit CancelTimer for the retired arm's scheduled timer")
}

// commandTypeNames returns the concrete Go type name of each command, in emitted
// order. The terminal-order assertions in this file are about WHICH command kind
// sits where — [task cancels…, terminal, scheduled-work cancels…] — not about the
// payloads, which the surrounding assertions cover.
func commandTypeNames(cmds []engine.Command) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, fmt.Sprintf("%T", c))
	}
	return out
}

// nestedEventSubprocessNoOutgoingFlowDef builds
//
//	root:  start → outer(SubProcess)         ← "outer" has NO outgoing flow
//	       [root-level event sub-process "root-esp", timer "1h", never fired]
//	outer: outer-start → gate(UserTask) → outer-end
//	       [nested NON-INTERRUPTING event sub-process "n-esp", signal "boom"]
//	n-esp: n-esp-start(signal "boom", non-interrupting) → n-esp-svc(ServiceTask) → n-esp-end
//
// Two properties make it reach the branch under test. First, "outer" has no
// outgoing sequence flow, so resumeInParentScope finds nothing to resume when the
// enclosing scope is finally closed. Second, the nested event sub-process
// OUTLIVES that enclosing scope: "gate" completes and the outer scope drains, but
// exitRegularSubprocessScope holds it open while the event sub-process runs
// alongside — so it is exitNestedEventSubprocessScope, not the regular exit, that
// prunes the enclosing scope and hits the `grandparentScopeID == "" &&
// len(Tokens) == 0` completion block.
//
// "root-esp" exists only to leave ONE piece of live scheduler work at completion,
// so the CancelTimer tail of the terminal order is observable. The event
// sub-process body parks on a ServiceTask rather than a user task so that the
// final Step is an ActionCompleted: a HumanCompleted would emit an UpdateTask of
// its own and blunt the exact-order assertion below.
func nestedEventSubprocessNoOutgoingFlowDef() *model.ProcessDefinition {
	nespBody := &model.ProcessDefinition{
		ID: "no-out-nesp-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("n-esp-start", event.WithSignalName("boom"), event.WithNonInterrupting()),
			activity.NewServiceTask("n-esp-svc", activity.WithTaskAction("n-esp-action")),
			event.NewEnd("n-esp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "nf1", Source: "n-esp-start", Target: "n-esp-svc"},
			{ID: "nf2", Source: "n-esp-svc", Target: "n-esp-end"},
		},
	}
	outer := &model.ProcessDefinition{
		ID: "no-out-outer-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			activity.NewUserTask("gate", activity.WithEligibleRoles("ops")),
			event.NewEnd("outer-end"),
			activity.NewSubProcess("n-esp", nespBody),
		},
		Flows: []flow.SequenceFlow{
			{ID: "of1", Source: "outer-start", Target: "gate"},
			{ID: "of2", Source: "gate", Target: "outer-end"},
		},
	}
	rootESPBody := &model.ProcessDefinition{
		ID: "no-out-root-esp-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("root-esp-start",
				event.WithStartTimer(schedule.AfterExpr(`"1h"`)),
				event.WithNonInterrupting()),
			event.NewEnd("root-esp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "rf1", Source: "root-esp-start", Target: "root-esp-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "nested-esp-no-outgoing", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("outer", outer),
			activity.NewSubProcess("root-esp", rootESPBody),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "outer"},
		},
	}
}

// TestNestedEventSubprocessRootExitWithNoOutgoingFlowCompletes pins the third
// terminal site ADR-0164 rerouted through endInstance: the completion block
// inside exitNestedEventSubprocessScope, reached when the enclosing sub-process
// node sits at the ROOT scope and has no outgoing sequence flow, so the exit
// completes the instance instead of resuming it. Phase 1's task review found that
// block 0-covered both before and after the rewrite — its behaviour-equivalence
// rested on inspection alone.
//
// The instance must complete with the canonical terminal command order
// [task cancels…, terminal, scheduled-work cancels…] (ADR-0164 Decision 1), which
// is what distinguishes the endInstance routing from the pre-delivery inline
// sequence at this site.
//
// The open task is INJECTED rather than driven. No production path reaches this
// completion with an open task: it requires zero tokens, and every path that
// removes a token from a live instance goes through cancelTokenWaits, which
// cancels the task attached to it (ADR-0163). The record is appended directly for
// the same reason TestTerminalTransitionKeepsIncidentWithEmptyTokenID assembles
// its incident by hand — the sweep's position in the emitted order is a contract
// this site owes every future caller, and it is unobservable otherwise.
func TestNestedEventSubprocessRootExitWithNoOutgoingFlowCompletes(t *testing.T) {
	t.Parallel()

	def := nestedEventSubprocessNoOutgoingFlowDef()
	actor := authz.Actor{ID: "alice", Roles: []string{"ops"}}

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-nested-esp-no-out"},
		engine.NewStartInstance(terminalT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	var rootArmTimerID string
	for _, c := range r1.Commands {
		if st, ok := c.(engine.ScheduleTimer); ok {
			rootArmTimerID = st.TimerID
		}
	}
	require.NotEmpty(t, rootArmTimerID, "setup: the root-level timer arm must be scheduled")
	require.Len(t, r1.State.Scopes, 1, "setup: the outer sub-process scope must be open")

	// 1. Fire the nested non-interrupting event sub-process; it parks on its own
	//    service task, alongside the still-running outer scope.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(terminalT0.Add(time.Minute), "boom", nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.State.Scopes, 2,
		"setup: the event sub-process scope must run ALONGSIDE the outer scope")
	nespCmdID := firstInvokeCommandID(t, r2.Commands)

	// 2. Complete "gate": the outer scope drains but is held open by the event
	//    sub-process running alongside it.
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewHumanCompleted(terminalT0.Add(2*time.Minute), taskIDForNode(t, r2.State, "gate"),
			engine.CompletionInput{}, actor), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r3.State.Scopes, 2,
		"control: the outer scope must be HELD OPEN by the event sub-process, so the event-sub-process exit is what closes it")
	require.Equal(t, engine.StatusRunning, r3.State.Status)

	// Inject the open task (see the doc comment above).
	st := r3.State
	st.Tasks = append(st.Tasks, humantask.HumanTask{
		TaskID:     "orphan-task",
		InstanceID: st.InstanceID,
		NodeID:     "gate",
		State:      humantask.Unclaimed,
		CreatedAt:  terminalT0,
	})

	// 3. Complete the event sub-process. Its exit closes the enclosing scope,
	//    finds no outgoing flow for "outer" in the ROOT definition, and completes
	//    the instance.
	r4, err := engine.Step(t.Context(), def, st,
		engine.NewActionCompleted(terminalT0.Add(3*time.Minute), nespCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)

	assert.Equal(t, engine.StatusCompleted, r4.State.Status,
		"a root-level enclosing sub-process with no outgoing flow completes the instance on event-sub-process exit")
	assert.Empty(t, r4.State.Tokens, "a completed instance carries no tokens")
	assert.Empty(t, r4.State.Scopes, "both the event sub-process and the enclosing scope must be closed")

	assert.Equal(t,
		[]string{"engine.UpdateTask", "engine.CompleteInstance", "engine.CancelTimer"},
		commandTypeNames(r4.Commands),
		"the terminal order is [task cancels…, terminal, scheduled-work cancels…]")
	uts := findUpdateTasks(r4.Commands)
	require.Len(t, uts, 1)
	assert.Equal(t, "orphan-task", uts[0].Task.TaskID)
	assert.Equal(t, humantask.Cancelled, uts[0].Task.State,
		"endInstance's task sweep must reconcile the open task at this site too")
	for _, c := range r4.Commands {
		if ct, ok := c.(engine.CancelTimer); ok {
			assert.Equal(t, rootArmTimerID, ct.TimerID,
				"the scheduled-work tail must retire the still-live root event-sub-process arm")
		}
	}
}

// subInstanceWithOpenTaskAndArmDef builds
//
//	root: start → fork ⇒ { user(UserTask) ; call(CallActivity "child") } → join → end
//	      [root-level event sub-process "root-esp", timer "1h", never fired]
//
// The parallel fork gives the terminal transition both things the emitted order
// is about: an OPEN user task parked on the sibling branch, and — via the
// never-fired root-level timer arm — a live piece of scheduler work.
// handleSubInstanceFailed leaves the tokens in place, so nothing else cancels
// either one; endInstance owns both sweeps.
func subInstanceWithOpenTaskAndArmDef() *model.ProcessDefinition {
	espBody := &model.ProcessDefinition{
		ID: "sub-fail-esp-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("sf-esp-start",
				event.WithStartTimer(schedule.AfterExpr(`"1h"`)),
				event.WithNonInterrupting()),
			event.NewEnd("sf-esp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "sef1", Source: "sf-esp-start", Target: "sf-esp-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p-sub-fail-order", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewUserTask("user", activity.WithEligibleRoles("ops")),
			activity.NewCallActivity("call", model.Latest("child")),
			gateway.NewParallel("join"),
			event.NewEnd("end"),
			activity.NewSubProcess("root-esp", espBody),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f0", Source: "start", Target: "fork"},
			{ID: "f1", Source: "fork", Target: "user"},
			{ID: "f2", Source: "fork", Target: "call"},
			{ID: "f3", Source: "user", Target: "join"},
			{ID: "f4", Source: "call", Target: "join"},
			{ID: "f5", Source: "join", Target: "end"},
		},
	}
}

// TestSubInstanceFailedEmitsFailInstanceAfterTaskCancels pins ADR-0164
// Decision 1 at the one terminal site that emitted the other order.
// handleSubInstanceFailed used to emit FailInstance FIRST and only then reconcile
// the human-task projection and cancel the scheduled work; routing it through
// endInstance moved the terminal command to the canonical position, so all eight
// sites now emit [task cancels…, terminal, scheduled-work cancels…].
//
// The order is a real contract, not cosmetics: a consumer relaying these commands
// in sequence would otherwise publish instance.failed while its own task
// projection still showed the sibling branch's task open.
func TestSubInstanceFailedEmitsFailInstanceAfterTaskCancels(t *testing.T) {
	t.Parallel()

	def := subInstanceWithOpenTaskAndArmDef()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-sub-fail-order"},
		engine.NewStartInstance(terminalT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.Tasks, 1, "setup: the sibling branch must be parked on a user task")
	require.True(t, r1.State.Tasks[0].IsOpen(), "setup: that task must still be open")
	var ssiCmdID, armTimerID string
	for _, c := range r1.Commands {
		switch cmd := c.(type) {
		case engine.StartSubInstance:
			ssiCmdID = cmd.CommandID
		case engine.ScheduleTimer:
			armTimerID = cmd.TimerID
		}
	}
	require.NotEmpty(t, ssiCmdID, "setup: the call activity must have emitted StartSubInstance")
	require.NotEmpty(t, armTimerID, "setup: the root-level timer arm must be scheduled")

	// The child instance fails, and the call-activity node carries no matching
	// error boundary, so the parent fails.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSubInstanceFailed(terminalT0.Add(time.Minute), ssiCmdID, "child blew up"),
		engine.StepOptions{})
	require.NoError(t, err)

	require.Equal(t, engine.StatusFailed, r2.State.Status,
		"control: an unmatched child failure must fail the parent")
	require.NotEmpty(t, r2.State.Tokens,
		"control: this terminal path leaves the tokens in place — the task sweep is endInstance's, not cancelTokenWaits'")

	assert.Equal(t,
		[]string{"engine.UpdateTask", "engine.FailInstance", "engine.CancelTimer"},
		commandTypeNames(r2.Commands),
		"FailInstance must sit AFTER the task cancels and BEFORE the scheduled-work cancels")
	uts := findUpdateTasks(r2.Commands)
	require.Len(t, uts, 1)
	assert.Equal(t, humantask.Cancelled, uts[0].Task.State,
		"the sibling branch's parked task must be reconciled to Cancelled")
	for _, c := range r2.Commands {
		if ct, ok := c.(engine.CancelTimer); ok {
			assert.Equal(t, armTimerID, ct.TimerID)
		}
	}
}

// terminalSurvivingSiblingDef builds the shared shape for the three
// resumption-guard regression tests below:
//
//	start → fork ⇒ { survivor(extraNodes[0]) → survivor-end   (parks, declared FIRST)
//	                 hold(ServiceTask "hold-action") → boom(error end E9, no boundary) }
//
// The "hold" branch is what turns the instance terminal, in a SEPARATE Step from
// the one that parks the survivor. That separation is load-bearing: it makes the
// survivor's command genuinely in flight ACROSS steps, exactly as a real
// dispatched action or a real child instance is, so ADR-0161's same-step
// liveAwaiters filter cannot be what suppresses it.
//
// The failure is an error end event with no matching boundary and no
// compensation records, so it takes handleUnhandledError's immediate-fail
// branch — the one terminal path that sets StatusFailed while LEAVING every
// sibling token in place with its AwaitCommand intact.
func terminalSurvivingSiblingDef(id string, extraNodes []model.Node, extraFlows []flow.SequenceFlow) *model.ProcessDefinition {
	nodes := []model.Node{
		event.NewStart("start"),
		gateway.NewParallel("fork"),
		event.NewEnd("survivor-end"),
		activity.NewServiceTask("hold", activity.WithTaskAction("hold-action")),
		event.NewEnd("boom", event.WithErrorCode("E9")),
	}
	nodes = append(nodes, extraNodes...)
	flows := []flow.SequenceFlow{
		{ID: "f-start", Source: "start", Target: "fork"},
		{ID: "f-hold", Source: "fork", Target: "hold"},
		{ID: "f-hold-boom", Source: "hold", Target: "boom"},
	}
	flows = append(flows, extraFlows...)
	return &model.ProcessDefinition{ID: id, Version: 1, Nodes: nodes, Flows: flows}
}

// driveToFailedWithSurvivor runs the shared two-step setup and returns the
// terminal state. It asserts both controls the regression tests depend on: the
// instance really is StatusFailed, and the survivor's token really did outlive
// the transition still awaiting survivorCmd.
func driveToFailedWithSurvivor(t *testing.T, def *model.ProcessDefinition, instanceID string, r1 engine.StepResult, survivorCmd string) engine.InstanceState {
	t.Helper()

	holdCmd := invokeCommandIDForAction(t, r1.Commands, "hold-action")
	require.NotEqual(t, survivorCmd, holdCmd, "setup: the two branches must hold distinct commands")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(terminalT0.Add(time.Second), holdCmd, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusFailed, r2.State.Status,
		"control: the unhandled error must have FAILED the instance")

	var survived bool
	for _, tok := range r2.State.Tokens {
		if tok.AwaitCommand == survivorCmd {
			survived = true
		}
	}
	require.True(t, survived,
		"control: the survivor's token must outlive the failure with its AwaitCommand intact — "+
			"that survival is the premise of this whole family of tests")
	return r2.State
}

// TestSubInstanceCompletedOnFailedInstanceWithSurvivingSiblingIsNoOp pins the
// call-activity route.
//
// A fork parks one token on a CallActivity while a sibling fails the instance
// with no boundary and no compensation records, leaving the call token alive
// with its AwaitCommand. The child then completes and runtime/calllink's
// CallNotifier delivers SubInstanceCompleted from DrainOnce — asynchronously,
// with NO parent-status check. Unguarded, handleSubInstanceCompleted merges the
// child's output into a dead instance and calls resumeAndDrive; the token
// reaches its end as the last remaining token and exitRootScope calls
// endInstance(StatusCompleted, …). The FAILED instance is flipped to Completed
// while terminalOutboxEvent's prevStatus.IsTerminal() check suppresses the
// second event — so the persisted status silently disagrees with the
// instance.failed event already published.
func TestSubInstanceCompletedOnFailedInstanceWithSurvivingSiblingIsNoOp(t *testing.T) {
	t.Parallel()

	def := terminalSurvivingSiblingDef("p-terminal-sub-completed",
		[]model.Node{activity.NewCallActivity("call", model.Latest("child"))},
		[]flow.SequenceFlow{
			{ID: "f-call", Source: "fork", Target: "call"},
			{ID: "f-call-end", Source: "call", Target: "survivor-end"},
		},
	)

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-sub-completed"},
		engine.NewStartInstance(terminalT0, nil), engine.StepOptions{})
	require.NoError(t, err)

	var callCmd string
	for _, c := range r1.Commands {
		if ssi, ok := c.(engine.StartSubInstance); ok {
			callCmd = ssi.CommandID
		}
	}
	require.NotEmpty(t, callCmd, "setup: the call activity must have emitted StartSubInstance")

	failed := driveToFailedWithSurvivor(t, def, "i-sub-completed", r1, callCmd)

	// The child completes and the CallNotifier delivers its success to a parent
	// that has been dead since the previous step.
	r3, err := engine.Step(t.Context(), def, failed,
		engine.NewSubInstanceCompleted(terminalT0.Add(2*time.Second), callCmd, map[string]any{"childOut": 1}),
		engine.StepOptions{})
	require.NoError(t, err)

	assert.Equal(t, engine.StatusFailed, r3.State.Status,
		"a failed instance must not be resurrected — let alone flipped to Completed — "+
			"by a child instance completing after the parent died")
	assert.Empty(t, r3.Commands,
		"a terminal instance awaits nothing, so the stray child completion must emit no command")
	assert.NotContains(t, r3.State.Variables, "childOut",
		"a dead parent must not absorb the child's output either — mergeVars must never run")
}

// TestSubInstanceFailedOnFailedInstanceWithSurvivingSiblingIsNoOp is the
// symmetric twin: the same delivery path and the same surviving token, but the
// child FAILS.
//
// With a matching error boundary on the call-activity node, handleSubInstanceFailed
// routes to the recovery flow and drives — reaching a normal end as the last
// token and flipping Failed to Completed, exactly as the completion twin does.
// Without a boundary it instead falls into its own endInstance tail and re-runs
// a terminal transition on an already-terminal instance, overwriting EndedAt
// with a later timestamp and emitting a DUPLICATE FailInstance. The boundary
// variant is pinned here because it produces the silent status flip rather than
// a duplicate command; EndedAt is asserted directly, which catches the
// no-boundary variant's damage too.
func TestSubInstanceFailedOnFailedInstanceWithSurvivingSiblingIsNoOp(t *testing.T) {
	t.Parallel()

	def := terminalSurvivingSiblingDef("p-terminal-sub-failed",
		[]model.Node{
			activity.NewCallActivity("call", model.Latest("child")),
			event.NewBoundary("call-bnd", "call", event.WithBoundaryErrorCode("E-CHILD")),
		},
		[]flow.SequenceFlow{
			{ID: "f-call", Source: "fork", Target: "call"},
			{ID: "f-call-end", Source: "call", Target: "survivor-end"},
			{ID: "f-call-bnd", Source: "call-bnd", Target: "survivor-end"},
		},
	)

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-sub-failed"},
		engine.NewStartInstance(terminalT0, nil), engine.StepOptions{})
	require.NoError(t, err)

	var callCmd string
	for _, c := range r1.Commands {
		if ssi, ok := c.(engine.StartSubInstance); ok {
			callCmd = ssi.CommandID
		}
	}
	require.NotEmpty(t, callCmd, "setup: the call activity must have emitted StartSubInstance")

	failed := driveToFailedWithSurvivor(t, def, "i-sub-failed", r1, callCmd)
	require.NotNil(t, failed.EndedAt, "control: the failure must have stamped EndedAt")
	endedAt := *failed.EndedAt

	r3, err := engine.Step(t.Context(), def, failed,
		engine.NewSubInstanceFailed(terminalT0.Add(2*time.Second), callCmd, "E-CHILD"),
		engine.StepOptions{})
	require.NoError(t, err)

	assert.Equal(t, engine.StatusFailed, r3.State.Status,
		"a failed instance must not be resurrected by a child failing after the parent died")
	assert.Empty(t, r3.Commands,
		"the stray child failure must emit no command — neither a boundary recovery route "+
			"nor a duplicate FailInstance")
	require.NotNil(t, r3.State.EndedAt)
	assert.Equal(t, endedAt, *r3.State.EndedAt,
		"re-running a terminal transition would overwrite EndedAt with a later timestamp, "+
			"silently rewriting when the instance actually died")
}

// TestResolveIncidentOnFailedInstanceWithSurvivingSiblingIsRejected pins the
// route that ADR-0164's own Decision 3 opened.
//
// removeOrphanedIncidents deliberately KEEPS an incident whose token survived a
// terminal transition. That is exactly the state — StatusFailed, a live
// TokenIncident token, a live incident — in which an admin ResolveIncident is
// still accepted: unguarded, it deletes the incident, flips the token back to
// TokenActive and calls reinvokeServiceAction, so a side-effecting service
// action REALLY RUNS against a dead instance.
//
// The follow-on damage is what makes this worse than a wasted invocation: that
// action's ActionCompleted is then swallowed by the guard ADR-0164 added,
// leaving the token permanently TokenActive on a terminal instance with its
// incident already deleted — it can be neither re-raised nor re-resolved.
// Decision 3's rationale enumerated only READ consumers of s.Incidents; this is
// the WRITE consumer nobody considered.
//
// ⚠ ADR-0165 changed the OUTCOME this test pins, which is why it was rewritten
// (and renamed off "IsNoOp") rather than dropped: ADR-0164 shipped the refusal
// as a SILENT no-op, so an admin was told their resolution worked when it did
// not. ResolveIncident's caller is a synchronous admin API, so ADR-0165
// classifies it rejectWithError and the refusal is now surfaced as
// engine.ErrInstanceTerminal — which wraps ErrInvalidTransition, the
// classification service/ already maps to a conflict. The state post-conditions
// below are unchanged and remain this test's real payload: they are what would
// catch the guard being removed altogether.
func TestResolveIncidentOnFailedInstanceWithSurvivingSiblingIsRejected(t *testing.T) {
	t.Parallel()

	def := terminalSurvivingSiblingDef("p-terminal-resolve-incident",
		[]model.Node{
			activity.NewServiceTask("work",
				activity.WithTaskAction("work-action"),
				activity.WithRetryPolicy(&model.RetryPolicy{MaxAttempts: 1})),
		},
		[]flow.SequenceFlow{
			{ID: "f-work", Source: "fork", Target: "work"},
			{ID: "f-work-end", Source: "work", Target: "survivor-end"},
		},
	)

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-resolve-incident"},
		engine.NewStartInstance(terminalT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	workCmd := invokeCommandIDForAction(t, r1.Commands, "work-action")

	// The work branch exhausts its single attempt and parks as an incident. The
	// instance stays RUNNING (the raiseIncident policy), so this is not yet the
	// state under test.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionFailed(terminalT0.Add(time.Second), workCmd, "work exploded", false),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r2.State.Status,
		"control: retry exhaustion must PARK an incident, not fail the instance")
	require.Len(t, r2.State.Incidents, 1, "control: exactly one incident must be parked")
	incID := r2.State.Incidents[0].ID
	incTokenID := r2.State.Incidents[0].TokenID

	// Now the sibling branch fails the instance, WITHOUT dropping tokens — so
	// Decision 3's narrow sweep keeps the incident whose token survived.
	holdCmd := invokeCommandIDForAction(t, r1.Commands, "hold-action")
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewActionCompleted(terminalT0.Add(2*time.Second), holdCmd, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusFailed, r3.State.Status,
		"control: the unhandled error must have FAILED the instance")
	require.Len(t, r3.State.Incidents, 1,
		"control: Decision 3 KEEPS an incident whose token survived — that retention "+
			"is precisely what makes this route reachable")

	// An admin resolves the incident on the dead instance.
	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewResolveIncident(terminalT0.Add(3*time.Second), incID, 3), engine.StepOptions{})

	assert.ErrorIs(t, err, engine.ErrInstanceTerminal,
		"an admin must be TOLD the instance is dead, not handed a silent no-op that reads as success")
	assert.ErrorIs(t, err, engine.ErrInvalidTransition,
		"the refusal must stay classifiable as an invalid transition, or service/ stops mapping it to a conflict")
	assert.Empty(t, r4.Commands,
		"the resolve must NOT re-invoke the stalled service action — a side-effecting "+
			"action must never run against a dead instance")

	// The state the caller is left holding. Asserted through committedAfter (see
	// step_terminal_policy_test.go) rather than r4.State: Step returns the ZERO
	// StepResult on error, so a caller that receives one keeps r3.State. Written
	// this way the post-conditions mean the same thing whether the refusal is
	// silent or surfaced — which is exactly what let them survive ADR-0165's
	// change of outcome unedited.
	after := committedAfter(r3.State, r4, err)
	assert.Equal(t, engine.StatusFailed, after.Status,
		"resolving an incident must not revive a terminal instance")
	assert.Len(t, after.Incidents, 1,
		"the incident must survive the refused resolve, or it can never be re-raised "+
			"nor re-resolved")
	for _, tok := range after.Tokens {
		if tok.ID == incTokenID {
			assert.Equal(t, engine.TokenIncident, tok.State,
				"the token must stay TokenIncident: flipping it to TokenActive on a "+
					"terminal instance strands it permanently")
		}
	}
}
