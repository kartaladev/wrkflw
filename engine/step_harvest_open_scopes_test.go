package engine_test

// step_harvest_open_scopes_test.go — ADR-0174: a dying instance harvests its open
// scopes' compensation records into the archive, then closes those scopes.
//
// The route driven here is the one ADR-0162 names explicitly as still leaving a
// zombie: a force-termination end event fired INSIDE a sub-process. It is
// scope-agnostic and ends the whole instance, so the instance dies while the
// sub-process scope is still open and holding the record for a compensable activity
// that completed inside it. It is also the ONLY route that reaches endInstance
// without first consulting a records-exist predicate, which is what makes it the
// route that pins the backstop.
//
// No `ctx` case modifier (table-test skill rule 3): engine.Step documents ctx as
// carrying no cancellation semantics — it is used only for trace-correlated logging
// and is never inspected for control flow — so a cancelled-context row would assert
// nothing about this SUT.
//
// ⚠ Table-skill divergence, deliberate: the second test function here
// (TestAdminRollbackWalksAfterEndInstanceOnlyTermination, T5) is NOT a row of the table
// above. The table's SUT call is "drive to terminal, inspect the two snapshots"; T5's is
// "deliver a FURTHER trigger to the terminal snapshot and inspect the returned
// StepResult". Different call shape, different assertion surface, exactly one case — the
// shared drive is factored into driveToForceTerminationInsideSub instead, which is what
// the skill's no-duplicated-boilerplate rule is actually protecting.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// harvestT0 anchors every timestamp in this file. Fixed, never time.Now(): the
// engine is clock-free and the records' CompletedAt ordering must be deterministic.
var harvestT0 = time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

// forceTermInsideSubDef is
//
//	root: start → sub[ inner-start → inner-svc → inner-second → inner-kill(force termination) ] → end
//
// When compensable, inner-svc carries a CompensateAction, so completing it records a
// compensation record in the OPEN scope for `sub`. inner-kill then ends the whole
// instance without closing that scope.
//
// ⚠ inner-second exists so the record and the termination land in DIFFERENT Steps.
// Without it, completing inner-svc records the record and advances straight into
// inner-kill within one ApplyTrigger, so no snapshot ever shows the record sitting in
// the open scope — and the control assertion "one record, un-archived" could never
// pass. That is exactly the vacuous-control defect this repo keeps shipping: the
// fixture, not the assertion line, is what decides whether a test can fail.
//
// The non-compensable variant is the same shape with no record, which is what
// distinguishes "the harvest archived nothing" from "the harvest did not run".
func forceTermInsideSubDef(compensable bool) *model.ProcessDefinition {
	opts := []activity.ServiceTaskOption{activity.WithTaskAction("book-inner")}
	if compensable {
		opts = append(opts, activity.WithCompensateAction("undo-inner"))
	}
	nested := &model.ProcessDefinition{
		ID: "p-harvest-nested", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", opts...),
			activity.NewServiceTask("inner-second", activity.WithTaskAction("hold-inner")),
			event.NewEnd("inner-kill", event.WithForceTermination("abort", event.OutcomeAbort)),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-second"},
			{ID: "if3", Source: "inner-second", Target: "inner-kill"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p-harvest", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", nested),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "end"},
		},
	}
}

