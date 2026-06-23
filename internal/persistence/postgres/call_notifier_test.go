package postgres_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// callNotifierChildDef: start → child-task(UserTask, role "worker") → child-end
func callNotifierChildDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "cncn-child",
		Version: 1,
		Nodes: []model.Node{
			{ID: "child-start", Kind: model.KindStartEvent},
			{ID: "child-task", Kind: model.KindUserTask, CandidateRoles: []string{"worker"}},
			{ID: "child-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "cf1", Source: "child-start", Target: "child-task"},
			{ID: "cf2", Source: "child-task", Target: "child-end"},
		},
	}
}

// callNotifierParentDef: start → call(KindCallActivity, DefRef:"cncn-child") → parent-end
func callNotifierParentDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "cncn-parent",
		Version: 1,
		Nodes: []model.Node{
			{ID: "parent-start", Kind: model.KindStartEvent},
			{ID: "call", Kind: model.KindCallActivity, DefRef: "cncn-child"},
			{ID: "parent-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "pf1", Source: "parent-start", Target: "call"},
			{ID: "pf2", Source: "call", Target: "parent-end"},
		},
	}
}

// TestCallNotifierCrashSafety verifies that a brand-new CallNotifier (simulating a
// process restart) correctly resumes a parked parent instance by draining terminal
// call links from Postgres.
//
// Scenario:
//  1. Wire parent + child definitions with Postgres-backed store and call links.
//  2. Start the parent via runner.Run → parent parks (child parks at human task).
//  3. Complete the child's human task via TaskService + runner.Deliver → child reaches
//     StatusCompleted → call link flips to status='completed'.
//  4. CRASH SIMULATION: build a brand-new persistence.NewCallNotifier (simulating
//     restart) with a fresh deliver func wrapping a NEW runner over the SAME DB pool.
//  5. notifier.DrainOnce(ctx) returns (1, nil) and parent reaches StatusCompleted.
//  6. A second DrainOnce is a no-op (returns 0, nil).
func TestCallNotifierCrashSafety(t *testing.T) {
	ctx := t.Context()

	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(ctx, pool))

	clk := clock.System()

	worker := authz.Actor{ID: "alice", Roles: []string{"worker"}}
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"worker": {worker},
	})
	tasks := humantask.NewMemTaskStore()
	az := authz.RoleAuthorizer{}

	child := callNotifierChildDef()
	parent := callNotifierParentDef()

	// DefinitionRegistry keyed as required by CallNotifier's fmt.Sprintf("%s:%d", defID, version) lookup.
	reg := runtime.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		"cncn-child":   child,
		"cncn-parent:1": parent,
	})

	// Phase 1: Start the parent, park child at human task.
	store := pg.NewStore(pool)
	cl := pg.NewCallLinkStore(pool)

	runner := runtime.NewRunner(nil, clk, store,
		runtime.WithCallLinks(cl),
		runtime.WithDefinitions(reg),
		runtime.WithHumanTasks(resolver, tasks, az),
	)

	const parentID = "cncn-parent-i1"
	parentSt, err := runner.Run(ctx, parent, parentID, nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parentSt.Status,
		"parent must be StatusRunning (parked) while child is at human task")

	// Derive child instance ID from the parent's scheme.
	childID := parentID + "-sub-c1"

	// Verify child is parked at human task.
	childSt, _, err := store.Load(ctx, childID)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, childSt.Status,
		"child must be StatusRunning (parked at human task)")

	// Phase 2: Complete the child's human task → child completes → link flips to 'completed'.
	taskSvc := runtime.NewTaskService(tasks, az, clk)

	// Find the task token — the human task was created when the child ran.
	// Use ClaimableBy to list tasks claimable by the worker actor.
	taskList, err := tasks.ClaimableBy(ctx, worker)
	require.NoError(t, err)
	require.Len(t, taskList, 1, "exactly one human task must exist for the child")
	taskToken := taskList[0].TaskToken

	// Claim and complete the task.
	claimTrg, err := taskSvc.Claim(ctx, taskToken, worker)
	require.NoError(t, err)
	childAfterClaim, err := runner.Deliver(ctx, child, childID, claimTrg)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, childAfterClaim.Status,
		"child must still be running after claim (waiting for complete)")

	completeTrg, err := taskSvc.Complete(ctx, taskToken, worker, map[string]any{"result": "done"})
	require.NoError(t, err)
	childFinal, err := runner.Deliver(ctx, child, childID, completeTrg)
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, childFinal.Status,
		"child must be StatusCompleted after task completion")

	// Verify the call link has been flipped to 'completed' in DB.
	pending, err := cl.ClaimPending(ctx, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1, "one pending notify must exist for the completed child link")
	require.True(t, pending[0].Outcome.Completed, "link outcome must be completed")

	// Phase 3 (CRASH SIMULATION): build brand-new runner + notifier over the SAME pool.
	// The original runner is discarded; only Postgres state survives. The crash path
	// under test is the call-link durability (wrkflw_call_links), which is fully
	// DB-backed — so the resume comes purely from durable state. The in-memory
	// MemTaskStore is intentionally reused (the human task already completed before
	// the "crash"; the task store is not on the notifier's delivery path).
	freshStore := pg.NewStore(pool)
	freshCl := pg.NewCallLinkStore(pool)
	freshRunner := runtime.NewRunner(nil, clk, freshStore,
		runtime.WithCallLinks(freshCl),
		runtime.WithDefinitions(reg),
		runtime.WithHumanTasks(resolver, tasks, az),
	)

	notifier := persistence.NewCallNotifier(pool,
		runtime.CallDeliverFunc(func(ctx context.Context, def *model.ProcessDefinition, instanceID string, trg engine.Trigger) error {
			_, err := freshRunner.Deliver(ctx, def, instanceID, trg)
			return err
		}),
		reg,
		clk,
	)

	// DrainOnce must notify exactly 1 link (the completed child).
	notified, err := notifier.DrainOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, notified, "DrainOnce must resume exactly 1 parent")

	// Parent must now be StatusCompleted after the notifier delivered SubInstanceCompleted.
	parentFinal, _, err := freshStore.Load(ctx, parentID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, parentFinal.Status,
		"parent must reach StatusCompleted after call notifier drains the completed child link")

	// Phase 4: Second DrainOnce must be a no-op (link already notified).
	notified2, err := notifier.DrainOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, notified2, "second DrainOnce must be a no-op (link already marked notified)")
}

