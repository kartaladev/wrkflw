package engine_test

// step_compensation_outcome_test.go — regression lock for the non-zero outcome
// branch of stepCompensationFinish (Fix 2 from the Task-1 code review).
//
// The branch that emits FailInstance{Err: FinalErr} and applies a non-default
// FinalStatus shipped in commit dfc8b49 but was only indirectly covered. This
// file tests it directly via the BeginCompensation export-test shim.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// oneCompensableDef returns a minimal definition with one compensable service task
// (CompensateAction "refund") so that beginCompensation has one record to emit.
//
//	start → svc(CompensateAction:"refund") → end
func oneCompensableDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "one-comp", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithTaskAction("charge"), activity.WithCompensateAction("refund")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
		},
	}
}

// TestBeginCompensationNonZeroOutcomeFailed verifies that when beginCompensation is
// called with finalStatus=StatusFailed and finalErr="boom", driving through all
// compensation steps results in:
//   - final s.Status == StatusFailed
//   - a FailInstance{Err: "boom"} command in the final result
//
// This locks the non-zero FinalStatus/FinalErr branch of stepCompensationFinish
// against regression.
func TestBeginCompensationNonZeroOutcomeFailed(t *testing.T) {
	at := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)
	def := oneCompensableDef()

	// Build a state that already has one compensation record (svc completed).
	// We construct this by driving an instance through startup + ActionCompleted,
	// but parking before the end event is reached — however, the easiest approach
	// is to build the state with a pre-seeded RootCompensations entry and call
	// BeginCompensation directly (testing the function in isolation).
	state := engine.InstanceState{
		InstanceID: "outcome-test-1",
		Status:     engine.StatusCompensating, // caller contract: set before calling
		RootCompensations: []engine.CompensationRecord{
			{
				NodeID:      "svc",
				Action:      "refund",
				CompletedAt: at.Add(-5 * time.Second),
				Input:       map[string]any{"amount": 99},
			},
		},
	}

	// Call BeginCompensation with a NON-ZERO outcome: StatusFailed + "boom".
	r1, err := engine.BeginCompensation(def, &state, "", engine.StatusFailed, "boom", at, engine.Macro)
	require.NoError(t, err)

	// The cursor is now set; one InvokeAction for "refund" must be emitted.
	assert.Equal(t, engine.StatusCompensating, r1.State.Status)
	require.Len(t, r1.Commands, 1, "beginCompensation must emit exactly one InvokeAction for the sole record")
	ia, ok := r1.Commands[0].(engine.InvokeAction)
	require.True(t, ok, "command must be InvokeAction")
	assert.Equal(t, "refund", ia.Name)

	// ApplyTrigger ActionCompleted for the compensation action — this triggers stepCompensationFinish.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(1*time.Second), ia.CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)

	// Assert non-zero outcome: Status must be StatusFailed (not StatusTerminated).
	assert.Equal(t, engine.StatusFailed, r2.State.Status,
		"full rollback with FinalStatus=StatusFailed must leave instance in StatusFailed")

	// Assert FailInstance{Err:"boom"} is among the result commands.
	var gotFail *engine.FailInstance
	for _, cmd := range r2.Commands {
		if f, ok2 := cmd.(engine.FailInstance); ok2 {
			f := f
			gotFail = &f
			break
		}
	}
	require.NotNil(t, gotFail, "a FailInstance command must be emitted when FinalErr is non-empty")
	assert.Equal(t, "boom", gotFail.Err, "FailInstance.Err must match the FinalErr set on the cursor")
}