// driveToForceTerminationInsideSub runs forceTermInsideSubDef to its terminal state and
// returns the last pre-terminal snapshot alongside the terminal one. Two Steps of the
// three-Step drive are shared by every test on this route, and the pre-terminal snapshot
// is what makes their controls non-vacuous.
func driveToForceTerminationInsideSub(t *testing.T, def *model.ProcessDefinition, instanceID string) (before, after engine.InstanceState) {
	t.Helper()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: instanceID},
		engine.NewStartInstance(harvestT0, nil), engine.StepOptions{})
	require.NoError(t, err)

	svcCmd := firstPendingInvoke(r1.Commands)
	require.NotEmpty(t, svcCmd, "fixture: inner-svc must have been dispatched")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(harvestT0.Add(time.Second), svcCmd, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.False(t, r2.State.Status.IsTerminal(),
		"fixture: the instance must still be alive here, or there is no pre-terminal snapshot")

	holdCmd := firstPendingInvoke(r2.Commands)
	require.NotEmpty(t, holdCmd, "fixture: inner-second must have been dispatched")

	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewActionCompleted(harvestT0.Add(2*time.Second), holdCmd, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.True(t, r3.State.Status.IsTerminal(), "fixture: the force-termination end must have run")

	return r2.State, r3.State
}

func TestDyingInstanceHarvestsOpenScopes(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// compensable selects whether inner-svc records a compensation record, i.e.
		// whether the open scope has anything to harvest.
		compensable bool
		assert      func(t *testing.T, before, after engine.InstanceState)
	}

	cases := []testCase{
		{
			// T3. The record must reach the archive under the SUB-PROCESS NODE's id,
			// which is the key a normal scope exit would have used — scope identity
			// has to survive an abnormal exit too, or scope-targeted compensation
			// (ADR-0039) silently stops working for these instances.
			name:        "a_force_termination_inside_a_sub_process_archives_the_record_under_its_node_id",
			compensable: true,
			assert: func(t *testing.T, before, after engine.InstanceState) {
				require.Len(t, before.Scopes, 1,
					"control: the record must be in the OPEN scope before termination, or this row proves nothing")
				require.Len(t, before.Scopes[0].Compensations, 1, "control: one record, un-archived")

				require.Len(t, after.ArchivedCompensations["sub"], 1,
					"the open scope's record must be harvested into the archive under the sub-process node id")
				assert.Equal(t, "undo-inner", after.ArchivedCompensations["sub"][0].Action,
					"and it must be the compensable activity's action, not some other record")
				assert.Equal(t, "inner-svc", after.ArchivedCompensations["sub"][0].NodeID)
				assert.Empty(t, after.RootCompensations,
					"the harvest archives under scope identity; it must not hoist to root (that is a walk's job)")
			},
		},
		{
			// T4. The zombie half of the invariant, and ADR-0162's deferred promise.
			name:        "the_terminal_snapshot_carries_no_open_scope",
			compensable: true,
			assert: func(t *testing.T, _, after engine.InstanceState) {
				require.True(t, after.Status.IsTerminal(), "control: the instance must have died")
				assert.Nil(t, after.Scopes,
					"a terminal snapshot must carry no open scope, and Scopes must be exactly nil "+
						"(assert.Empty would pass for a non-nil empty slice and so could not see the persisted-shape change)")
			},
		},
		{
			// T8. Separates "archived nothing" from "did not run": with no record the
			// harvest must be a no-op that creates no empty archive key — a `{}` where
			// `null` belongs is the persisted-shape wart ADR-0173 normalised away.
			name:        "an_open_scope_holding_no_records_still_closes_and_archives_nothing",
			compensable: false,
			assert: func(t *testing.T, before, after engine.InstanceState) {
				require.Len(t, before.Scopes, 1, "control: the scope is open")
				require.Empty(t, before.Scopes[0].Compensations, "control: and holds no record")

				assert.Nil(t, after.Scopes, "the scope must still be closed")
				assert.Empty(t, after.ArchivedCompensations,
					"a harvest with nothing to archive must not mint an empty archive key")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := forceTermInsideSubDef(tc.compensable)

			r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-harvest"},
				engine.NewStartInstance(harvestT0, nil), engine.StepOptions{})
			require.NoError(t, err)

			svcCmd := firstPendingInvoke(r1.Commands)
			require.NotEmpty(t, svcCmd, "fixture: inner-svc must have been dispatched")

			// Completing inner-svc records the compensation record (when compensable)
			// and parks the token on inner-second, so this state is a genuine
			// pre-terminal snapshot with the record still in the OPEN scope.
			r2, err := engine.Step(t.Context(), def, r1.State,
				engine.NewActionCompleted(harvestT0.Add(time.Second), svcCmd, nil), engine.StepOptions{})
			require.NoError(t, err)
			require.False(t, r2.State.Status.IsTerminal(),
				"fixture: the instance must still be alive here, or there is no pre-terminal snapshot to compare against")

			holdCmd := firstPendingInvoke(r2.Commands)
			require.NotEmpty(t, holdCmd, "fixture: inner-second must have been dispatched")

			// Completing inner-second advances into inner-kill, which force-terminates
			// the whole instance while the sub-process scope is still open.
			r3, err := engine.Step(t.Context(), def, r2.State,
				engine.NewActionCompleted(harvestT0.Add(2*time.Second), holdCmd, nil), engine.StepOptions{})
			require.NoError(t, err)

			tc.assert(t, r2.State, r3.State)
		})
	}
}