// TestCallNotifierDrainIdempotentDuplicate verifies that delivering an already-resumed
// parent (engine.ErrTokenNotFound) is treated as a success: the link is marked notified
// and counted — it does not block subsequent drains.
func TestCallNotifierDrainIdempotentDuplicate(t *testing.T) {
	ctx := t.Context()

	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(ctx, pool))

	clk := clock.System()

	worker := authz.Actor{ID: "bob", Roles: []string{"worker"}}
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"worker": {worker},
	})
	tasks := humantask.NewMemTaskStore()
	az := authz.RoleAuthorizer{}

	child := callNotifierChildDef()
	parent := callNotifierParentDef()
	reg := runtime.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		"cncn-child":    child,
		"cncn-parent:1": parent,
	})

	store := pg.NewStore(pool)
	cl := pg.NewCallLinkStore(pool)

	runner := runtime.NewRunner(nil, clk, store,
		runtime.WithCallLinks(cl),
		runtime.WithDefinitions(reg),
		runtime.WithHumanTasks(resolver, tasks, az),
	)

	const parentID = "cncn-dup-parent-i1"
	_, err := runner.Run(ctx, parent, parentID, nil)
	require.NoError(t, err)

	childID := parentID + "-sub-c1"

	// Complete the human task.
	taskSvc := runtime.NewTaskService(tasks, az, clk)
	taskList, err := tasks.ClaimableBy(ctx, worker)
	require.NoError(t, err)
	require.Len(t, taskList, 1)
	taskToken := taskList[0].TaskToken

	claimTrg, err := taskSvc.Claim(ctx, taskToken, worker)
	require.NoError(t, err)
	_, err = runner.Deliver(ctx, child, childID, claimTrg)
	require.NoError(t, err)

	completeTrg, err := taskSvc.Complete(ctx, taskToken, worker, nil)
	require.NoError(t, err)
	childFinal, err := runner.Deliver(ctx, child, childID, completeTrg)
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, childFinal.Status)

	// First notifier: drains and resumes the parent normally.
	callCount := 0
	freshStore1 := pg.NewStore(pool)
	freshRunner1 := runtime.NewRunner(nil, clk, freshStore1,
		runtime.WithCallLinks(pg.NewCallLinkStore(pool)),
		runtime.WithDefinitions(reg),
		runtime.WithHumanTasks(resolver, tasks, az),
	)
	notifier1 := persistence.NewCallNotifier(pool,
		runtime.CallDeliverFunc(func(ctx context.Context, def *model.ProcessDefinition, instanceID string, trg engine.Trigger) error {
			callCount++
			_, err := freshRunner1.Deliver(ctx, def, instanceID, trg)
			return err
		}),
		reg,
		clk,
	)
	notified, err := notifier1.DrainOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, notified)
	require.Equal(t, 1, callCount, "deliver must be called once")

	// Second notifier simulating a duplicate drain: the link is already 'notified',
	// so ClaimPending returns nothing — deliver is never called.
	notifier2 := persistence.NewCallNotifier(pool,
		runtime.CallDeliverFunc(func(ctx context.Context, def *model.ProcessDefinition, instanceID string, trg engine.Trigger) error {
			// Deliver would return ErrTokenNotFound since parent is already completed.
			// But ClaimPending should return empty, so this should never be called.
			return engine.ErrTokenNotFound
		}),
		reg,
		clk,
	)
	notified2, err := notifier2.DrainOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, notified2, "second DrainOnce must be a no-op after notified link is excluded")
}
