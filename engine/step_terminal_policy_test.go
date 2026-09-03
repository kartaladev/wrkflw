package engine_test

// step_terminal_policy_test.go — the structural terminal-trigger
// guard. Every test here pins a route by which a trigger delivered against an
// ALREADY-TERMINAL instance is accepted today, and states the behaviour the
// per-trigger terminalPolicy must produce instead.
//
// Two structural notes, documented here rather than argued per test:
//
//  1. One test function per ROUTE, not one table over all of them. The
//     `table-test` skill's mandate is scoped to two or more cases exercising the
//     SAME SUT call; these are different SUT ENTRY POINTS that happen to share
//     engine.Step as their front door. Each trigger type dispatches to its own
//     handler — handleHumanClaimed, handleHumanReassigned, handleHumanCompleted,
//     handleSignalReceived, handleMessageReceived — and each trigger type has
//     its own INDEPENDENTLY DECLARED terminalPolicy(), so there is
//     no shared call whose inputs vary. (This is emphatically NOT the rejected
//     "clearer failure narrative" or "subtly different assertions" excuse:
//     claim/reassign/completed do share a three-line setup, and their shared
//     post-conditions are factored into assertAssignmentRejected precisely so
//     the split costs no duplication.)
//
//     The split is also load-bearing for the delivery's own verification step:
//     each removed guard is mutation-verified by disabling the single dispatch
//     check and requiring a RED attributable to THAT handler. Per-route function
//     names make that attribution mechanical; a fused table would report one
//     failing function for five different handlers.
//
//     Cases WITHIN a function are tabled in the skill's form — see
//     TestStartInstanceRejectedOnTerminalInstance.
//  2. Post-conditions are asserted against committedAfter(...) rather than
//     r.State — see that helper's comment. Asserting on r.State directly would
//     make every "nothing changed" assertion flip meaning at the moment the
//     rejection lands, which is precisely when it must not.

import (
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
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
)

// terminalGuardDefWithWait builds
//
//	start → fork ⇉ { wait → end-a ; svc(ServiceTask "svc-action") }
//
// "svc" carries no retry policy and no boundary, so a non-retryable
// ActionFailed against it takes the fail-fast policy: handleUnhandledError's
// immediate branch fails the instance WITHOUT dropping a single token. That is
// what makes the fixture load-bearing — the wait branch's token survives the
// terminal transition still parked on its waiter, which is the state every
// resurrection route in this file needs. "svc" deliberately has no outgoing
// flow: its token never advances, it only fails.
//
// id doubles as the definition id and (prefixed "i-") the instance id, so the
// three fixtures below mint disjoint, deterministic token/task/command ids.
func terminalGuardDefWithWait(id string, wait model.Node) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      id,
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			wait,
			activity.NewServiceTask("svc", activity.WithTaskAction("svc-action")),
			event.NewEnd("end-a"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fork"},
			{ID: "f2", Source: "fork", Target: wait.ID()},
			{ID: "f3", Source: "fork", Target: "svc"},
			{ID: "f4", Source: wait.ID(), Target: "end-a"},
		},
	}
}

// terminalGuardDef parks the surviving branch on a human task, which is what
// the StartInstance and the three human-task routes need.
func terminalGuardDef() *model.ProcessDefinition {
	return terminalGuardDefWithWait("p-terminal-guard", activity.NewUserTask("approve"))
}

// terminalGuardSignalDef parks the surviving branch on a SIGNAL CATCH instead.
//
// ⚠ This second fixture is not a convenience — it is what makes
// TestSignalReceivedIsNoOpOnTerminalInstance capable of failing at all. On
// terminalGuardDef NO token awaits a signal, so handleSignalReceived matches
// nothing, never reaches its deferred mergeVars, and is ALREADY a clean no-op:
// a signal test built on the human fixture passes before any implementation
// exists. Verified by execution against this base — delivering
// SignalReceived{"approved", {x:1}} to the human fixture's terminal state
// leaves tokens=2, history=4, vars=map[] (i.e. nothing at all happens), while
// on THIS fixture it leaves tokens=1, history=5, vars=map[x:1].
func terminalGuardSignalDef() *model.ProcessDefinition {
	return terminalGuardDefWithWait("p-terminal-guard-signal",
		event.NewIntermediateCatch("await", event.WithSignalName("approved")))
}

// terminalGuardMessageDef is the message-catch variant. The correlation key is
// empty, which tokenAwaitingMessage treats as "uncorrelated" and matches
// against a delivery whose key is also empty.
func terminalGuardMessageDef() *model.ProcessDefinition {
	return terminalGuardDefWithWait("p-terminal-guard-message",
		event.NewIntermediateCatch("await", event.WithMessageCorrelator("approved-msg", "")))
}

