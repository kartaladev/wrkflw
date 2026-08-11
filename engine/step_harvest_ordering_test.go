package engine_test

// step_harvest_ordering_test.go — ADR-0174 Decision 3: endInstance harvests its open
// scopes BEFORE clearing the compensation cursor.
//
// This is the ONLY test that can see that ordering, and the ordering is the difference
// between compensating exactly once and running money-moving actions twice.
//
// Why the fixture is this elaborate. partitionForLiveWalk only windows a scope when
// scopeWideWalkDraining is true — a LIVE scope-wide walk over that very scope. So the
// test needs an instance that reaches endInstance while such a walk is in flight, which
// on this engine means exactly one route: forceTerminate (engine/step_nodes.go),
// which clears no cursor and closes no scope. A walk's own finish CANNOT be used —
// stepCompensationFinish zeroes s.Compensating one call up, so there both orderings
// compute an identical result and the test could not fail. That was the defect the
// rule-#9 audit found in this test's first specification, and it is the same
// "recomputes identically" trap ADR-0173's delivery shipped.
//
// The walk must also be ADVANCED at least one step before the kill, because it is the
// window's OFFSET (NextIndex) that moves, not its count: with NextIndex still at its
// initial value the two orderings archive the same set.

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

// invokeIDForAction returns the CommandID of the first pending InvokeAction whose Name
// is action, or "" when none is pending. Selecting by NAME rather than by position is
// required here: the fork dispatches a compensation action and a sibling service task
// in the same Step, so "the first pending invoke" is ambiguous.
func invokeIDForAction(cmds []engine.Command, action string) string {
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok && !ia.FireAndForget && ia.Name == action {
			return ia.CommandID
		}
	}
	return ""
}

// orderingThrowDef is
//
//	root: start → sub[ inner-start → svcA → svcB → svcC → fork
//	                     ⇒ throw(scope-wide CompensateThrow) → rbEnd
//	                     ⇒ hold(ServiceTask) → kill(force-termination end) ] → end
//
// svcA/svcB/svcC are compensable (undoA/undoB/undoC), so the scope for `sub` holds
// three records in completion order. The scope-wide throw drains that same scope while
// the `hold` branch stays live, and completing `hold` force-terminates the whole
// instance with the walk still outstanding.
func orderingThrowDef() *model.ProcessDefinition {
	svc := func(id, doAct, undoAct string) model.Node {
		return activity.NewServiceTask(id,
			activity.WithTaskAction(doAct),
			activity.WithCompensateAction(undoAct),
		)
	}
	nested := &model.ProcessDefinition{
		ID: "p-ordering-nested", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			svc("svcA", "doA", "undoA"),
			svc("svcB", "doB", "undoB"),
			svc("svcC", "doC", "undoC"),
			gateway.NewParallel("fork"),
			event.NewCompensateThrow("throw"),
			event.NewEnd("rbEnd"),
			activity.NewServiceTask("hold", activity.WithTaskAction("holdAct")),
			event.NewEnd("kill", event.WithForceTermination("abort", event.OutcomeAbort)),
		},
		Flows: []flow.SequenceFlow{
			{ID: "n1", Source: "inner-start", Target: "svcA"},
			{ID: "n2", Source: "svcA", Target: "svcB"},
			{ID: "n3", Source: "svcB", Target: "svcC"},
			{ID: "n4", Source: "svcC", Target: "fork"},
			{ID: "n5", Source: "fork", Target: "throw"},
			{ID: "n6", Source: "throw", Target: "rbEnd"},
			{ID: "n7", Source: "fork", Target: "hold"},
			{ID: "n8", Source: "hold", Target: "kill"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p-ordering", Version: 1,
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

func TestForceTerminationMidWalkArchivesOnlyTheUndispatchedRemainder(t *testing.T) {
	t.Parallel()

	def := orderingThrowDef()
	at := harvestT0

	step := func(t *testing.T, st engine.InstanceState, trg engine.Trigger) engine.StepResult {
		t.Helper()
		at = at.Add(time.Second)
		r, err := engine.Step(t.Context(), def, st, trg, engine.StepOptions{})
		require.NoError(t, err)
		return r
	}

	r := step(t, engine.InstanceState{InstanceID: "i-ord"}, engine.NewStartInstance(at, nil))
	for _, act := range []string{"doA", "doB", "doC"} {
		id := invokeIDForAction(r.Commands, act)
		require.NotEmpty(t, id, "fixture: %s must have been dispatched", act)
		r = step(t, r.State, engine.NewActionCompleted(at, id, nil))
	}

	// The fork has now fired: the scope-wide throw started a walk over sub's three
	// records (reverse order, so undoC first) and the `hold` branch is live.
	require.Len(t, r.State.Scopes, 1, "fixture: the sub-process scope must still be open")
	require.Len(t, r.State.Scopes[0].Compensations, 3,
		"fixture: three compensable records must be held in the OPEN scope")
	require.Equal(t, engine.StatusCompensating, r.State.Status,
		"fixture: the scope-wide throw must have started a walk")

	undoC := invokeIDForAction(r.Commands, "undoC")
	require.NotEmpty(t, undoC, "fixture: the walk must dispatch undoC first (reverse order)")
	holdID := invokeIDForAction(r.Commands, "holdAct")
	require.NotEmpty(t, holdID, "fixture: the sibling branch must be live, or nothing can force-terminate")

	// ADVANCE the walk one step: undoC completes, so undoB goes in flight and the
	// window's OFFSET moves. Without this the two orderings archive the same set.
	r = step(t, r.State, engine.NewActionCompleted(at, undoC, nil))
	require.NotEmpty(t, invokeIDForAction(r.Commands, "undoB"),
		"fixture: the walk must have advanced to undoB, or the offset never moved")

	// Kill the instance with the walk still in flight, via the one route that reaches
	// endInstance with a live cursor.
	r = step(t, r.State, engine.NewActionCompleted(at, holdID, nil))
	require.True(t, r.State.Status.IsTerminal(), "fixture: the force-termination end must have run")
	assert.Nil(t, r.State.Scopes, "the terminal snapshot carries no open scope (ADR-0174)")

	// undoC was dispatched AND completed; undoB was dispatched. Only undoA is still
	// owed, so only undoA may survive into the archive.
	assert.Equal(t, []string{"undoA"}, actionsOf(r.State.ArchivedCompensations["sub"]),
		"only the records the abandoned walk never dispatched may be archived — "+
			"harvesting after the cursor clear archives all three and a later rollback re-runs undoB and undoC")

	// And the observable consequence: a later admin rollback compensates undoA alone.
	rb := step(t, r.State, engine.NewCompensateRequested(at, ""))
	assert.Equal(t, []string{"undoA"}, invokedActionNames(rb.Commands),
		"a rollback after the abandoned walk must re-dispatch undoA only, never undoC/undoB again")
}

// actionsOf projects a record slice to its action names, in slice order.
func actionsOf(recs []engine.CompensationRecord) []string {
	var out []string
	for _, r := range recs {
		out = append(out, r.Action)
	}
	return out
}
