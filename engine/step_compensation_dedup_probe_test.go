package engine_test

// step_compensation_dedup_probe_test.go — ADR-0071 dedup investigation.
//
// The HANDOVER flagged a "partial-rollback record-retention hazard (no
// recordCompensation dedup)". This probe attempts to reproduce a genuine
// DOUBLE-record of the same activity execution. The hypothesis under test is the
// spec's: the ActionCompleted call site (step_triggers.go) is already idempotent
// because a duplicate/stale ActionCompleted for the same CommandID hits
// tokenAwaiting -> ErrTokenNotFound BEFORE reaching recordCompensation.
//
// If this probe ever shows a SECOND record appearing for one execution, that is a
// reproducing failure and recordCompensation needs a (token-id+node-id) dedup. As
// of ADR-0071 it does NOT reproduce: the duplicate is rejected with
// ErrTokenNotFound and exactly one record exists.

import (
	"errors"
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

// dedupProbeDef: start → svc(CompensateAction "cancel-svc") → userTask → end.
// svc is a single compensable activity; the userTask parks so we can inspect the
// recorded compensation without the instance completing.
func dedupProbeDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "dedup-probe", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithTaskAction("book"), activity.WithCompensateAction("cancel-svc")),
			activity.NewUserTask("userTask"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "userTask"},
			{ID: "f3", Source: "userTask", Target: "end"},
		},
	}
}

// TestRecordCompensationDoubleRecordIsRejected confirms a duplicate ActionCompleted
// for the same CommandID does NOT produce a second compensation record: the second
// delivery is rejected with ErrTokenNotFound before reaching recordCompensation.
func TestRecordCompensationDoubleRecordIsRejected(t *testing.T) {
	at := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	def := dedupProbeDef()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "dedup-inst"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1)
	ia, ok := r1.Commands[0].(engine.InvokeAction)
	require.True(t, ok)
	require.Equal(t, "book", ia.Name)

	// First ActionCompleted: records compensation once, parks at userTask.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(at.Add(time.Second), ia.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.State.RootCompensations, 1,
		"svc must be recorded exactly once after the first ActionCompleted")

	// Duplicate ActionCompleted for the SAME CommandID: the token already advanced
	// (AwaitCommand cleared), so tokenAwaiting finds nothing → ErrTokenNotFound.
	_, err = engine.Step(t.Context(), def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), ia.CommandID, nil),
		engine.StepOptions{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, engine.ErrTokenNotFound),
		"a duplicate ActionCompleted must be rejected with ErrTokenNotFound, never double-recorded")

	// The state from the successful r2 step still holds exactly one record (Step is
	// pure: the rejected duplicate produced no new state).
	assert.Len(t, r2.State.RootCompensations, 1,
		"no second compensation record may appear for one execution")
}
