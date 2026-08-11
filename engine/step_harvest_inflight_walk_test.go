package engine_test

// step_harvest_inflight_walk_test.go — ADR-0174, T9: the harvest is placed AFTER each
// live site's in-flight-walk guard, so while a compensation walk is in flight the harvest
// does NOT run at all.
//
// ⚠ Read this before "improving" the test. The obvious formulation — "an in-flight walk
// still defers at both sites" — CANNOT FAIL, and the rule-#9 audit rejected it for that
// reason. Both guards
// (step_errors.go's `s.Status == StatusCompensating && s.Compensating.ActiveCmdID != ""`
// and step_triggers.go's identical predicate) read ONLY the compensation cursor. Moving
// the harvest above or below them changes nothing either guard observes, so a test that
// asserts only "it deferred" is green with the harvest on either side of the guard.
//
// What IS observable is the harvest's own side effect: archiveCompensations sets
// `scope.Compensations = nil` and appends into ArchivedCompensations. So the falsifiable
// property is the RECORD SET the deferral leaves behind — a second, unrelated open scope's
// record must still be sitting in `Scope.Compensations`, un-hoisted, because the harvest
// never ran. Hoist the call above the guard and that record moves; both rows go RED.
//
// Why the harvest must not run mid-walk: `harvestOpenScopeCompensations`' own godoc says
// it. The teardown WINDOW that archiveCompensations stamps onto the cursor
// (TeardownArchiveKey/Offset/Count, ADR-0173) is only sound where the walk never advances
// again. These two sites are exactly the ones where the walk DOES continue, so a harvest
// here would reintroduce the double-compensation residue ADR-0173's window fields exist to
// remove — for the drained scope — while also stealing the sibling scope's records out from
// under a walk that has not yet decided its own fate.
//
// No `ctx` case modifier (table-test skill rule 3): engine.Step documents ctx as carrying
// no cancellation semantics — it is used only for trace-correlated logging and is never
// inspected for control flow — so a cancelled-context row would assert nothing about this
// SUT.

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

// scopeByNodeID returns the open scope opened by the sub-process node nodeID, failing the
// test when no such scope is open. Selecting by NODE id rather than by slice position is
// what makes the assertions readable when two sibling scopes are open at once.
func scopeByNodeID(t *testing.T, st engine.InstanceState, nodeID string) engine.Scope {
	t.Helper()
	for _, sc := range st.Scopes {
		if sc.NodeID == nodeID {
			return sc
		}
	}
	require.FailNowf(t, "no open scope for node", "node %q has no open scope; open scopes: %+v", nodeID, st.Scopes)
	return engine.Scope{}
}

