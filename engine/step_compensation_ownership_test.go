package engine_test

// ADR-0173 — a compensation walk's finish consumes exactly the records it
// drained. These are the TARGETED-branch cases; the scope-wide teardown routes
// live in step_compensation_scope_drain_test.go next to the fixtures they reuse.

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
)

// targetedThrowInnerDef is the compensable body both fixtures below embed:
// innerStart → svcInner(doInner/undoInner) → innerEnd. Its records archive under
// the sub-process node's ID on normal exit (ADR-0039).
func targetedThrowInnerDef(extra ...string) *model.ProcessDefinition {
	nodes := []model.Node{
		event.NewStart("innerStart"),
		activity.NewServiceTask("svcInner",
			activity.WithTaskAction("doInner"), activity.WithCompensateAction("undoInner")),
	}
	flows := []flow.SequenceFlow{{ID: "i1", Source: "innerStart", Target: "svcInner"}}
	prev := "svcInner"
	for _, name := range extra {
		id := "svcInner" + name
		nodes = append(nodes, activity.NewServiceTask(id,
			activity.WithTaskAction("doInner"+name), activity.WithCompensateAction("undoInner"+name)))
		flows = append(flows, flow.SequenceFlow{ID: "ix" + name, Source: prev, Target: id})
		prev = id
	}
	nodes = append(nodes, event.NewEnd("innerEnd"))
	flows = append(flows, flow.SequenceFlow{ID: "i2", Source: prev, Target: "innerEnd"})
	return &model.ProcessDefinition{ID: "ownership-inner", Version: 1, Nodes: nodes, Flows: flows}
}

// targetedThrowOnceDef: start → sub → rb(CompensateThrow ref="sub") → end.
// One visit, one record, no concurrency — the narrowest shape that drives a
// targeted walk to its finish.
//
// rb MUST carry an outgoing flow or compensationThrowEventStrategy.enter
// auto-advances and no walk starts at all.
func targetedThrowOnceDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "targeted-once", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", targetedThrowInnerDef()),
			event.NewCompensateThrow("rb", event.WithCompensateRef("sub")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "rb"},
			{ID: "f3", Source: "rb", Target: "end"},
		},
	}
}

// targetedReentryDef re-enters the SAME sub-process node while the walk from the
// first visit is still outstanding, so a second record lands in the one archive
// slot mid-walk:
//
//	start → sub → fork ⇒ { rb(CompensateThrow ref="sub") → endA ;
//	                       sibling(UserTask) → sub  (re-entry) }
//
// archiveCompensations keys the archive by the sub-process node's ID, so both
// visits accumulate under "sub" — the case ADR-0173 Decision 3 exists for.
func targetedReentryDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "targeted-reentry", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", targetedThrowInnerDef()),
			gateway.NewParallel("fork"),
			event.NewCompensateThrow("rb", event.WithCompensateRef("sub")),
			event.NewEnd("endA"),
			activity.NewUserTask("sibling"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "fork"},
			{ID: "f3", Source: "fork", Target: "rb"},
			{ID: "f4", Source: "rb", Target: "endA"},
			{ID: "f5", Source: "fork", Target: "sibling"},
			{ID: "f6", Source: "sibling", Target: "sub"},
		},
	}
}

