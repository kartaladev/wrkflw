package engine

// White-box by necessity: the state this file exercises cannot be produced by
// driving the engine any more (ADR-0171 stops it arising), only DESERIALIZED
// from a row written before ADR-0171. Building it means writing the unexported
// compensationCursor directly.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
)

// TestCompensationAdvanceFinishesWhenRecordSourceIsGone covers ADR-0171's third
// element: a walk whose record source has vanished must route to the walk's
// FINISH, never to an index expression.
//
// The only way to reach it after ADR-0171 is a rolling upgrade. A cursor pinned
// by startCompensationWalk always carries its Records, and exitSubprocessScope
// now holds a scope a live walk names — but a walk that was already in flight
// when the process restarted was persisted by the OLD code: its Records is nil,
// so cursorRecords falls back to the live read, and that read returns nil once
// the scope has been closed. Before the bounds check,
// stepCompensationAdvance's `records[nextIdx]` panicked on exactly this state
// (`panic: runtime error: index out of range [0] with length 0`) — inside the
// pure engine core, i.e. in the library consumer's own process.
//
// What makes this test fail without the fix: NextIndex is 1 (the walk had
// emitted the second of two records) while the live source resolves to nil, so
// nextIdx is 0, `0 >= 0` and `0 > toNodeIdx(-1)` both hold, and the read of
// records[0] is out of range.
func TestCompensationAdvanceFinishesWhenRecordSourceIsGone(t *testing.T) {
	t.Parallel()

	def := &model.ProcessDefinition{
		ID: "p-migrated-cursor", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithTaskAction("do")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
		},
	}
	at := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)

	// A scope-wide throw walk as the pre-ADR-0171 code persisted it: no pinned
	// Records, a ScopeID naming the sub-process scope it was walking, and a
	// resume target at the root scope. The scope itself is absent — a sibling
	// branch closed it while the walk was outstanding, which is the whole defect.
	state := InstanceState{
		InstanceID: "migrated",
		DefID:      def.ID,
		DefVersion: def.Version,
		Status:     StatusCompensating,
		StartedAt:  at,
		Compensating: compensationCursor{
			ScopeID:          "migrated-s1",
			ResumeNode:       "end",
			ResumeScope:      "",
			StartRecordCount: 2,
			NextIndex:        1,
			ActiveCmdID:      "migrated-c9",
		},
	}
	require.Nil(t, state.Compensating.Records,
		"control: the migrated cursor carries no pinned records")
	require.Nil(t, state.scopeByID("migrated-s1"),
		"control: the walk's record source no longer exists")

	res, err := Step(t.Context(), def, state,
		NewActionCompleted(at.Add(time.Second), "migrated-c9", nil), StepOptions{})

	require.NoError(t, err)
	require.Empty(t, res.State.Compensating.ActiveCmdID,
		"the vanished source must finish the walk, not advance it")
	require.Equal(t, StatusCompleted, res.State.Status,
		"the finish resumes at the throw's successor, which ends the instance")
}

// TestRetryOnAVanishedRecordSourceFinishesWithoutPanicking is T13.
//
// Retry indexes records[cur.NextIndex], and it needs its OWN bounds check:
// ADR-0171's third disjunct in stepCompensationAdvance guards NextIndex-1, which
// says nothing about NextIndex itself. On the migrated cursor below the live
// source resolves to nil while NextIndex is 1, so a naive index PANICS inside the
// pure engine core — in the library consumer's own process.
//
// What makes it fail without the bounds check: len(records)==0 and NextIndex==1,
// so records[1] is out of range.
func TestRetryOnAVanishedRecordSourceFinishesWithoutPanicking(t *testing.T) {
	t.Parallel()

	def := &model.ProcessDefinition{
		ID: "p-migrated-retry", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithTaskAction("do")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
		},
	}
	at := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)

	state := InstanceState{
		InstanceID: "migrated-retry",
		DefID:      def.ID,
		DefVersion: def.Version,
		Status:     StatusCompensating,
		StartedAt:  at,
		Compensating: compensationCursor{
			ScopeID:          "migrated-s1",
			ResumeNode:       "end",
			StartRecordCount: 2,
			NextIndex:        1,
			ActiveCmdID:      "migrated-c9",
		},
	}
	require.Nil(t, state.scopeByID("migrated-s1"),
		"control: the walk's record source no longer exists")
	require.Empty(t, cursorRecords(&state, state.Compensating),
		"control: len(records)==0 while NextIndex==1 — the shape that panics")

	require.NotPanics(t, func() {
		res, err := Step(t.Context(), def, state,
			NewRetryStalledCompensation(at.Add(time.Second), "migrated-c9", ""), StepOptions{})

		require.NoError(t, err)
		assert.Empty(t, res.State.Compensating.ActiveCmdID,
			"a vanished source must route retry to the walk FINISH, not to an index")
		assert.Equal(t, StatusCompleted, res.State.Status,
			"the finish resumes at the throw successor, which ends the instance")
	})
}

