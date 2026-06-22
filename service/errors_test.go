// Package service_test is the black-box test suite for the service facade — error classification.
package service_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/service"
)

// TestErrConflict_ClosedTask verifies that ClaimTask returns service.ErrConflict
// when the targeted task is already closed (Completed or Cancelled).
func TestErrConflict_ClosedTask(t *testing.T) {
	def := approvalDef()
	h := newHarness(t, def)
	svc := service.New(h.runner, h.tasks, h.reg, h.store, h.lister, h.taskStore, h.clk)

	ctx := t.Context()

	// Start the instance — parks at the user task node.
	parked, err := h.runner.Run(ctx, def, "conflict-closed-task", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status, "must park at user task")
	require.Len(t, parked.Tokens, 1)
	taskToken := parked.Tokens[0].AwaitCommand
	require.NotEmpty(t, taskToken, "task token must be set")

	// Forcibly mark the task as Completed in the task store (simulates a
	// race or a task that was already completed/cancelled).
	task, err := h.taskStore.Get(ctx, taskToken)
	require.NoError(t, err)
	task.State = humantask.Completed
	require.NoError(t, h.taskStore.Upsert(ctx, task))

	// Claiming a closed (Completed) task must return ErrConflict.
	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	_, err = svc.ClaimTask(ctx, service.ClaimTaskRequest{
		TaskToken: taskToken,
		Actor:     manager,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, service.ErrConflict, "claiming a closed task must return ErrConflict")
}

// TestErrConflict_TerminalInstance verifies that DeliverSignal returns
// service.ErrConflict when the targeted instance has already reached a
// terminal state (Completed, Failed, or Terminated).
func TestErrConflict_TerminalInstance(t *testing.T) {
	def := linearDef()
	h := newHarness(t, def)
	svc := service.New(h.runner, h.tasks, h.reg, h.store, h.lister, h.taskStore, h.clk)

	ctx := t.Context()

	// Start a linear process — it runs to completion immediately.
	completed, err := h.runner.Run(ctx, def, "conflict-terminal-inst", map[string]any{"name": "test"})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, completed.Status, "linear process must complete immediately")

	// Delivering a signal to an already-completed instance must return ErrConflict.
	_, err = svc.DeliverSignal(ctx, service.DeliverSignalRequest{
		InstanceID: "conflict-terminal-inst",
		Signal:     "any-signal",
		Payload:    nil,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, service.ErrConflict, "delivering signal to terminal instance must return ErrConflict")
}

// TestErrConflict_CancelledTask verifies that ClaimTask returns service.ErrConflict
// when the task state is Cancelled (another form of closed).
func TestErrConflict_CancelledTask(t *testing.T) {
	def := approvalDef()
	h := newHarness(t, def)
	svc := service.New(h.runner, h.tasks, h.reg, h.store, h.lister, h.taskStore, h.clk)

	ctx := t.Context()

	// Seed a Cancelled task directly into the task store. The instance must also
	// exist in the instance store so resolveDefinition can load it (the task-closed
	// guard fires first, so we only need the taskStore entry to be closed).
	closedTask := humantask.HumanTask{
		TaskToken:  "cancelled-task-token",
		InstanceID: "any-instance-id",
		NodeID:     "approve",
		State:      humantask.Cancelled,
		CreatedAt:  h.clk.Now(), // use the test harness clock for time-source consistency
	}
	require.NoError(t, h.taskStore.Upsert(ctx, closedTask))

	// ClaimTask on a Cancelled task must return ErrConflict.
	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	_, err := svc.ClaimTask(ctx, service.ClaimTaskRequest{
		TaskToken: "cancelled-task-token",
		Actor:     manager,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, service.ErrConflict, "claiming a cancelled task must return ErrConflict")
}

// TestErrConflict_EngineWrongStateClassified verifies that a wrong-state error
// that escapes the pre-flight guards (task is Open, instance is non-terminal) is
// classified as service.ErrConflict with the engine cause still inspectable.
//
// Mechanism: we seed a synthetic Open task into the shared taskStore whose
// TaskToken does not exist in the engine's live token set. The pre-flight checks
// pass (task.IsOpen() == true, instance.Status == Running), but the engine's
// step handler cannot find the token and returns ErrTokenNotFound, which wraps
// engine.ErrInvalidTransition via the Task 1 sentinel chain.
func TestErrConflict_EngineWrongStateClassified(t *testing.T) {
	def := approvalDef()
	h := newHarness(t, def)
	svc := service.New(h.runner, h.tasks, h.reg, h.store, h.lister, h.taskStore, h.clk)

	ctx := t.Context()

	// Start the instance — parks at the user task node; the engine owns token T1.
	parked, err := h.runner.Run(ctx, def, "race-instance", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status, "must park at user task")

	// Seed a synthetic Open task that references the running instance but whose
	// TaskToken is unknown to the engine. Pre-flight passes; engine rejects it.
	syntheticTask := humantask.HumanTask{
		TaskToken:   "synthetic-ghost-token",
		InstanceID:  "race-instance",
		NodeID:      "approve",
		State:       humantask.Unclaimed,
		Eligibility: authz.AuthzSpec{}, // open access — bypasses role check
		CreatedAt:   h.clk.Now(),
	}
	require.NoError(t, h.taskStore.Upsert(ctx, syntheticTask))

	// ClaimTask: pre-flight passes (Open task, Running instance), engine rejects
	// with ErrTokenNotFound (wraps ErrInvalidTransition) → must be ErrConflict.
	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	_, err = svc.ClaimTask(ctx, service.ClaimTaskRequest{
		TaskToken: "synthetic-ghost-token",
		Actor:     manager,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, service.ErrConflict,
		"an engine wrong-state error leaking past pre-flight must classify as ErrConflict")
	require.ErrorIs(t, err, engine.ErrInvalidTransition,
		"the engine cause stays inspectable through the wrap")
}

// TestErrConflict_HappyPath_ClaimOpen verifies that claiming a genuinely Open task
// does NOT return ErrConflict — the happy-path contract for ClaimTask.
func TestErrConflict_HappyPath_ClaimOpen(t *testing.T) {
	def := approvalDef()
	h := newHarness(t, def)
	svc := service.New(h.runner, h.tasks, h.reg, h.store, h.lister, h.taskStore, h.clk)

	ctx := t.Context()

	// Start the instance — parks at the user task node with the task in Open state.
	parked, err := h.runner.Run(ctx, def, "happy-claim-open", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status, "must park at user task")
	require.Len(t, parked.Tokens, 1)
	taskToken := parked.Tokens[0].AwaitCommand
	require.NotEmpty(t, taskToken, "task token must be set")

	// Claiming the still-open task must succeed without conflict.
	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	_, claimErr := svc.ClaimTask(ctx, service.ClaimTaskRequest{
		TaskToken: taskToken,
		Actor:     manager,
	})
	require.NoError(t, claimErr, "claiming an Open task must succeed (no ErrConflict on the happy path)")
}
