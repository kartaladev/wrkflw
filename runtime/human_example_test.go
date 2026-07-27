package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
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
//  1. Drive parks at the user task.
//  2. TaskStore.ClaimableBy returns the task for a manager actor.
//  3. TaskService.Claim → ProcessDriver.ApplyTrigger(HumanClaimed) transitions the task to Claimed.
//  4. TaskService.Complete → ProcessDriver.ApplyTrigger(HumanCompleted) completes the instance.
//  5. Journal shows StartInstance + HumanClaimed + HumanCompleted.
//  6. Final task State==Completed and Claim.Actor.ID==manager actor ID.
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

	driver := runtimetest.MustProcessDriver(t, nil, store,
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

	taskID := task.TaskID

	// --- TaskService.Claim → ApplyTrigger ---
	svc := runtimetest.MustTaskService(t, taskStore, az)

	claimTrg, err := svc.Claim(ctx, taskID, manager)
	require.NoError(t, err)

	claimedState, err := driver.ApplyTrigger(ctx, def, instanceID, claimTrg)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, claimedState.Status, "instance still running after claim")

	// Verify task is Claimed in the store.
	storedTask, err := taskStore.Get(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, humantask.Claimed, storedTask.State)
	require.NotNil(t, storedTask.Claim)
	assert.Equal(t, manager.ID, storedTask.Claim.Actor.ID)

	// --- TaskService.Complete → ApplyTrigger ---
	completeTrg, err := svc.Complete(ctx, taskID, manager, engine.CompletionInput{Output: map[string]any{"approved": true}})
	require.NoError(t, err)

	finalState, err := driver.ApplyTrigger(ctx, def, instanceID, completeTrg)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, finalState.Status)
	assert.Empty(t, finalState.Tokens, "no tokens remain after completion")

	// Final task state.
	finalTask, err := taskStore.Get(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, humantask.Completed, finalTask.State)
	require.NotNil(t, finalTask.Claim)
	assert.Equal(t, manager.ID, finalTask.Claim.Actor.ID)

	// Journal: StartInstance + HumanClaimed + HumanCompleted (Run's StartInstance
	// plus two ApplyTrigger calls). Candidate resolution rides the SAME commit
	// that parks the task (ADR-0147 amendment #1), so creating a human task costs
	// no extra step, no extra commit, and no extra journal entry. A
	// HumanCandidatesResolved entry here would mean resolution had regressed to a
	// second, race-losable commit.
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

	// The user-task visit links to its human task, which carries the audit.
	var userVisit *engine.NodeVisit
	for i := range finalState.History {
		if finalState.History[i].NodeID == "approve" {
			userVisit = &finalState.History[i]
		}
	}
	require.NotNil(t, userVisit, "must have a history entry for the 'approve' node")
	require.Len(t, finalState.Tasks, 1)
	assert.Equal(t, finalState.Tasks[0].TaskID, userVisit.TaskID, "visit must link to its task")
}

// TestDeliverLoadError verifies that ApplyTrigger returns an error when the state
// store does not have a record for the given instance ID.
func TestDeliverLoadError(t *testing.T) {
	ctx := t.Context()
	driver := runtimetest.MustProcessDriver(t, nil, runtimetest.MustMemStore(t))
	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	trg := engine.NewHumanClaimed(clockwork.NewRealClock().Now(), "no-token", manager)
	_, err := driver.ApplyTrigger(ctx, runtimetest.ApprovalDef(), "non-existent", trg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workflow-runtime: deliver: load:")
}

// TestProcessDriverSnapshotsVarsIntoHumanTask verifies that the runner, when it performs
// an AwaitHuman command, copies the current process variables into
// HumanTask.Vars as a defensive snapshot — so attribute-based eligibility
// predicates that reference data variables work correctly without aliasing the
// live process-variable map.
func TestProcessDriverSnapshotsVarsIntoHumanTask(t *testing.T) {
	ctx := t.Context()

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"manager": {manager},
	})
	az := authz.RoleAuthorizer{}

	driver := runtimetest.MustProcessDriver(t, nil, runtimetest.MustMemStore(t),
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
	fetched, err := taskStore.Get(ctx, task.TaskID)
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

// TestProcessDriverAttributeOverVarsThroughRunner verifies the FULL runner→snapshot→claim
// chain: the runner snapshots process variables into HumanTask.Vars when it
// performs an AwaitHuman command, and TaskService.Claim enforces the
// EligibleExpr predicate against those snapshotted vars. The task is NOT
// pre-populated — it is created exclusively by runner.Drive so the test exercises
// the real end-to-end path.
//
// Two instances are run:
//  1. region="EU"  → Claim succeeds (predicate true).
//  2. region="US"  → Claim returns ErrNotAuthorized (predicate false).
func TestProcessDriverAttributeOverVarsThroughRunner(t *testing.T) {
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

			driver := runtimetest.MustProcessDriver(t, nil, store,
				runtime.WithHumanTasks(resolver, taskStore, az),
			)

			// Step 1: Run the process — the runner must create the HumanTask and
			// snapshot the process variables into task.Vars. No manual Upsert.
			parkedState, err := driver.Drive(ctx, def, tc.instID, map[string]any{"region": tc.region})
			require.NoError(t, err)
			require.Equal(t, engine.StatusRunning, parkedState.Status, "instance must park at the user task")
			require.Len(t, parkedState.Tokens, 1, "exactly one parked token expected")

			// The parked token's AwaitCommand is the task id (engine assigns it).
			taskID := parkedState.Tokens[0].AwaitCommand
			require.NotEmpty(t, taskID, "task id must be set on the parked token")

			// Step 2: Verify the runner populated task.Vars from the process variables
			// (not pre-upserted): the snapshotted vars must carry the region value.
			storedTask, err := taskStore.Get(ctx, taskID)
			require.NoError(t, err)
			assert.Equal(t, tc.region, storedTask.Vars["region"],
				"runner must snapshot process vars into task.Vars at task-creation time")

			// Step 3: Claim — the TaskService evaluates the EligibleExpr against
			// the snapshotted vars. Result depends on whether region matches the predicate.
			svc := runtimetest.MustTaskService(t, taskStore, az)
			_, err = svc.Claim(ctx, taskID, approver)
			tc.assertErr(t, err)
		})
	}
}

