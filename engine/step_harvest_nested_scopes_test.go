package engine_test

// step_harvest_nested_scopes_test.go — a dying instance with MORE THAN ONE
// open scope harvests each scope's records under THAT scope's own NodeID key.
//
// This is the assertion that pins scope IDENTITY, not merely record survival. A harvest
// that dumped every open scope's records under one key — the innermost scope's, the root's,
// or a single flat list — would satisfy every other test in this delivery and would
// silently break scope-targeted compensation: a later CompensateThrow scoped to
// `outerSub` looks up ArchivedCompensations["outerSub"], and would find undoInner in it or
// find nothing at all. So the invariant is "an abnormal exit archives exactly what a
// normal exit would have", one key per scope.
//
// ⚠ Route matters, and this test asserts the keys on the force-termination route ONLY.
// On the unhandled-error and cancel routes the harvest is immediately followed by
// beginCompensation's consolidateArchiveIntoRoot, which DRAINS the archive into the root
// record set — an audit lens measured ArchivedCompensations as observably `map[]` there,
// so the per-scope keys cannot be asserted on those routes at all. What is assertable
// there is record presence and reverse order in RootCompensations, which is the live-sites
// file's business (step_harvest_live_sites_test.go). Force termination reaches endInstance
// without beginning a walk, so the archive it leaves behind is the harvest's own output.
//
// Fixture note, and the reason for i-hold: without a node between i-svc and i-kill, the
// inner record and the termination land in the SAME Step, so no snapshot ever shows both
// scopes holding their records un-archived and the control assertions could not fail. That
// is the vacuous-control defect this repo keeps shipping — the fixture, not the assertion
// line, decides whether a test can fail.
//
// Single case, so no table (table-test skill): there is exactly one SUT call shape here
// and no imminent variant — the per-route variants are excluded above for a measured
// reason, not deferred.

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

// nestedForceTermDef is
//
//	root:     start → outerSub[ … ] → end
//	outerSub: o-start → o-svc(compensable "undoOuter") → innerSub[ … ] → o-end
//	innerSub: i-start → i-svc(compensable "undoInner") → i-hold → i-kill(force termination)
//
// Driven to the kill, TWO scopes are open — one for outerSub holding undoOuter's record,
// one for innerSub holding undoInner's — and the force-termination end ends the whole
// instance without closing either. openScope appends a child after its parent, so the
// snapshot's scope order is outerSub then innerSub.
func nestedForceTermDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "p-nested-inner", Version: 1,
		Nodes: []model.Node{
			event.NewStart("i-start"),
			activity.NewServiceTask("i-svc",
				activity.WithTaskAction("do-inner"),
				activity.WithCompensateAction("undoInner"),
			),
			activity.NewServiceTask("i-hold", activity.WithTaskAction("hold-inner")),
			event.NewEnd("i-kill", event.WithForceTermination("abort", event.OutcomeAbort)),
		},
		Flows: []flow.SequenceFlow{
			{ID: "i1", Source: "i-start", Target: "i-svc"},
			{ID: "i2", Source: "i-svc", Target: "i-hold"},
			{ID: "i3", Source: "i-hold", Target: "i-kill"},
		},
	}
	outer := &model.ProcessDefinition{
		ID: "p-nested-outer", Version: 1,
		Nodes: []model.Node{
			event.NewStart("o-start"),
			activity.NewServiceTask("o-svc",
				activity.WithTaskAction("do-outer"),
				activity.WithCompensateAction("undoOuter"),
			),
			activity.NewSubProcess("innerSub", inner),
			event.NewEnd("o-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "o1", Source: "o-start", Target: "o-svc"},
			{ID: "o2", Source: "o-svc", Target: "innerSub"},
			{ID: "o3", Source: "innerSub", Target: "o-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p-nested", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("outerSub", outer),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "outerSub"},
			{ID: "f2", Source: "outerSub", Target: "end"},
		},
	}
}

func TestNestedOpenScopesEachHarvestUnderTheirOwnNodeID(t *testing.T) {
	t.Parallel()

	def := nestedForceTermDef()
	at := harvestT0

	step := func(t *testing.T, st engine.InstanceState, trg engine.Trigger) engine.StepResult {
		t.Helper()
		at = at.Add(time.Second)
		r, err := engine.Step(t.Context(), def, st, trg, engine.StepOptions{})
		require.NoError(t, err)
		return r
	}

	r := step(t, engine.InstanceState{InstanceID: "i-nested"}, engine.NewStartInstance(at, nil))
	for _, act := range []string{"do-outer", "do-inner", "hold-inner"} {
		id := invokeIDForAction(r.Commands, act)
		require.NotEmpty(t, id, "fixture: %s must have been dispatched", act)

		// The snapshot taken just before the last completion is the pre-terminal one:
		// both scopes open, both records held live, nothing archived. It is what makes
		// the archive assertions below non-vacuous.
		if act == "hold-inner" {
			require.False(t, r.State.Status.IsTerminal(),
				"fixture: the instance must still be alive here, or there is no pre-terminal snapshot")
			require.Len(t, r.State.Scopes, 2,
				"control: BOTH sub-process scopes must be open, or this test is the single-scope test again")
			require.Equal(t, []string{"outerSub", "innerSub"},
				[]string{r.State.Scopes[0].NodeID, r.State.Scopes[1].NodeID},
				"control: parent before child (openScope append order)")
			require.Len(t, r.State.Scopes[0].Compensations, 1,
				"control: the outer scope holds undoOuter's record, un-archived")
			require.Len(t, r.State.Scopes[1].Compensations, 1,
				"control: the inner scope holds undoInner's record, un-archived")
			require.Empty(t, r.State.ArchivedCompensations,
				"control: nothing archived yet — every key below is the harvest's own work")
			require.Empty(t, r.State.RootCompensations,
				"control: nothing at root either, so no key can be a hoist")
		}

		r = step(t, r.State, engine.NewActionCompleted(at, id, nil))
	}

	require.True(t, r.State.Status.IsTerminal(),
		"fixture: the force-termination end inside the INNERMOST body must have run")

	// The invariant: one archive key per scope, each carrying only its own scope's
	// record. Asserted as the whole projected map so an extra key, a missing key, or a
	// record archived under the WRONG scope all fail — a per-key Contains would pass if
	// the harvest also dumped undoInner under "outerSub".
	assert.Equal(t,
		map[string][]string{
			"outerSub": {"undoOuter"},
			"innerSub": {"undoInner"},
		},
		map[string][]string{
			"outerSub": actionsOf(r.State.ArchivedCompensations["outerSub"]),
			"innerSub": actionsOf(r.State.ArchivedCompensations["innerSub"]),
		},
		"each open scope must archive under ITS OWN sub-process node id, so scope identity "+
			"survives an abnormal exit exactly as it survives a normal one")
	assert.Len(t, r.State.ArchivedCompensations, 2,
		"and no third key: the harvest must not mint a key for a scope that does not exist")
	assert.Empty(t, r.State.RootCompensations,
		"the harvest archives under scope identity; hoisting to root is a walk's job")

	assert.Nil(t, r.State.Scopes,
		"both scopes must be closed, and Scopes must be exactly nil "+
			"(assert.Empty would pass for a non-nil empty slice and so could not see the persisted shape)")
}