// inflightWalkSiblingScopesDef is
//
//	root: start → fork ⇉ { subA[…] ; subB[…] }
//
//	subA: a-start → aSvc(compensable "undoA") → aGate(svc "gateAct")
//	                → aThrow(scope-wide CompensateThrow) → a-rbEnd
//	subB: b-start → bSvc(compensable "undoB") → bHold(svc "holdB") → b-end
//
// Two SIBLING scopes, deliberately not nested. subA's scope-wide throw drains subA's own
// records and leaves subB's scope entirely alone, so subB's record is the un-harvested
// control. Nesting them would have made the test hostage to whether a scope-wide walk
// reaches descendant scopes, which is a different question.
//
// aGate exists so the throw fires in a LATER Step than aSvc's completion: the walk must
// already be in flight (cursor stamped, undoA outstanding) when the killing trigger
// arrives, and a throw reached in the same ApplyTrigger as the record's creation would
// leave no snapshot in which to deliver it.
//
// bHold is the token the unhandled-error row fails. It is inside subB, i.e. NOT the scope
// the walk is draining — an error on a token inside the draining scope would be a different
// (and much narrower) collision.
func inflightWalkSiblingScopesDef() *model.ProcessDefinition {
	subA := &model.ProcessDefinition{
		ID: "p-inflight-a", Version: 1,
		Nodes: []model.Node{
			event.NewStart("a-start"),
			activity.NewServiceTask("aSvc",
				activity.WithTaskAction("doA"),
				activity.WithCompensateAction("undoA"),
			),
			activity.NewServiceTask("aGate", activity.WithTaskAction("gateAct")),
			event.NewCompensateThrow("aThrow"),
			event.NewEnd("a-rbEnd"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "a1", Source: "a-start", Target: "aSvc"},
			{ID: "a2", Source: "aSvc", Target: "aGate"},
			{ID: "a3", Source: "aGate", Target: "aThrow"},
			{ID: "a4", Source: "aThrow", Target: "a-rbEnd"},
		},
	}
	subB := &model.ProcessDefinition{
		ID: "p-inflight-b", Version: 1,
		Nodes: []model.Node{
			event.NewStart("b-start"),
			activity.NewServiceTask("bSvc",
				activity.WithTaskAction("doB"),
				activity.WithCompensateAction("undoB"),
			),
			activity.NewServiceTask("bHold", activity.WithTaskAction("holdB")),
			event.NewEnd("b-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "b1", Source: "b-start", Target: "bSvc"},
			{ID: "b2", Source: "bSvc", Target: "bHold"},
			{ID: "b3", Source: "bHold", Target: "b-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p-inflight", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewSubProcess("subA", subA),
			activity.NewSubProcess("subB", subB),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fork"},
			{ID: "f2", Source: "fork", Target: "subA"},
			{ID: "f3", Source: "fork", Target: "subB"},
			{ID: "f4", Source: "subA", Target: "end"},
		},
	}
}

func TestInFlightCompensationWalkSuppressesTheHarvest(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// kill builds the trigger delivered while subA's walk is in flight. holdB is the
		// outstanding bHold command id, which the unhandled-error row fails and the
		// cancel row ignores.
		kill func(at time.Time, holdB string) engine.Trigger
		// assertDeferral pins the ADR-0170 deferral each site performs, which differs
		// per site even though the suppression property is shared.
		assertDeferral func(t *testing.T, st engine.InstanceState)
	}

	cases := []testCase{
		{
			// step_errors.go: handleUnhandledError's guard wins, the failure is recorded
			// as the live walk's pending terminal outcome, and the harvest below it is
			// never reached.
			name: "an_unhandled_error_defers_to_the_live_walk_and_harvests_nothing",
			kill: func(at time.Time, holdB string) engine.Trigger {
				return engine.NewActionFailed(at, holdB, "BOOM", false)
			},
			assertDeferral: func(t *testing.T, st engine.InstanceState) {
				assert.True(t, st.PendingCancel,
					"the error must be deferred to the in-flight walk (ADR-0170), not applied now")
				assert.Equal(t, engine.StatusFailed, st.PendingFinalStatus,
					"and the deferred outcome must be this error's, applied when the walk finishes")
				assert.Equal(t, "BOOM", st.PendingFinalErr)
			},
		},
		{
			// step_triggers.go: handleCancelRequested's guard wins. subA's walk is a
			// scope-wide THROW walk, so its cursor carries ResumeNode and the cancel
			// takes the defer-if-resuming branch rather than the silent no-op one.
			name: "an_operator_cancel_defers_to_the_live_walk_and_harvests_nothing",
			kill: func(at time.Time, _ string) engine.Trigger {
				return engine.NewCancelRequested(at)
			},
			assertDeferral: func(t *testing.T, st engine.InstanceState) {
				assert.True(t, st.PendingCancel,
					"a cancel arriving mid-walk must be deferred, never start a second walk (ADR-0039/0170)")
				assert.Equal(t, engine.StatusTerminated, st.PendingFinalStatus,
					"and the deferred outcome must be the cancel's own, explicitly stamped")
				assert.Equal(t, "cancelled", st.PendingFinalErr)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := inflightWalkSiblingScopesDef()
			at := harvestT0

			step := func(st engine.InstanceState, trg engine.Trigger) engine.StepResult {
				at = at.Add(time.Second)
				r, err := engine.Step(t.Context(), def, st, trg, engine.StepOptions{})
				require.NoError(t, err)
				return r
			}

			r := step(engine.InstanceState{InstanceID: "i-inflight"}, engine.NewStartInstance(at, nil))

			// The fork dispatches BOTH branches' first service task in the start Step, so
			// both command ids are collected from that one result — a later
			// invokeIDForAction lookup would find only the branch that just advanced.
			doA := invokeIDForAction(r.Commands, "doA")
			require.NotEmpty(t, doA, "fixture: aSvc must have been dispatched")
			doB := invokeIDForAction(r.Commands, "doB")
			require.NotEmpty(t, doB, "fixture: bSvc must have been dispatched")

			// Order matters: subB's record is created FIRST, so it is already sitting in
			// its open scope by the time subA's walk begins. Its successor bHold is
			// dispatched by that same Step, and is the token the error row fails, so its
			// id is captured here rather than after the walk starts.
			r = step(r.State, engine.NewActionCompleted(at, doB, nil))
			holdB := invokeIDForAction(r.Commands, "holdB")
			require.NotEmpty(t, holdB, "fixture: bHold must have been dispatched")

			r = step(r.State, engine.NewActionCompleted(at, doA, nil))

			gate := invokeIDForAction(r.Commands, "gateAct")
			require.NotEmpty(t, gate, "fixture: aGate must have been dispatched")
			r = step(r.State, engine.NewActionCompleted(at, gate, nil))

			// Preconditions. Every one of these is what makes the two assertions after
			// the kill non-vacuous: a live walk to defer to, and a SEPARATE open scope
			// whose record the harvest would move if it ran.
			before := r.State
			require.Equal(t, engine.StatusCompensating, before.Status,
				"fixture: subA's scope-wide throw must have started a walk")
			require.NotEmpty(t, before.Compensating.ActiveCmdID,
				"fixture: the walk must be IN FLIGHT, or neither guard fires and the harvest runs")
			require.NotEmpty(t, before.Compensating.ResumeNode,
				"fixture: a throw walk's cursor carries ResumeNode — the cancel row's deferral branch needs it")
			require.Equal(t, []string{"undoA"}, invokedActionNames(r.Commands),
				"fixture: the walk must have dispatched undoA")
			require.Len(t, before.Scopes, 2, "fixture: both sibling scopes must be open")
			require.Equal(t, []string{"undoB"}, actionsOf(scopeByNodeID(t, before, "subB").Compensations),
				"fixture: subB's record must be held LIVE in its open scope — it is the harvest's target")
			require.Empty(t, before.ArchivedCompensations,
				"fixture: nothing archived yet, so every archived record after the kill is the harvest's work")

			after := step(before, tc.kill(at, holdB)).State

			// THE PROPERTY. The harvest sits below each site's in-flight guard, so it
			// never ran: subB's record is exactly where it was.
			assert.Equal(t, []string{"undoB"}, actionsOf(scopeByNodeID(t, after, "subB").Compensations),
				"the harvest must NOT have run while a walk was in flight — subB's record must still be "+
					"live in its open scope (hoisting the harvest above the guard nils this slice)")
			assert.Empty(t, after.ArchivedCompensations,
				"and nothing may have reached the archive: a mid-walk harvest would stamp a teardown "+
					"window onto a cursor that is still going to advance, which is exactly the "+
					"double-compensation residue ADR-0173 removed")

			// And the deferral ADR-0170 prescribes is intact: the walk continues, it was
			// not restarted, and no compensation action was re-dispatched.
			assert.Equal(t, engine.StatusCompensating, after.Status,
				"the live walk must still own the instance")
			assert.Equal(t, before.Compensating, after.Compensating,
				"the cursor must be untouched — a second walk would rename it out from under the "+
					"outstanding undoA and re-run money-moving actions")
			tc.assertDeferral(t, after)
		})
	}
}
