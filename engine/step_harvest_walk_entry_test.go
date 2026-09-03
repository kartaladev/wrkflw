package engine_test

// step_harvest_walk_entry_test.go — the two compensation-walk ENTRY points
// that reach `beginCompensation` without passing either of the patched dying-instance
// sites, found by `/code-review` at the delivery gate.
//
// Both were collateral damage from deleting the original blanket harvest. That
// harvest ran inside `beginCompensation` itself, and was killed
// because it also fired for ALREADY-TERMINAL legacy rows (re-running compensations an
// abandoned walk had dispatched) and for RESUMING walks. Deleting it wholesale removed
// two harvests that were neither: an admin rollback on a LIVE instance, and the
// deferred-cancel remainder walk. Both are re-added here at the SITE, gated on the walk
// actually terminating — which is the discrimination the blanket version lacked.
//
// ⚠ The gate is `ToNode == "" && ReverseNode == ""`. A partial rollback and a full
// reverse both RESUME, leaving the sub-process scope alive; hoisting their records both
// changes what the rollback compensates and re-exposes them to the retained-records
// double-run. The audit measured that: partial rollback `[]` → `[undoInner]`, and a
// later cancel `[undoRoot undoRoot]` → `[undoInner undoRoot undoInner undoRoot]`.
//
// No `ctx` case modifier (table-test skill rule 3): engine.Step documents ctx as
// carrying no cancellation semantics.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
)

// driveToParkedInsideRecordHoldingSub drives liveSiteSubDef two Steps in: inner-svc has
// completed (so the OPEN scope for `sub` holds `undo-inner`) and the token is parked on
// inner-hold. The instance is RUNNING — this is the live-instance substrate both rows
// below need, and it is the state on which `main` silently does nothing.
func driveToParkedInsideRecordHoldingSub(t *testing.T, instanceID string) (engine.InstanceState, string) {
	t.Helper()

	def := liveSiteSubDef()
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: instanceID},
		engine.NewStartInstance(harvestT0, nil), engine.StepOptions{})
	require.NoError(t, err)

	svcCmd := firstPendingInvoke(r1.Commands)
	require.NotEmpty(t, svcCmd, "fixture: inner-svc must have been dispatched")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(harvestT0.Add(time.Second), svcCmd, nil), engine.StepOptions{})
	require.NoError(t, err)

	require.Equal(t, engine.StatusRunning, r2.State.Status, "fixture: the instance must be LIVE")
	require.Len(t, r2.State.Scopes, 1, "fixture: the sub-process scope must be open")
	require.Len(t, r2.State.Scopes[0].Compensations, 1,
		"fixture: the record must be in the OPEN scope, or this row proves nothing")
	require.Empty(t, r2.State.RootCompensations, "fixture: nothing at root")
	require.Empty(t, r2.State.ArchivedCompensations, "fixture: nothing archived")

	holdCmd := firstPendingInvoke(r2.Commands)
	require.NotEmpty(t, holdCmd, "fixture: inner-hold must be outstanding")
	return r2.State, holdCmd
}

func TestAdminRollbackOnALiveInstanceHarvestsOnlyWhenItTerminates(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		trigger func(at time.Time) engine.CompensateRequested
		assert  func(t *testing.T, r engine.StepResult)
	}

	cases := []testCase{
		{
			// A plain full rollback TERMINATES, so it must compensate the
			// work completed inside the still-open sub-process. On `main` — and on this
			// bundle before the gate fix — it dispatched NOTHING, flipped the instance
			// to terminated, and only then did endInstance archive the record, so a
			// SECOND rollback was needed to actually run `undo-inner`. An operator was
			// told nothing and got no compensation for the rollback they asked for.
			name: "a_plain_full_rollback_compensates_the_open_scopes_record",
			trigger: func(at time.Time) engine.CompensateRequested {
				return engine.NewCompensateRequested(at, "")
			},
			assert: func(t *testing.T, r engine.StepResult) {
				assert.Contains(t, invokedActionNames(r.Commands), "undo-inner",
					"a terminating rollback must undo work completed inside the open sub-process on the FIRST attempt")
				assert.Equal(t, engine.StatusCompensating, r.State.Status,
					"the walk must actually start rather than the instance being re-stamped terminal")
			},
		},
		{
			// The guard on the fix, and the reason it is gated rather than blanket. A
			// full REVERSE resumes at its start node: the sub-process scope stays alive
			// and its records must NOT be hoisted, or they are both compensated early
			// and left in RootCompensations for a later walk to run a second time.
			//
			// ReverseNode rather than ToNode deliberately: a partial rollback's ToNode
			// must name a node that HAS a compensation record, and this fixture's only
			// record is the one inside the open scope — which the rollback would then be
			// asked to roll back TO rather than past. The reverse shape exercises the
			// same `resumes ⇒ do not hoist` branch of the gate.
			name: "a_full_reverse_resumes_and_must_NOT_hoist_the_open_scopes_record",
			trigger: func(at time.Time) engine.CompensateRequested {
				return engine.NewReverseToStart(at, "start")
			},
			assert: func(t *testing.T, r engine.StepResult) {
				assert.NotContains(t, invokedActionNames(r.Commands), "undo-inner",
					"a resuming rollback must leave the live scope's records alone")
				sc := scopeByNodeID(t, r.State, "sub")
				assert.Equal(t, []string{"undo-inner"}, actionsOf(sc.Compensations),
					"the record must still be LIVE in its scope, not hoisted into the archive or root")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			before, _ := driveToParkedInsideRecordHoldingSub(t, "i-walk-entry")
			r, err := engine.Step(t.Context(), liveSiteSubDef(), before,
				tc.trigger(harvestT0.Add(2*time.Second)), engine.StepOptions{})
			require.NoError(t, err)
			tc.assert(t, r)
		})
	}
}

