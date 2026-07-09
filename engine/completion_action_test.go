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

// receiveCompletionDef returns a linear definition with a single receive-task
// node carrying a CompletionAction between start and end.
//
//	Start → ReceiveTask(r1, message=m, completion=ackOrder) → End
//
// The node also carries a RetryPolicy{MaxAttempts: 1}. This is required, not
// incidental: per WithCompletionAction's documented contract, a completion
// action's failure is governed by the node's WithRetryPolicy / error boundary
// — "the same machinery as a ServiceTask action". Without an explicit policy,
// effectiveRetryPolicy has nothing to key off and an unhandled ActionFailed
// fails the instance outright (existing behavior for any action, not specific
// to completion actions). MaxAttempts:1 makes the very first failure terminal
// so the retry-exhaustion branch's incident fallback (used when no
// catch-flow/boundary handles the terminal error) is exercised.
func receiveCompletionDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-rc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewReceiveTask("r1", "m",
				activity.WithCompletionAction("ackOrder"),
				activity.WithRetryPolicy(&model.RetryPolicy{MaxAttempts: 1}),
			),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "r1"},
			{ID: "f2", Source: "r1", Target: "end"},
		},
	}
}

// TestReceiveTaskCompletionAction_ParksThenAdvances verifies that a message
// resolving a parked ReceiveTask token whose node carries a CompletionAction
// does NOT advance the token immediately. Instead it emits an InvokeAction for
// the completion action and parks the token on the command round-trip; the
// instance only completes once the corresponding ActionCompleted arrives.
func TestReceiveTaskCompletionAction_ParksThenAdvances(t *testing.T) {
	def := receiveCompletionDef()
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)
	r2, err := engine.Step(def, r1.State, engine.NewMessageReceived(t0, "m", "", map[string]any{"orderID": "o1"}), engine.StepOptions{})
	require.NoError(t, err)
	var cmdID string
	for _, c := range r2.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "ackOrder" {
			cmdID = ia.CommandID
		}
	}
	require.NotEmpty(t, cmdID, "completion should emit InvokeAction for ackOrder")
	assert.NotEqual(t, engine.StatusCompleted, r2.State.Status, "must not complete before the action returns")
	r3, err := engine.Step(def, r2.State, engine.NewActionCompleted(t0, cmdID, map[string]any{"acked": true}), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r3.State.Status)
	assert.Equal(t, true, r3.State.Variables["acked"])
}

// TestCompletionAction_FailureRaisesIncidentWhenNoRetryOrBoundary verifies
// that a non-retryable terminal failure of a completion action reuses the
// existing ActionFailed retry/incident/boundary machinery unchanged: the
// parked token is an ordinary action-awaiting token, so no completion-action
// specific failure handling is required.
func TestCompletionAction_FailureRaisesIncidentWhenNoRetryOrBoundary(t *testing.T) {
	def := receiveCompletionDef()
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"}, engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)
	r2, err := engine.Step(def, r1.State, engine.NewMessageReceived(t0, "m", "", nil), engine.StepOptions{})
	require.NoError(t, err)
	var cmdID string
	for _, c := range r2.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "ackOrder" {
			cmdID = ia.CommandID
		}
	}
	require.NotEmpty(t, cmdID)
	r3, err := engine.Step(def, r2.State,
		engine.NewActionFailed(t0, cmdID, "boom", false), engine.StepOptions{}) // non-retryable
	require.NoError(t, err)
	assert.Len(t, r3.State.Incidents, 1, "terminal completion-action failure raises an incident")
}