// terminalWithSurvivingToken drives def (built by terminalGuardDefWithWait) to
// StatusFailed via a non-retryable failure of "svc", leaving the wait branch's
// token alive and still parked. The preconditions it asserts are controls: if
// the fail path ever starts dropping tokens, every test in this file would go
// vacuously green without them.
func terminalWithSurvivingToken(t *testing.T, def *model.ProcessDefinition) engine.InstanceState {
	t.Helper()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-" + def.ID},
		engine.NewStartInstance(terminalT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	svcCmd := invokeCommandIDForAction(t, r1.Commands, "svc-action")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionFailed(terminalT0.Add(time.Minute), svcCmd, "boom", false), engine.StepOptions{})
	require.NoError(t, err)

	st := r2.State
	require.Equal(t, engine.StatusFailed, st.Status,
		"setup: the unhandled error must have failed the instance")
	require.Len(t, st.Tokens, 2,
		"setup: the fail-fast path must leave BOTH tokens in place, or every route below is vacuous")
	for _, task := range st.Tasks {
		require.Equal(t, humantask.Cancelled, task.State,
			"setup: the terminal transition must have cancelled the open task")
	}

	return st
}

// committedAfter returns the instance state a caller is left holding once this
// Step returns.
//
// engine.Step is pure and returns the ZERO StepResult on error, so a caller
// that receives an error keeps its own `before` state while a caller that
// receives none commits r.State. Writing "the trigger changed nothing" against
// this value is what lets ONE assertion mean the same thing on both sides of
// the change: today the trigger is wrongly APPLIED and r.State carries the
// damage (so the assertion fires), and once guarded it is REJECTED and the
// caller keeps `before` (so the assertion holds). Asserting on r.State directly
// would instead compare against a zero InstanceState the moment the guard
// lands, which is not the property under test.
func committedAfter(before engine.InstanceState, r engine.StepResult, err error) engine.InstanceState {
	if err != nil {
		return before
	}
	return r.State
}

// historyNodeIDs returns the node ids of every visit recorded on s, in order.
func historyNodeIDs(s engine.InstanceState) []string {
	ids := make([]string, 0, len(s.History))
	for _, v := range s.History {
		ids = append(ids, v.NodeID)
	}
	return ids
}