// TestDeferredCancelRemainderWalkHarvestsTheSiblingScope covers the
// deferred-cancel re-entry into beginCompensation from applyFinish.
//
// A CancelRequested that arrives while a RESUMING walk is in flight is deferred,
// recording PendingCancel; when that walk finishes, applyFinish re-enters
// beginCompensation "over the remainder" and terminates. That re-entry passed neither
// patched site, so a sibling scope's completed compensable work was never compensated —
// the operator's cancel terminated the instance and `undoB` was merely archived
// afterwards by endInstance, unreachable on a terminal instance until a second rollback.
//
// Standalone rather than a row of the table above (table-skill divergence): the setup is
// structurally different — it needs a live scope-wide walk plus a deferred cancel across
// three Steps, not a single trigger against a parked instance.
func TestDeferredCancelRemainderWalkHarvestsTheSiblingScope(t *testing.T) {
	t.Parallel()

	def := inflightWalkSiblingScopesDef()
	at := harvestT0

	step := func(t *testing.T, st engine.InstanceState, trg engine.Trigger) engine.StepResult {
		t.Helper()
		at = at.Add(time.Second)
		r, err := engine.Step(t.Context(), def, st, trg, engine.StepOptions{})
		require.NoError(t, err)
		return r
	}

	// Drive to: subA's scope-wide throw walk in flight over undoA, subB parked holding
	// undoB in its own OPEN scope.
	// ⚠ These are ACTION names, not node ids — invokeIDForAction matches InvokeAction.Name.
	// ⚠ The fork dispatches BOTH branches in the first Step, so doB's command id must be
	// captured from that Step: it will not reappear in any later Step's Commands.
	r := step(t, engine.InstanceState{InstanceID: "i-deferred"}, engine.NewStartInstance(at, nil))
	doA := invokeIDForAction(r.Commands, "doA")
	doB := invokeIDForAction(r.Commands, "doB")
	require.NotEmpty(t, doA, "fixture: subA's compensable task must be dispatched at the fork")
	require.NotEmpty(t, doB, "fixture: subB's compensable task must be dispatched at the fork")

	r = step(t, r.State, engine.NewActionCompleted(at, doA, nil))
	gate := invokeIDForAction(r.Commands, "gateAct")
	require.NotEmpty(t, gate, "fixture: completing doA must advance subA to its gate")

	r = step(t, r.State, engine.NewActionCompleted(at, doB, nil))
	r = step(t, r.State, engine.NewActionCompleted(at, gate, nil))
	require.Equal(t, engine.StatusCompensating, r.State.Status,
		"fixture: subA's scope-wide throw must have started a walk")
	undoA := invokeIDForAction(r.Commands, "undoA")
	require.NotEmpty(t, undoA, "fixture: the walk must have dispatched undoA")
	sb := scopeByNodeID(t, r.State, "subB")
	require.Equal(t, []string{"undoB"}, actionsOf(sb.Compensations),
		"fixture: subB's record must be LIVE in its own open scope")

	// An operator cancel arrives mid-walk. It is deferred.
	r = step(t, r.State, engine.NewCancelRequested(at))
	require.True(t, r.State.PendingCancel, "fixture: the cancel must have been DEFERRED, not applied")
	require.Equal(t, engine.StatusCompensating, r.State.Status, "fixture: the walk continues")

	// The in-flight walk finishes; applyFinish consumes PendingCancel and re-enters
	// beginCompensation over the remainder. THAT is the entry point under test.
	r = step(t, r.State, engine.NewActionCompleted(at, undoA, nil))

	assert.Contains(t, invokedActionNames(r.Commands), "undoB",
		"the deferred cancel's remainder walk must compensate the sibling scope's completed work, "+
			"not terminate around it and leave undoB archived on a dead instance")
}