// TestAdminRollbackWalksAfterEndInstanceOnlyTermination is T5: the harvested records are
// not merely present in the snapshot, they are REACHABLE — an operator can still roll the
// instance back after it died.
//
// On `main` this same rollback is REFUSED with "nothing left to compensate", because the
// only copy of the record is stranded in the open scope and hasCompensationRecordsToWalk
// reads RootCompensations + ArchivedCompensations only (M1). The harvest is what puts the
// record where that predicate can see it, and consolidateArchiveIntoRoot is what carries
// it from there into the walk's record set.
//
// ⚠ RESTRICTED to the force-termination route DELIBERATELY, and it is the only route on
// which this test is satisfiable at all. The spec's audited draft demanded all three
// dying-instance routes; that was unsatisfiable. Post-fix the unhandled-error and cancel
// routes leave the instance StatusCompensating, not terminal: a rollback delivered
// mid-walk is swallowed by stepCompensateRequested's in-flight guard (0 commands, no
// error), and one delivered after the walk finishes is CORRECTLY refused because the walk
// consumed the records. Force termination is the one route that reaches endInstance
// without consulting a records-exist predicate, so it is the only one that produces a
// terminal instance whose harvested records are still owed.
func TestAdminRollbackWalksAfterEndInstanceOnlyTermination(t *testing.T) {
	t.Parallel()

	def := forceTermInsideSubDef(true)
	before, after := driveToForceTerminationInsideSub(t, def, "i-harvest-rollback")

	// Controls. Without them this test would pass on a fixture where the record had
	// already been archived by a NORMAL scope exit, which is not the route under test.
	require.Len(t, before.Scopes, 1,
		"control: the record must still be in the OPEN scope before termination")
	require.Len(t, before.Scopes[0].Compensations, 1, "control: one record, un-archived")
	require.Empty(t, before.ArchivedCompensations,
		"control: nothing archived yet, or the harvest is not what makes the rollback reachable")
	require.Empty(t, before.RootCompensations,
		"control: nothing at root either, or the predicate would already have admitted the rollback")
	require.True(t, after.Status.IsTerminal(), "control: the instance must have died")

	// A plain full rollback — ReverseNode "" and no ResetVars/RestoreTargetVars — which is
	// the one CompensateRequested shape allowOnTerminal waves through (ADR-0164 carve-out
	// #1). Any resume-shaped rollback would be refused by dispatch for carrying resume
	// intent, which says nothing about the harvest.
	rb, err := engine.Step(t.Context(), def, after,
		engine.NewCompensateRequested(harvestT0.Add(3*time.Second), ""), engine.StepOptions{})
	require.NoError(t, err,
		"the rollback must be ACCEPTED; on main it fails with \"nothing left to compensate\" "+
			"because the record is stranded in the open scope")

	assert.Equal(t, engine.StatusCompensating, rb.State.Status,
		"the walk must actually start, not be silently swallowed")
	assert.Equal(t, []string{"undo-inner"}, invokedActionNames(rb.Commands),
		"the walk must dispatch the harvested record's compensation action — "+
			"asserted by NAME, not by command count, because the count is fixture-dependent (M3)")
}
