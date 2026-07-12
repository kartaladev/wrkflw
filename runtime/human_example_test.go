package runtime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/clock"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
)

// TestHumanTaskEndToEnd tests the full human-task lifecycle:
//
//  1. Run parks at the user task.
//  2. TaskStore.ClaimableBy returns the task for a manager actor.
//  3. TaskService.Claim → Runner.ApplyTrigger(HumanClaimed) transitions the task to Claimed.
//  4. TaskService.Complete → Runner.ApplyTrigger(HumanCompleted) completes the instance.
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
	store := runtimetest.MustMemStore(t)

	driver := runtimetest.MustRunner(t, nil, store,
		runtime.WithHumanTasks(resolver, taskStore, az),
	)

	def := runtimetest.ApprovalDef()
	const instanceID = "inst-1"

	// --- Run: parks at the user task ---
	parkedState, err := driver.Drive(ctx, def, instanceID, nil)
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

	// --- TaskService.Claim → ApplyTrigger ---
	svc := runtimetest.MustTaskService(t, taskStore, az)

	claimTrg, err := svc.Claim(ctx, taskToken, manager)
	require.NoError(t, err)

	claimedState, err := driver.ApplyTrigger(ctx, def, instanceID, claimTrg)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, claimedState.Status, "instance still running after claim")

	// Verify task is Claimed in the store.
	storedTask, err := taskStore.Get(ctx, taskToken)
	require.NoError(t, err)
	assert.Equal(t, humantask.Claimed, storedTask.State)
	assert.Equal(t, manager.ID, storedTask.ClaimedBy)

	// --- TaskService.Complete → ApplyTrigger ---
	completeTrg, err := svc.Complete(ctx, taskToken, manager, map[string]any{"approved": true})
	require.NoError(t, err)

	finalState, err := driver.ApplyTrigger(ctx, def, instanceID, completeTrg)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, finalState.Status)
	assert.Empty(t, finalState.Tokens, "no tokens remain after completion")

	// Final task state.
	finalTask, err := taskStore.Get(ctx, taskToken)
	require.NoError(t, err)
	assert.Equal(t, humantask.Completed, finalTask.State)
	assert.Equal(t, manager.ID, finalTask.ClaimedBy)

	// Journal: StartInstance + HumanClaimed + HumanCompleted (Run's StartInstance
	// plus two ApplyTrigger calls).
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

// TestDeliverLoadError verifies that ApplyTrigger returns an error when the state
// store does not have a record for the given instance ID.
func TestDeliverLoadError(t *testing.T) {
	ctx := t.Context()
	driver := runtimetest.MustRunner(t, nil, runtimetest.MustMemStore(t))
	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	trg := engine.NewHumanClaimed(clock.System().Now(), "no-token", manager)
	_, err := driver.ApplyTrigger(ctx, runtimetest.ApprovalDef(), "non-existent", trg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workflow-runtime: deliver: load:")
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

	driver := runtimetest.MustRunner(t, nil, runtimetest.MustMemStore(t),
		runtime.WithHumanTasks(resolver, taskStore, az),
	)

	// Start with non-nil process variables so the snapshot is meaningful.
	instanceVars := map[string]any{"region": "EU", "priority": 1}
	_, err := driver.Drive(ctx, runtimetest.ApprovalDef(), "snap-inst-1", instanceVars)
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

// approvalWithEligibleExprDef returns a process: start → userTask("approve",
// role "approver", EligibleExpr vars["region"] == "EU") → end.
// The EligibleExpr is mapped to AuthzSpec.Attribute by the engine so that
// attribute-based authorization is enforced at Claim time over snapshotted vars.
func approvalWithEligibleExprDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "approval-with-attr",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("approve", activity.WithEligibleRoles("approver"), activity.WithEligibleExpr(`vars["region"] == "EU"`)),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}
}

// TestRunnerAttributeOverVarsThroughRunner verifies the FULL runner→snapshot→claim
// chain: the runner snapshots process variables into HumanTask.Vars when it
// performs an AwaitHuman command, and TaskService.Claim enforces the
// EligibleExpr predicate against those snapshotted vars. The task is NOT
// pre-populated — it is created exclusively by runner.Drive so the test exercises
// the real end-to-end path.
//
// Two instances are run:
//  1. region="EU"  → Claim succeeds (predicate true).
//  2. region="US"  → Claim returns ErrNotAuthorized (predicate false).
func TestRunnerAttributeOverVarsThroughRunner(t *testing.T) {
	approver := authz.Actor{ID: "alice", Roles: []string{"approver"}}
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"approver": {approver},
	})
	def := approvalWithEligibleExprDef()

	cases := []struct {
		name      string
		instID    string
		region    string
		assertErr func(t *testing.T, err error)
	}{
		{
			name:      "matching region claims",
			instID:    "inst-attr-through-runner-eu",
			region:    "EU",
			assertErr: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		{
			name:   "non-matching region denied",
			instID: "inst-attr-through-runner-us",
			region: "US",
			assertErr: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()

			// Each sub-test gets its own isolated stores so they do not share state.
			taskStore := humantask.NewMemTaskStore()
			az := authz.RoleAuthorizer{}
			store := runtimetest.MustMemStore(t)

			driver := runtimetest.MustRunner(t, nil, store,
				runtime.WithHumanTasks(resolver, taskStore, az),
			)

			// Step 1: Run the process — the runner must create the HumanTask and
			// snapshot the process variables into task.Vars. No manual Upsert.
			parkedState, err := driver.Drive(ctx, def, tc.instID, map[string]any{"region": tc.region})
			require.NoError(t, err)
			require.Equal(t, engine.StatusRunning, parkedState.Status, "instance must park at the user task")
			require.Len(t, parkedState.Tokens, 1, "exactly one parked token expected")

			// The parked token's AwaitCommand is the task token (engine assigns it).
			taskToken := parkedState.Tokens[0].AwaitCommand
			require.NotEmpty(t, taskToken, "task token must be set on the parked token")

			// Step 2: Verify the runner populated task.Vars from the process variables
			// (not pre-upserted): the snapshotted vars must carry the region value.
			storedTask, err := taskStore.Get(ctx, taskToken)
			require.NoError(t, err)
			assert.Equal(t, tc.region, storedTask.Vars["region"],
				"runner must snapshot process vars into task.Vars at task-creation time")

			// Step 3: Claim — the TaskService evaluates the EligibleExpr against
			// the snapshotted vars. Result depends on whether region matches the predicate.
			svc := runtimetest.MustTaskService(t, taskStore, az)
			_, err = svc.Claim(ctx, taskToken, approver)
			tc.assertErr(t, err)
		})
	}
}
