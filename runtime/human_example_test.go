package runtime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// approvalDef returns a minimal process: start → userTask("approve", role "manager") → end.
func approvalDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "approval",
		Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "approve", Kind: model.KindUserTask, CandidateRoles: []string{"manager"}},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}
}

// TestHumanTaskEndToEnd tests the full human-task lifecycle:
//
//  1. Run parks at the user task.
//  2. TaskStore.ClaimableBy returns the task for a manager actor.
//  3. TaskService.Claim → Runner.Deliver(HumanClaimed) transitions the task to Claimed.
//  4. TaskService.Complete → Runner.Deliver(HumanCompleted) completes the instance.
//  5. Journal shows StartInstance + HumanClaimed + HumanCompleted.
//  6. Final task State==Completed and ClaimedBy==manager actor ID.
func TestHumanTaskEndToEnd(t *testing.T) {
	ctx := t.Context()

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}

	// Wire up in-memory ports.
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"manager": {manager},
	})
	az := authz.RoleAuthorizer{}
	clk := clock.System()
	jnl := runtime.NewMemJournal()

	r := runtime.NewRunner(
		nil, // no service actions needed for this process
		clk,
		runtime.NewMemStateStore(),
		jnl,
		runtime.NewMemOutbox(),
		resolver,
		taskStore,
		az,
	)

	def := approvalDef()
	const instanceID = "inst-1"

	// --- Run: parks at the user task ---
	parkedState, err := r.Run(ctx, def, instanceID, nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, parkedState.Status, "instance should be parked (running) at the user task")
	require.Len(t, parkedState.Tokens, 1, "exactly one parked token")
	assert.Equal(t, "approve", parkedState.Tokens[0].NodeID)

	// --- TaskStore.ClaimableBy returns the task ---
	claimable, err := taskStore.ClaimableBy(ctx, manager)
	require.NoError(t, err)
	require.Len(t, claimable, 1, "manager should see one claimable task")
	task := claimable[0]
	assert.Equal(t, instanceID, task.InstanceID)
	assert.Equal(t, humantask.Unclaimed, task.State)

	taskToken := task.TaskToken

	// --- TaskService.Claim → Deliver ---
	svc := runtime.NewTaskService(taskStore, az, clk)

	claimTrg, err := svc.Claim(ctx, taskToken, manager)
	require.NoError(t, err)

	claimedState, err := r.Deliver(ctx, def, instanceID, claimTrg)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, claimedState.Status, "instance still running after claim")

	// Verify task is Claimed in the store.
	storedTask, err := taskStore.Get(ctx, taskToken)
	require.NoError(t, err)
	assert.Equal(t, humantask.Claimed, storedTask.State)
	assert.Equal(t, manager.ID, storedTask.ClaimedBy)

	// --- TaskService.Complete → Deliver ---
	completeTrg, err := svc.Complete(ctx, taskToken, manager, map[string]any{"approved": true})
	require.NoError(t, err)

	finalState, err := r.Deliver(ctx, def, instanceID, completeTrg)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, finalState.Status)
	assert.Empty(t, finalState.Tokens, "no tokens remain after completion")

	// Final task state.
	finalTask, err := taskStore.Get(ctx, taskToken)
	require.NoError(t, err)
	assert.Equal(t, humantask.Completed, finalTask.State)
	assert.Equal(t, manager.ID, finalTask.ClaimedBy)

	// Journal: StartInstance + HumanClaimed + HumanCompleted (Run's StartInstance
	// plus two Deliver calls).
	entries := jnl.Entries(instanceID)
	require.Len(t, entries, 3, "journal must record StartInstance + HumanClaimed + HumanCompleted")
	assert.IsType(t, engine.StartInstance{}, entries[0])
	assert.IsType(t, engine.HumanClaimed{}, entries[1])
	assert.IsType(t, engine.HumanCompleted{}, entries[2])

	// All OccurredAt timestamps must be non-zero.
	for i, e := range entries {
		assert.False(t, e.OccurredAt().IsZero(), "entry %d OccurredAt must not be zero", i)
	}

	// Output variable merged into state.
	assert.Equal(t, true, finalState.Variables["approved"])

	// ActorID on the NodeVisit for the user-task node.
	var userVisit *engine.NodeVisit
	for i := range finalState.History {
		if finalState.History[i].NodeID == "approve" {
			userVisit = &finalState.History[i]
		}
	}
	require.NotNil(t, userVisit, "must have a history entry for the 'approve' node")
	require.NotNil(t, userVisit.ActorID, "ActorID must be set on user-task visit")
	assert.Equal(t, manager.ID, *userVisit.ActorID)
}

// TestDeliverLoadError verifies that Deliver returns an error when the state
// store does not have a record for the given instance ID.
func TestDeliverLoadError(t *testing.T) {
	ctx := t.Context()
	r := runtime.NewRunner(nil, clock.System(), runtime.NewMemStateStore(), runtime.NewMemJournal(), runtime.NewMemOutbox(), nil, nil, nil)
	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	trg := engine.NewHumanClaimed(clock.System().Now(), "no-token", manager)
	_, err := r.Deliver(ctx, approvalDef(), "non-existent", trg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runtime: deliver: load:")
}