// TestDeferredCancelFromLegacySnapshotTerminatesAsCancelled covers the one input
// applyFinish's zero-value pending outcome still describes.
//
// Both deferral sites now stamp PendingFinalStatus/PendingFinalErr explicitly,
// so a state this engine WRITES never reaches the default. A row persisted by an
// older build does: its handleCancelRequested set PendingCancel alone. The
// default must keep decoding that as the ADR-0039 cancel outcome
// (StatusTerminated / "cancelled") rather than terminating the instance with an
// empty error code.
//
// What makes this test fail without the default: pendingStatus stays
// StatusRunning, applyTerminate's own unset-status mapping still yields
// StatusTerminated, but pendingErr stays "" — so no FailInstance is emitted at
// all and the cancelled instance closes with no reason recorded.
func TestDeferredCancelFromLegacySnapshotTerminatesAsCancelled(t *testing.T) {
	t.Parallel()

	def := &model.ProcessDefinition{
		ID: "p-legacy-pending-cancel", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc",
				activity.WithTaskAction("do"), activity.WithCompensateAction("undo")),
			event.NewCompensateThrow("rb"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "rb"},
			{ID: "f3", Source: "rb", Target: "end"},
		},
	}
	at := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)

	// A root scope-wide throw walk with a cancel deferred behind it, exactly as an
	// older build persisted it: PendingCancel set, the outcome fields left zero.
	state := InstanceState{
		InstanceID: "legacy-cancel",
		DefID:      def.ID,
		DefVersion: def.Version,
		Status:     StatusCompensating,
		StartedAt:  at,
		Compensating: compensationCursor{
			ResumeNode:       "end",
			StartRecordCount: 1,
			NextIndex:        0,
			ActiveCmdID:      "legacy-cancel-c9",
			Records: []CompensationRecord{
				{NodeID: "svc", Action: "undo", CompletedAt: at},
			},
		},
		PendingCancel: true,
	}
	require.Zero(t, state.PendingFinalStatus,
		"control: an older build recorded no outcome alongside PendingCancel")

	res, err := Step(t.Context(), def, state,
		NewActionCompleted(at.Add(time.Second), "legacy-cancel-c9", nil), StepOptions{})

	require.NoError(t, err)
	require.False(t, res.State.PendingCancel, "the deferred cancel must be consumed")
	require.Equal(t, StatusTerminated, res.State.Status,
		"a legacy deferred cancel must still terminate the instance")
	var failures []FailInstance
	for _, c := range res.Commands {
		if fi, ok := c.(FailInstance); ok {
			failures = append(failures, fi)
		}
	}
	require.Len(t, failures, 1, "exactly one terminal command; cmds=%#v", res.Commands)
	require.Equal(t, "cancelled", failures[0].Err,
		"the legacy default must decode as the ADR-0039 cancel outcome")
}

// TestCompensationResumeSurfacesAnUnresolvableScopeAsAnError draws the line
// under applyFinish's resume recovery: it drops the resume when the scope is
// GONE, and only then. A scope that still exists but cannot be resolved to a
// definition is a corrupt snapshot, not a race, and must surface as an error
// rather than be silently swallowed or panicked on.
//
// This is the state a row can carry after its definition changed underneath it:
// the Scope survives, but the sub-process node that opened it no longer exists
// in the parent definition.
//
// What makes this test fail without the error propagation: drive's own
// defForScope failure would have to be dropped for the Step to return nil, and
// applyFinish would return a StepResult built over a token nobody can advance.
func TestCompensationResumeSurfacesAnUnresolvableScopeAsAnError(t *testing.T) {
	t.Parallel()

	def := &model.ProcessDefinition{
		ID: "p-unresolvable-resume", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithTaskAction("do")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
		},
	}
	at := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)

	state := InstanceState{
		InstanceID: "unresolvable",
		DefID:      def.ID,
		DefVersion: def.Version,
		Status:     StatusCompensating,
		StartedAt:  at,
		// The scope EXISTS, so the resume is not dropped; its opening node does
		// not, so defForScope cannot resolve a definition for it.
		Scopes: []Scope{{ID: "unresolvable-s1", NodeID: "ghost", ParentID: ""}},
		Compensating: compensationCursor{
			ScopeID:          "unresolvable-s1",
			ResumeNode:       "innerResume",
			ResumeScope:      "unresolvable-s1",
			StartRecordCount: 1,
			NextIndex:        0,
			ActiveCmdID:      "unresolvable-c9",
			Records: []CompensationRecord{
				{NodeID: "svc", Action: "undo", CompletedAt: at},
			},
		},
	}
	require.NotNil(t, state.scopeByID("unresolvable-s1"),
		"control: the resume scope still exists, so the resume is attempted")

	_, err := Step(t.Context(), def, state,
		NewActionCompleted(at.Add(time.Second), "unresolvable-c9", nil), StepOptions{})

	require.EqualError(t, err,
		`workflow-engine: defForScope: sub-process node "ghost" not found in parent definition`,
		"an unresolvable resume scope must surface, not be swallowed")
}