// TestTargetedThrowFinishLeavesNoEmptyArchiveMap is T8. It fails on main with
// `ArchivedCompensations = map[]` — a non-nil EMPTY map.
//
// ⚠ The wart is PRE-EXISTING and NOT introduced by ADR-0173: on an unmodified
// tree it is applyFinish's whole-key delete that produces it. On THIS branch that
// is no longer the mechanism — the last key is already gone by the time
// applyFinish runs, removed by startCompensationWalk → consumeDispatchedRecord →
// dropArchiveRecordAt, so applyFinish's `ArchivedCompensations != nil` guard
// short-circuits and its delete is never reached. What this test pins is the
// OUTCOME on both paths; putting the nilling in the one shared helper is what
// makes the outcome hold whichever path gets there.
//
// (The attribution above was wrong in the first version of this comment, which
// credited applyFinish for the fix on this branch. Corrected at the delivery gate:
// a mutation swapping deleteArchiveSlot back to a plain delete leaves the suite
// green, which is the proof that path is not the one under test here.)
func TestTargetedThrowFinishLeavesNoEmptyArchiveMap(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	def := targetedThrowOnceDef()
	at := scopeDrainT0
	next := func() time.Time { at = at.Add(time.Second); return at }

	res, err := engine.Step(ctx, def, engine.InstanceState{InstanceID: "t8"},
		engine.NewStartInstance(scopeDrainT0, nil), engine.StepOptions{})
	require.NoError(t, err)

	doInner := firstInvokeCmd(res.Commands, "doInner")
	require.NotEmpty(t, doInner, "control: the sub-process body invokes doInner")
	res, err = engine.Step(ctx, def, res.State,
		engine.NewActionCompleted(next(), doInner, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"undoInner"}, compensationInvokeNames(res.Commands),
		"control: the targeted walk starts and dispatches the archived record")

	walkCmd := res.State.Compensating.ActiveCmdID
	require.NotEmpty(t, walkCmd, "control: the walk is in flight")

	res, err = engine.Step(ctx, def, res.State,
		engine.NewActionCompleted(next(), walkCmd, nil), engine.StepOptions{})
	require.NoError(t, err)

	assert.Empty(t, res.State.Compensating.ActiveCmdID, "control: the walk finished")
	assert.Nil(t, res.State.ArchivedCompensations,
		"an emptied archive must be nil, not a non-nil empty map: InstanceState is "+
			"persisted as JSON and `{}` vs `null` is a gratuitous difference in a stored shape")
}

