package engine_test

// step_harvest_incident_retirement_test.go — ADR-0174, T12: the harvest retires the
// surviving token's INCIDENT, and that changes a payload consumers read.
//
// ⚠⚠ This test pins an ACCEPTED, INTENDED consequence. It is not a defect report and the
// production code must not be "fixed" to make it green a different way. It exists because
// the consequence is invisible from inside this package's own contracts and CROSSES A
// PACKAGE BOUNDARY:
//
//	runtime/outbox.go's terminalEventErr prefers the Error of the first incident
//	runtime's causeOfDeathIncident allow-list admits — an engine.IncidentAction, which
//	is exactly what this fixture's exhausted branch raises — when building the
//	instance.failed event. Retiring the incident therefore changes both that payload's
//	error string and the incident_count a consumer sees — for the same process, same
//	definition, same failure, across an engine upgrade.
//
// Nothing in runtime/ is touched or asserted here (that is a different package and a
// different test tree). What is pinned is the ENGINE-side state the outbox reads: the
// terminal snapshot's Incidents, which go 1 → 0.
//
// The mechanism, and why the shape is this specific. ADR-0164 Decision 3 deliberately
// PRESERVES a shape where an unhandled error fails the instance WITHOUT dropping tokens, so
// that removeOrphanedIncidents (endInstance) retains the incidents whose token survives.
// ADR-0174's harvest makes handleUnhandledError's records-exist predicate true for a record
// held in an open sub-process scope, so that branch is no longer reached at all: the
// instance enters beginCompensation, whose prologue cancels EVERY token — and cancelling a
// token deletes the incident raised against it.
//
// ⚠ That last clause corrects the spec. §4 M7 attributes the retirement to
// removeOrphanedIncidents, which is called ONLY from endInstance and would therefore put it
// at the walk's finish. Executed, the incident is gone one Step earlier, at the kill:
// cancelTokenWaits calls removeIncidentsForToken per token (engine/step_cancel.go:56).
// M7's end numbers hold; its named mechanism does not, and the assertion below pins the
// measured one.
//
// Measured on THIS fixture, by deleting both harvest call sites and re-running the drive
// (the pre-ADR-0174 control) against the shipped behaviour:
//
//	no harvest: status=failed tokens=3 incidents=1 archived=map[] invoked=[]
//	shipped:    status=failed tokens=0 incidents=0 archived consumed by the walk
//
// ⚠ Spec §4 M7 records tokens 2 → 0 for the same property. That count is its own fixture's;
// here the fail-fast branch leaves three tokens because three branches are live. The
// load-bearing pair — incidents 1 → 0 with no compensation dispatched → undoX dispatched —
// reproduces exactly. Counts do not travel between fixtures; the property does.
//
// Single case, so no table (table-test skill): there is one SUT call shape — drive to the
// unhandled error, then drive the walk to its end — and no imminent variant. The cancel
// route is not a sibling row: it terminates rather than fails, so it emits a different
// event entirely and carries no Incidents[0].Error preference.

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
	"github.com/kartaladev/wrkflw/engine"
)

