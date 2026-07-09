package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// userTaskCompletionDef returns a linear definition with a single user-task
// node carrying a CompletionAction between start and end.
//
//	Start → UserTask(u1, completion=recordApproval) → End
func userTaskCompletionDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-uc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("u1", []string{"r"}, activity.WithCompletionAction("recordApproval")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "u1"},
			{ID: "f2", Source: "u1", Target: "end"},
		},
	}
}

// TestUserTaskCompletionAction_ParksThenAdvancesOnActionCompleted verifies that
// completing a human task whose UserTask node carries a CompletionAction does
// NOT advance the token immediately. Instead it emits an InvokeAction for the
// completion action and parks the token on the command round-trip; the
// instance only completes once the corresponding ActionCompleted arrives, and
// the action's output is merged alongside the human-task output.
func TestUserTaskCompletionAction_ParksThenAdvancesOnActionCompleted(t *testing.T) {
	def := userTaskCompletionDef()
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)
	tok := r1.State.Tokens[0]
	require.Equal(t, "u1", tok.NodeID)
	taskToken := r1.State.Tasks[0].TaskToken // task record created alongside the parked UserTask token

	// Complete the human task: expect an InvokeAction for the completion action,
	// and the instance NOT yet complete (token parked on the action).
	r2, err := engine.Step(def, r1.State,
		engine.NewHumanCompleted(t0, taskToken, map[string]any{"approved": true}, authz.Actor{ID: "alice"}),
		engine.StepOptions{})
	require.NoError(t, err)
	var cmdID string
	for _, c := range r2.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "recordApproval" {
			cmdID = ia.CommandID
		}
	}
	require.NotEmpty(t, cmdID, "completion should emit InvokeAction for recordApproval")
	assert.NotEqual(t, engine.StatusCompleted, r2.State.Status, "must not complete before the action returns")

	// Action returns → token advances to end → instance completes, action output merged.
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(t0, cmdID, map[string]any{"recorded": true}), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r3.State.Status)
	assert.Equal(t, true, r3.State.Variables["recorded"])
	assert.Equal(t, true, r3.State.Variables["approved"])
}