// TestTargetedThrowRetainsARecordItNeverDrained is T3. On main the finish runs
// delete(ArchivedCompensations, "sub") — the WHOLE key — so the second visit's
// record, which the walk pinned nothing of and never compensated, is silently
// destroyed: measured `archive={}`, `root=0`, and the deferred throw that pops at
// finish finds an empty slot.
//
// This is ADR-0120 review A1's rule — clear only the prefix you drained — present
// on the scope-wide branch and absent on the targeted one.
func TestTargetedThrowRetainsARecordItNeverDrained(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	def := targetedReentryDef()
	at := scopeDrainT0
	next := func() time.Time { at = at.Add(time.Second); return at }

	res, err := engine.Step(ctx, def, engine.InstanceState{InstanceID: "t3"},
		engine.NewStartInstance(scopeDrainT0, nil), engine.StepOptions{})
	require.NoError(t, err)

	// First visit: archives one record, and the throw starts a walk over it.
	first := firstInvokeCmd(res.Commands, "doInner")
	require.NotEmpty(t, first, "control: the first visit invokes doInner")
	res, err = engine.Step(ctx, def, res.State,
		engine.NewActionCompleted(next(), first, nil), engine.StepOptions{})
	require.NoError(t, err)
	walkCmd := res.State.Compensating.ActiveCmdID
	require.NotEmpty(t, walkCmd, "control: the targeted walk is in flight")
	require.Len(t, res.State.Compensating.Records, 1,
		"control: the walk pinned exactly the first visit's record")
	dispatched := compensationInvokeNames(res.Commands)
	require.Equal(t, []string{"undoInner"}, dispatched, "control: it dispatched that record")

	// The sibling re-enters "sub" while that walk is still outstanding, appending
	// a SECOND record to the same archive slot.
	human := firstAwaitHuman(res.Commands)
	require.NotNil(t, human, "control: the sibling parks on its user task")
	res, err = engine.Step(ctx, def, res.State,
		engine.NewHumanCompleted(next(), human.TaskID, engine.CompletionInput{}, authz.Actor{ID: "u1"}),
		engine.StepOptions{})
	require.NoError(t, err)
	second := firstInvokeCmd(res.Commands, "doInner")
	require.NotEmpty(t, second, "control: the re-entry invokes doInner again")
	res, err = engine.Step(ctx, def, res.State,
		engine.NewActionCompleted(next(), second, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, walkCmd, res.State.Compensating.ActiveCmdID,
		"control: the re-entry did not disturb the in-flight walk")
	require.Len(t, res.State.DeferredCompensationThrows, 1,
		"control: the re-entry's own throw is deferred behind the live walk (ADR-0071)")

	// The first walk finishes. It must consume ONLY what it drained.
	res, err = engine.Step(ctx, def, res.State,
		engine.NewActionCompleted(next(), walkCmd, nil), engine.StepOptions{})
	require.NoError(t, err)

	assert.Equal(t, []string{"undoInner"}, compensationInvokeNames(res.Commands),
		"the popped deferred throw compensates the second visit's retained record; "+
			"on main it finds an empty slot and nothing runs")
	assert.NotEmpty(t, res.State.Compensating.ActiveCmdID,
		"a walk over the retained record is now in flight")
}

// TestLegacyTargetedCursorStillConsumesItsWholeArchiveKey covers
// finishPlan.archiveConsumed, whose contract the ADR commits to but which had NO
// test: a targeted cursor persisted before ADR-0171 pinned no snapshot, so it
// consumed nothing as it dispatched and the finish must still delete its whole
// archive key — the single-ownership consume that has always been main's
// behaviour.
//
// It goes RED when the `!plan.archiveConsumed ||` disjunct is dropped: the slot is
// then non-empty at the finish, nothing is deleted, and the records this walk
// DISPATCHED survive for a later walk to run a second time. Measured — without
// this test that mutation leaves the whole engine suite at EXIT=0.
//
// Two compensable records are needed. With one, the walk consumes it at start and
// the slot is empty either way, so the disjunct is an identity and the test cannot
// discriminate.
func TestLegacyTargetedCursorStillConsumesItsWholeArchiveKey(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	def := &model.ProcessDefinition{
		ID: "targeted-legacy", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", targetedThrowInnerDef("2")),
			event.NewCompensateThrow("rb", event.WithCompensateRef("sub")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "rb"},
			{ID: "f3", Source: "rb", Target: "end"},
		},
	}
	at := scopeDrainT0
	next := func() time.Time { at = at.Add(time.Second); return at }

	res, err := engine.Step(ctx, def, engine.InstanceState{InstanceID: "legacy"},
		engine.NewStartInstance(scopeDrainT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	var dispatched []string
	for _, action := range []string{"doInner", "doInner2"} {
		cmdID := firstInvokeCmd(res.Commands, action)
		require.NotEmpty(t, cmdID, "control: %q invoked", action)
		res, err = engine.Step(ctx, def, res.State,
			engine.NewActionCompleted(next(), cmdID, nil), engine.StepOptions{})
		require.NoError(t, err)
		dispatched = append(dispatched, compensationInvokeNames(res.Commands)...)
	}
	require.Equal(t, []string{"undoInner2"}, dispatched,
		"control: the targeted walk started and dispatched the most recent record")

	// The round-trip through a row written before ADR-0171: no pinned Records.
	st := res.State
	st.Compensating.Records = nil
	require.NotEmpty(t, st.Compensating.ArchiveKey, "control: the cursor still names its slot")

	for st.Compensating.ActiveCmdID != "" {
		cmdID := st.Compensating.ActiveCmdID
		var r engine.StepResult
		r, err = engine.Step(ctx, def, st, engine.NewActionCompleted(next(), cmdID, nil),
			engine.StepOptions{})
		require.NoError(t, err)
		dispatched = append(dispatched, compensationInvokeNames(r.Commands)...)
		st = r.State
	}
	assert.Equal(t, []string{"undoInner2", "undoInner"}, dispatched,
		"control: reading its slot live, the legacy walk still reaches the second record")
	assert.Nil(t, st.ArchivedCompensations,
		"a walk that consumed nothing as it dispatched must still consume its whole key at "+
			"the finish, or the records it DID dispatch are left for a later walk to re-run")
}