// incidentAndOpenScopeDef is
//
//	root: start → fork ⇉ { subX[…] ; incSvc(retry MaxAttempts 1) → inc-end ; doomed }
//	subX: x-start → xSvc(compensable "undoX") → xHold(svc "holdX") → x-end
//
// Three branches, each carrying exactly one of the ingredients:
//
//   - subX supplies the COMPENSABLE RECORD in an open scope — the thing the harvest makes
//     visible, and hence the reason a walk runs at all.
//   - incSvc supplies the INCIDENT. It needs an effective retry policy: an incident is
//     raised only on handleUnhandledError's raiseIncident path, i.e. retry exhaustion with
//     no catch flow and no boundary. MaxAttempts 1 makes the first retryable failure
//     terminal, which parks the token as TokenIncident and leaves the instance running.
//   - doomed supplies the KILLING ERROR. No retry policy, no boundary and no outgoing
//     flow, so a non-retryable failure takes handleUnhandledError's fail-fast policy — the
//     branch whose behaviour ADR-0174 changes.
//
// The incident must sit on a DIFFERENT token from the one that dies: an incident on the
// failing token would be dropped along with it on either code path, and the row would
// measure nothing.
func incidentAndOpenScopeDef() *model.ProcessDefinition {
	subX := &model.ProcessDefinition{
		ID: "p-incident-sub", Version: 1,
		Nodes: []model.Node{
			event.NewStart("x-start"),
			activity.NewServiceTask("xSvc",
				activity.WithTaskAction("doX"),
				activity.WithCompensateAction("undoX"),
			),
			activity.NewServiceTask("xHold", activity.WithTaskAction("holdX")),
			event.NewEnd("x-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "x1", Source: "x-start", Target: "xSvc"},
			{ID: "x2", Source: "xSvc", Target: "xHold"},
			{ID: "x3", Source: "xHold", Target: "x-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p-incident", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewSubProcess("subX", subX),
			activity.NewServiceTask("incSvc",
				activity.WithTaskAction("incAct"),
				activity.WithRetryPolicy(&model.RetryPolicy{MaxAttempts: 1}),
			),
			activity.NewServiceTask("doomed", activity.WithTaskAction("doomAct")),
			event.NewEnd("inc-end"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fork"},
			{ID: "f2", Source: "fork", Target: "subX"},
			{ID: "f3", Source: "subX", Target: "end"},
			{ID: "f4", Source: "fork", Target: "incSvc"},
			{ID: "f5", Source: "incSvc", Target: "inc-end"},
			{ID: "f6", Source: "fork", Target: "doomed"},
		},
	}
}

func TestHarvestRetiresTheSurvivingTokensIncident(t *testing.T) {
	t.Parallel()

	def := incidentAndOpenScopeDef()
	at := harvestT0

	step := func(st engine.InstanceState, trg engine.Trigger) engine.StepResult {
		at = at.Add(time.Second)
		r, err := engine.Step(t.Context(), def, st, trg, engine.StepOptions{})
		require.NoError(t, err)
		return r
	}

	r := step(engine.InstanceState{InstanceID: "i-incident"}, engine.NewStartInstance(at, nil))

	// The fork dispatches all three branches in the start Step, so every command id is
	// collected here — a later lookup would see only the branch that just advanced.
	doX := invokeIDForAction(r.Commands, "doX")
	require.NotEmpty(t, doX, "fixture: xSvc must have been dispatched")
	incAct := invokeIDForAction(r.Commands, "incAct")
	require.NotEmpty(t, incAct, "fixture: incSvc must have been dispatched")
	doomAct := invokeIDForAction(r.Commands, "doomAct")
	require.NotEmpty(t, doomAct, "fixture: doomed must have been dispatched")

	// Complete xSvc: the compensation record lands in subX's OPEN scope.
	r = step(r.State, engine.NewActionCompleted(at, doX, nil))

	// Exhaust incSvc's single attempt: retryable=true with MaxAttempts 1 is terminal, so
	// the token parks as an incident and the instance keeps running.
	r = step(r.State, engine.NewActionFailed(at, incAct, "inc-boom", true))

	before := r.State
	require.False(t, before.Status.IsTerminal(), "fixture: the instance must still be alive")
	require.Len(t, before.Incidents, 1,
		"fixture: exactly one incident must be raised, or there is nothing for the walk to retire")
	require.Equal(t, "incSvc", before.Incidents[0].NodeID,
		"fixture: and it must be on the incSvc branch, not on the token that is about to die")
	require.Equal(t, "inc-boom", before.Incidents[0].Error,
		"fixture: this is the string runtime/outbox.go's terminalEventErr would prefer")
	require.Equal(t, []string{"undoX"}, actionsOf(scopeByNodeID(t, before, "subX").Compensations),
		"fixture: the record must be LIVE in the open scope — the harvest is what makes it visible")
	require.Empty(t, before.ArchivedCompensations,
		"fixture: nothing archived yet, so the walk below can only have come from the harvest")
	require.Empty(t, before.RootCompensations,
		"fixture: nothing at root either, or the predicate would already have admitted a walk on main")
	require.Len(t, before.Tokens, 3,
		"fixture: three live tokens — parked in subX, parked as the incident, and the one about to fail")

	// THE KILL: a non-retryable failure with no handler. Without the harvest this is an
	// immediate FailInstance that keeps every surviving token and dispatches nothing
	// (measured on this fixture: status=failed tokens=3 incidents=1 invoked=[]). With the
	// harvest it becomes a compensation walk.
	killed := step(before, engine.NewActionFailed(at, doomAct, "BOOM", false))

	require.Equal(t, engine.StatusCompensating, killed.State.Status,
		"the harvest must have made the records-exist predicate true, so the instance compensates "+
			"instead of failing immediately — that redirection is the whole mechanism")
	require.Equal(t, []string{"undoX"}, invokedActionNames(killed.Commands),
		"and the harvested record's compensation action must be dispatched")
	assert.Empty(t, killed.State.Tokens,
		"beginCompensation's prologue cancels EVERY token, including the incident's")
	// ⚠ MEASURED, and it corrects the spec. §4 M7 names removeOrphanedIncidents as what
	// retires the incident, which would put the retirement at the walk's FINISH (that
	// function is called only from endInstance). It is already gone here, one Step
	// earlier: cancelTokenWaits calls removeIncidentsForToken per token
	// (engine/step_cancel.go:56), so the prologue that cancels the incident's token
	// deletes its incident directly. removeOrphanedIncidents has nothing left to sweep.
	// The end numbers M7 records are right; the named mechanism is not.
	assert.Empty(t, killed.State.Incidents,
		"the incident is retired by the prologue's per-token removeIncidentsForToken, not by "+
			"endInstance's orphan sweep — so it is already gone mid-walk, while the instance is "+
			"still StatusCompensating and has emitted no terminal event")

	// Drive the walk to its end. status only becomes terminal here, and only here does
	// endInstance run removeOrphanedIncidents over the now-empty token set.
	undoX := invokeIDForAction(killed.Commands, "undoX")
	require.NotEmpty(t, undoX, "fixture: the walk's compensation action must be outstanding")
	final := step(killed.State, engine.NewActionCompleted(at, undoX, nil)).State

	// THE PINNED CONSEQUENCE. main: tokens=2 incidents=1. Here: tokens=0 incidents=0.
	require.Equal(t, engine.StatusFailed, final.Status,
		"the deferred outcome is still a failure — the walk changes HOW it fails, not THAT it fails")
	assert.Empty(t, final.Tokens,
		"no token survives the walk, so no incident can be retained by removeOrphanedIncidents")
	assert.Empty(t, final.Incidents,
		"ACCEPTED CONSEQUENCE, pinned deliberately (ADR-0174, spec M7): the incident that ADR-0164 "+
			"Decision 3 preserved on this shape is now retired, so runtime/outbox.go's "+
			"terminalEventErr finds no allow-listed incident to quote and the instance.failed "+
			"payload plus incident_count both change for consumers")
}