// TestStartInstanceRejectedOnTerminalInstance pins the loudest route: nothing
// in handleStartInstance looks at s.Status, so a StartInstance delivered
// against a terminal instance re-initialises it in place — stamping
// StatusRunning over StatusFailed while leaving the OLD EndedAt behind, placing
// a SECOND start token beside the survivors, and minting a second copy of every
// task the definition opens.
//
// Observed against this base (terminal baseline: Running=no, tokens=2, tasks=1,
// history=4):
//   - manual start:  err=nil, Status=Running, tokens=4, tasks=2, history=8, 2 commands.
//   - start at node: err=nil, Status=Running, tokens=3,          history=5, 1 command.
func TestStartInstanceRejectedOnTerminalInstance(t *testing.T) {
	t.Parallel()

	def := terminalGuardDef()
	st := terminalWithSurvivingToken(t, def)

	type testCase struct {
		name    string
		trigger engine.Trigger
		assert  func(t *testing.T, r engine.StepResult, err error)
	}

	assertRejected := func(t *testing.T, r engine.StepResult, err error) {
		t.Helper()

		assert.ErrorIs(t, err, engine.ErrInstanceTerminal,
			"a terminal instance must not be restarted")
		assert.Empty(t, r.Commands,
			"a rejected StartInstance must dispatch nothing — today it re-invokes the whole definition")

		after := committedAfter(st, r, err)
		assert.Equal(t, engine.StatusFailed, after.Status,
			"the terminal status must survive the rejected restart")
		assert.Len(t, after.Tokens, len(st.Tokens),
			"a rejected restart must place no second start token")
		assert.Len(t, after.Tasks, len(st.Tasks),
			"a rejected restart must mint no second copy of the definition's tasks")
		assert.Equal(t, historyNodeIDs(st), historyNodeIDs(after),
			"a rejected restart must append no post-mortem visit")
	}

	cases := []testCase{
		{
			name:    "manual_start",
			trigger: engine.NewStartInstance(terminalT0.Add(time.Hour), nil),
			assert:  assertRejected,
		},
		{
			// The restart-at-node facade reaches the same handler and is
			// equally unguarded; it re-enters the definition mid-graph.
			name:    "start_at_node",
			trigger: engine.NewStartInstanceAtNode(terminalT0.Add(time.Hour), "svc", nil),
			assert:  assertRejected,
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

// assertAssignmentRejected holds the post-conditions the claim and reassign
// routes share: the sentinel, no UpdateTask escaping to the consumer's
// TaskStore, and the closed task left exactly as the terminal transition left
// it. before is the terminal baseline the trigger was delivered against.
func assertAssignmentRejected(t *testing.T, before engine.InstanceState, taskID string, r engine.StepResult, err error) {
	t.Helper()

	assert.ErrorIs(t, err, engine.ErrInstanceTerminal,
		"a terminal instance must not accept an assignment change")
	assert.Empty(t, r.Commands,
		"a rejected assignment must emit no UpdateTask — today the cancelled task is republished as claimed")

	after := committedAfter(before, r, err)
	task := after.TaskByID(taskID)
	require.NotNil(t, task, "the task record must still be there")
	assert.Equal(t, humantask.Cancelled, task.State,
		"the closed task must stay closed")
	assert.Nil(t, task.Claim,
		"a rejected assignment must record no claimant on a closed task")
}

// TestHumanClaimedRejectedOnTerminalInstance pins the claim route.
// handleHumanClaimed looks the task up by id and writes to it unconditionally —
// no status check, and (unlike its sibling handleHumanCandidatesResolved) no
// IsOpen check either. The task it finds on a terminal instance is the
// CANCELLED one the terminal transition closed, so the claim revives a closed
// audit record and emits an UpdateTask that the runtime persists to the
// consumer's TaskStore.
//
// Observed against this base: err=nil, 1 command, and the task flips
// cancelled → claimed with Claim.Actor.ID == "alice".
func TestHumanClaimedRejectedOnTerminalInstance(t *testing.T) {
	t.Parallel()

	def := terminalGuardDef()
	st := terminalWithSurvivingToken(t, def)
	require.Len(t, st.Tasks, 1, "setup: the human branch must have opened exactly one task")
	taskID := st.Tasks[0].TaskID

	r, err := engine.Step(t.Context(), def, st,
		engine.NewHumanClaimed(terminalT0.Add(time.Hour), taskID, authz.Actor{ID: "alice"}),
		engine.StepOptions{})

	assertAssignmentRejected(t, st, taskID, r, err)
}

// TestHumanReassignedRejectedOnTerminalInstance pins the reassign route, which
// is classified as its own trigger. It is the same unguarded write as the
// claim route with one extra hazard: the trigger identifies the new assignee by
// ID only, so a successful reassign against a dead instance FORGES a claim for
// an actor nobody authorised.
//
// Observed against this base: err=nil, 1 command, task cancelled → claimed with
// Claim.Actor.ID == "bob".
func TestHumanReassignedRejectedOnTerminalInstance(t *testing.T) {
	t.Parallel()

	def := terminalGuardDef()
	st := terminalWithSurvivingToken(t, def)
	require.Len(t, st.Tasks, 1, "setup: the human branch must have opened exactly one task")
	taskID := st.Tasks[0].TaskID

	r, err := engine.Step(t.Context(), def, st,
		engine.NewHumanReassigned(terminalT0.Add(time.Hour), taskID, "alice", "bob", authz.Actor{ID: "root"}),
		engine.StepOptions{})

	assertAssignmentRejected(t, st, taskID, r, err)
}

// TestHumanCompletedRejectedOnTerminalInstance pins the worst of the human
// routes. handleHumanCompleted resolves the token by tokenAwaiting(t.TaskID),
// which matches on AwaitCommand ALONE with no status check; the surviving
// token is handed straight back, the handler advances it, and drive — which has
// no status guard either — walks it to "end-a". On this fixture the instance
// stays Failed only because the sibling "svc" token is still parked; with one
// token instead of two the same route flips a FAILED instance to Completed.
//
// The history assertion is the load-bearing one: it proves no POST-MORTEM VISIT
// was appended. Observed against this base — err=nil, 1 command, tokens 2→1,
// task cancelled → completed, and history 4→5 with the fifth visit being
// "end-a", i.e. the dead instance walked to its end event.
func TestHumanCompletedRejectedOnTerminalInstance(t *testing.T) {
	t.Parallel()

	def := terminalGuardDef()
	st := terminalWithSurvivingToken(t, def)
	require.Len(t, st.Tasks, 1, "setup: the human branch must have opened exactly one task")
	taskID := st.Tasks[0].TaskID
	require.NotContains(t, historyNodeIDs(st), "end-a",
		"setup: the terminal state must not already have visited the end event")

	r, err := engine.Step(t.Context(), def, st,
		engine.NewHumanCompleted(terminalT0.Add(time.Hour), taskID, engine.CompletionInput{}, authz.Actor{ID: "alice"}),
		engine.StepOptions{})

	assert.ErrorIs(t, err, engine.ErrInstanceTerminal,
		"a terminal instance must not accept a task completion")
	assert.Empty(t, r.Commands,
		"a rejected completion must emit no UpdateTask and no downstream dispatch")

	after := committedAfter(st, r, err)
	assert.Equal(t, historyNodeIDs(st), historyNodeIDs(after),
		"a rejected completion must append no post-mortem visit — today the token walks on to end-a")
	assert.NotContains(t, historyNodeIDs(after), "end-a",
		"a dead instance must never reach its end event")
	assert.Len(t, after.Tokens, len(st.Tokens),
		"a rejected completion must not consume the surviving token")

	task := after.TaskByID(taskID)
	require.NotNil(t, task, "the task record must still be there")
	assert.Equal(t, humantask.Cancelled, task.State,
		"the cancelled task must not be re-closed as completed")
}

// TestSignalReceivedIsNoOpOnTerminalInstance pins the broadcast route, and it
// runs on terminalGuardSignalDef for the reason spelled out on that fixture:
// on the plain fixture no token awaits a signal, so the test would pass before
// any implementation exists.
//
// SignalReceived stays SILENT rather than erroring:
// signalbus.Publish errors.Joins its fan-out, so one terminal target would fail
// an entire broadcast. The failure today is therefore in the STATE, not the
// error. Observed against this base — err=nil, 0 commands, but tokens 2→1,
// history 4→5, and Variables map[] → map[x:1]: the signal resumed the parked
// token, merged the publisher's payload into a dead instance's variables, and
// drove it to "end-a".
func TestSignalReceivedIsNoOpOnTerminalInstance(t *testing.T) {
	t.Parallel()

	def := terminalGuardSignalDef()
	st := terminalWithSurvivingToken(t, def)
	require.NotContains(t, historyNodeIDs(st), "end-a",
		"setup: the terminal state must not already have visited the end event")

	r, err := engine.Step(t.Context(), def, st,
		engine.NewSignalReceived(terminalT0.Add(time.Hour), "approved", map[string]any{"x": 1}),
		engine.StepOptions{})

	require.NoError(t, err,
		"a signal against a terminal instance must stay silent: signalbus joins its fan-out errors")
	assert.Empty(t, r.Commands, "a silently-dropped signal must dispatch nothing")

	after := committedAfter(st, r, err)
	assert.Equal(t, st.Variables, after.Variables,
		"a dropped signal must not merge the publisher's payload into a dead instance")
	assert.Len(t, after.Tokens, len(st.Tokens),
		"a dropped signal must not resume the parked token")
	assert.Equal(t, historyNodeIDs(st), historyNodeIDs(after),
		"a dropped signal must append no post-mortem visit")
	assert.Equal(t, engine.StatusFailed, after.Status,
		"a dropped signal must leave the terminal status alone")
}

// TestMessageReceivedIsNoOpOnTerminalInstance is the point-to-point twin. This
// route was argued by analogy with the signal one rather than from a
// reproduction; it reproduces identically on this fixture.
//
// Observed against this base — err=nil, 0 commands, tokens 2→1, history 4→5,
// Variables map[] → map[x:1].
func TestMessageReceivedIsNoOpOnTerminalInstance(t *testing.T) {
	t.Parallel()

	def := terminalGuardMessageDef()
	st := terminalWithSurvivingToken(t, def)
	require.NotContains(t, historyNodeIDs(st), "end-a",
		"setup: the terminal state must not already have visited the end event")

	r, err := engine.Step(t.Context(), def, st,
		engine.NewMessageReceived(terminalT0.Add(time.Hour), "approved-msg", "", map[string]any{"x": 1}),
		engine.StepOptions{})

	require.NoError(t, err,
		"a message against a terminal instance must stay silent, like its signal twin")
	assert.Empty(t, r.Commands, "a silently-dropped message must dispatch nothing")

	after := committedAfter(st, r, err)
	assert.Equal(t, st.Variables, after.Variables,
		"a dropped message must not merge its payload into a dead instance")
	assert.Len(t, after.Tokens, len(st.Tokens),
		"a dropped message must not resume the parked token")
	assert.Equal(t, historyNodeIDs(st), historyNodeIDs(after),
		"a dropped message must append no post-mortem visit")
	assert.Equal(t, engine.StatusFailed, after.Status,
		"a dropped message must leave the terminal status alone")
}

// TestResolveIncidentRejectedOnTerminalInstance pins the one behaviour change in
// this delivery that is an owner decision rather than a reproduced defect.
// handleResolveIncident ALREADY refuses a terminal instance — but silently, so
// an admin who resolves an incident on a dead instance is told it worked and
// nothing happens. ResolveIncident is classified as rejectWithError because its
// caller is a synchronous admin API, and ErrInstanceTerminal wraps
// ErrInvalidTransition, which service/ already re-classifies as ErrConflict and
// transport/http/httpcore already maps to 422.
//
// Observed against this base: err=nil (a silent no-op) with the state already
// correctly untouched. The error IS the whole assertion here — the state
// assertions below are controls that the existing silent guard is not
// regressed while the error is added.
func TestResolveIncidentRejectedOnTerminalInstance(t *testing.T) {
	t.Parallel()

	// terminalIncidentFork + raiseIncidentOnFlaky (step_terminal_test.go) build
	// the only state in which this route is reachable at all: the
	// removeOrphanedIncidents sweep deliberately KEEPS an incident whose token
	// survived the terminal transition, which is exactly what an admin can still
	// try to resolve. The sibling branch is a plain ServiceTask with no retry
	// policy, so failing it fails the instance without dropping tokens.
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

	raised, cmdIDs := raiseIncidentOnFlaky(t, def, "i-terminal-policy-incident")
	require.Contains(t, cmdIDs, "doomed-action", "setup: the doomed branch must have been invoked")

	terminal, err := engine.Step(t.Context(), def, raised,
		engine.NewActionFailed(terminalT0.Add(time.Hour), cmdIDs["doomed-action"], "fatal", false),
		engine.StepOptions{})
	require.NoError(t, err)

	st := terminal.State
	require.Equal(t, engine.StatusFailed, st.Status, "setup: the instance must be terminal")
	require.Len(t, st.Incidents, 1, "setup: the surviving token's incident must have been kept")
	incidentID := st.Incidents[0].ID

	r, err := engine.Step(t.Context(), def, st,
		engine.NewResolveIncident(terminalT0.Add(2*time.Hour), incidentID, 1), engine.StepOptions{})

	assert.ErrorIs(t, err, engine.ErrInstanceTerminal,
		"an admin resolving an incident on a dead instance must be TOLD, not silently ignored")
	assert.Empty(t, r.Commands,
		"a rejected resolution must not re-invoke the stalled action")

	after := committedAfter(st, r, err)
	assert.Len(t, after.Incidents, len(st.Incidents),
		"a rejected resolution must leave the incident in place")
	assert.Len(t, after.Tokens, len(st.Tokens),
		"a rejected resolution must not revive the parked token")
}

// TestCancelRequestedIsNoOpOnTerminalInstance pins the route the rule-#9 audit
// found. handleCancelRequested has no status guard at all, and forceTerminate
// never clears RootCompensations, so cancelling an already-TERMINATED instance
// whose compensation records survived re-enters beginCompensation: it stamps
// StatusCompensating over the terminal status and re-fires every compensation
// action against a dead instance. terminalOutboxEvent suppresses a second
// terminal event only when prevStatus.IsTerminal(), and by walk end it is
// Compensating — so a SECOND terminal event is published too.
//
// It stays SILENT rather than erroring: propagateCancel's child loop logs and
// swallows every error, so rejectSilently satisfies it identically.
//
// Observed against this base — err=nil, Status terminated → compensating, and 1
// command: InvokeAction{Name: "unship-action"}, a compensation action fired
// against an instance that ended two seconds after it started.
func TestCancelRequestedIsNoOpOnTerminalInstance(t *testing.T) {
	t.Parallel()

	def := resumableForceTerminatedDef()
	st := driveToForceTerminatedWithBothRecords(t, def, "i-terminal-policy-cancel")
	require.Equal(t, engine.StatusTerminated, st.Status,
		"setup: the instance must have force-terminated")

	r, err := engine.Step(t.Context(), def, st,
		engine.NewCancelRequested(terminalT0.Add(time.Hour)), engine.StepOptions{})

	require.NoError(t, err,
		"a cancel against a terminal instance must stay silent: propagateCancel swallows child errors")
	assert.Empty(t, r.Commands,
		"a dropped cancel must re-fire no compensation action against a dead instance")

	after := committedAfter(st, r, err)
	assert.Equal(t, engine.StatusTerminated, after.Status,
		"a dropped cancel must not stamp StatusCompensating over the terminal status")
	assert.Len(t, after.RootCompensations, len(st.RootCompensations),
		"a dropped cancel must leave the surviving compensation records untouched")
	assert.Equal(t, st.EndedAt, after.EndedAt,
		"a dropped cancel must not move EndedAt")
}
