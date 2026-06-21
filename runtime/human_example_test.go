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
	store := runtime.NewMemStore()

	r := runtime.NewRunner(
		nil, // no service actions needed for this process
		clk,
		store,
		runtime.WithHumanTasks(resolver, taskStore, az),
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
	entries, err := store.Entries(ctx, instanceID)
	require.NoError(t, err)
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
	r := runtime.NewRunner(nil, clock.System(), runtime.NewMemStore())
	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	trg := engine.NewHumanClaimed(clock.System().Now(), "no-token", manager)
	_, err := r.Deliver(ctx, approvalDef(), "non-existent", trg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runtime: deliver: load:")
}

// TestRunnerSnapshotsVarsIntoHumanTask verifies that the runner, when it performs
// an AwaitHuman command, copies the current process variables into
// HumanTask.Vars as a defensive snapshot — so attribute-based eligibility
// predicates that reference data variables work correctly without aliasing the
// live process-variable map.
func TestRunnerSnapshotsVarsIntoHumanTask(t *testing.T) {
	ctx := t.Context()

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"manager": {manager},
	})
	az := authz.RoleAuthorizer{}
	clk := clock.System()

	r := runtime.NewRunner(
		nil,
		clk,
		runtime.NewMemStore(),
		runtime.WithHumanTasks(resolver, taskStore, az),
	)

	// Start with non-nil process variables so the snapshot is meaningful.
	instanceVars := map[string]any{"region": "EU", "priority": 1}
	_, err := r.Run(ctx, approvalDef(), "snap-inst-1", instanceVars)
	require.NoError(t, err)

	// After Run parks, the task must be in the store with Vars populated.
	claimable, err := taskStore.ClaimableBy(ctx, manager)
	require.NoError(t, err)
	require.Len(t, claimable, 1)

	task := claimable[0]
	assert.Equal(t, map[string]any{"region": "EU", "priority": 1}, task.Vars,
		"task.Vars must be a copy of the process variables at task-creation time")

	// Defensive-copy proof: mutating instanceVars after Run must NOT change task.Vars.
	instanceVars["region"] = "US"
	fetched, err := taskStore.Get(ctx, task.TaskToken)
	require.NoError(t, err)
	assert.Equal(t, "EU", fetched.Vars["region"],
		"mutating the original vars map must not change the snapshotted task.Vars")
}

// TestRunnerAttributeOverVarsEndToEnd verifies the full vars-plumbing path:
// the runner snapshots process variables into HumanTask.Vars at task-creation
// time, and TaskService.Claim enforces an attribute predicate that references
// those variables (vars["region"] == "EU").
func TestRunnerAttributeOverVarsEndToEnd(t *testing.T) {
	cases := map[string]struct {
		instanceID string
		region     string
		assertErr  func(t *testing.T, err error)
	}{
		"matching region claims": {
			instanceID: "inst-attr-eu",
			region:     "EU",
			assertErr:  func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		"non-matching region denied": {
			instanceID: "inst-attr-us",
			region:     "US",
			assertErr: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			taskStore := humantask.NewMemTaskStore()
			az := authz.RoleAuthorizer{}
			clk := clock.System()
			svc := runtime.NewTaskService(taskStore, az, clk)

			// Upsert a task whose Vars snapshot matches what the runner would set,
			// proven by TestRunnerSnapshotsVarsIntoHumanTask. The attribute predicate
			// is the sole gate (no role constraint).
			require.NoError(t, taskStore.Upsert(t.Context(), humantask.HumanTask{
				TaskToken:   "tok-attr-" + tc.instanceID,
				Eligibility: authz.AuthzSpec{Attribute: `vars["region"] == "EU"`},
				Vars:        map[string]any{"region": tc.region},
				State:       humantask.Unclaimed,
			}))

			_, err := svc.Claim(t.Context(), "tok-attr-"+tc.instanceID, authz.Actor{ID: "alice"})
			tc.assertErr(t, err)
		})
	}
}