// TestHumanTaskCandidatesSurviveReload is the regression test for the defect the
// rule-#9 audit found: candidates must reach the COMMITTED snapshot, not just the
// in-memory state the current call happens to return.
//
// The instance view is a pure projection over the persisted snapshot
// (service.newInstanceJSON), so anything the runtime writes after the commit is
// invisible to every later reader. Resolution therefore happens BEFORE the
// parking commit; this test reloads the instance from the store to prove it,
// which is exactly what a post-commit write would fail.
//
// It also pins that a claim does not erase the list: every UpdateTask command
// round-trips the engine's task through the task store, so a task whose snapshot
// lacks candidates wipes them from the store on the first update.
func TestHumanTaskCandidatesSurviveReload(t *testing.T) {
	ctx := t.Context()

	manager := authz.Actor{
		ID:         "alice",
		Roles:      []string{"manager"},
		Attributes: map[string]any{"email": "alice@acme.com"},
	}
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{"manager": {manager}})
	taskStore := humantask.NewMemTaskStore()
	az := authz.RoleAuthorizer{}
	store := runtimetest.MustMemStore(t)

	driver := runtimetest.MustProcessDriver(t, nil, store,
		runtime.WithHumanTasks(resolver, taskStore, az),
	)
	def := runtimetest.ApprovalDef()
	const instanceID = "inst-reload-1"

	_, err := driver.Drive(ctx, def, instanceID, nil)
	require.NoError(t, err)

	// Reload from the store — the committed snapshot, not the returned value.
	reloaded, _, err := store.Load(ctx, instanceID)
	require.NoError(t, err)
	require.Len(t, reloaded.Tasks, 1)
	require.Equal(t, []authz.Actor{manager}, reloaded.Tasks[0].Candidates,
		"the committed snapshot must carry the resolved actors verbatim")

	// A claim must not erase them, in the snapshot or in the task store.
	taskID := reloaded.Tasks[0].TaskID
	svc := runtimetest.MustTaskService(t, taskStore, az)
	claimTrg, err := svc.Claim(ctx, taskID, manager)
	require.NoError(t, err)
	_, err = driver.ApplyTrigger(ctx, def, instanceID, claimTrg)
	require.NoError(t, err)

	afterClaim, _, err := store.Load(ctx, instanceID)
	require.NoError(t, err)
	require.Equal(t, []authz.Actor{manager}, afterClaim.Tasks[0].Candidates,
		"a claim must not erase the candidate list from the snapshot")

	storedTask, err := taskStore.Get(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, []authz.Actor{manager}, storedTask.Candidates,
		"a claim must not erase the candidate list from the task store")
}

// blockingResolver blocks until its context is cancelled, then reports why. It
// models a directory service that has stopped responding.
type blockingResolver struct{ entered chan struct{} }

func (r *blockingResolver) Candidates(ctx context.Context, _ authz.AuthzSpec, _ map[string]any) ([]authz.Actor, error) {
	select {
	case r.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestCandidateResolveTimeout verifies that a single candidate resolution is
// bounded. Resolution runs BEFORE the parking commit (ADR-0147 amendment #1), so
// an unresponsive ActorResolver would otherwise hold the step open indefinitely
// and stall the commit — the driver must not depend on a third-party directory
// being well-behaved.
//
// The bound is a sensible default with an explicit opt-out, mirroring
// WithActionTimeout.
func TestCandidateResolveTimeout(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		opts   []runtime.Option
		assert func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name: "a hung resolver is bounded by the default timeout",
			opts: []runtime.Option{runtime.WithCandidateResolveTimeout(50 * time.Millisecond)},
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.Contains(t, err.Error(), "resolve candidates")
			},
		},
		{
			name: "a non-positive timeout disables the bound",
			opts: []runtime.Option{runtime.WithCandidateResolveTimeout(0)},
			assert: func(t *testing.T, err error) {
				// With no deadline the resolver blocks on ctx.Done, which only fires
				// when the test's own context is cancelled — so the drive must not
				// have returned a deadline error of its own.
				require.NotErrorIs(t, err, context.DeadlineExceeded)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			resolver := &blockingResolver{entered: make(chan struct{}, 1)}
			opts := append([]runtime.Option{
				runtime.WithHumanTasks(resolver, humantask.NewMemTaskStore(), authz.RoleAuthorizer{}),
			}, tc.opts...)
			driver := runtimetest.MustProcessDriver(t, nil, runtimetest.MustMemStore(t), opts...)

			done := make(chan error, 1)
			go func() {
				_, err := driver.Drive(ctx, runtimetest.ApprovalDef(), "resolve-timeout-1", nil)
				done <- err
			}()

			select {
			case err := <-done:
				tc.assert(t, err)
			case <-time.After(2 * time.Second):
				cancel()
				tc.assert(t, <-done)
			}
		})
	}
}
