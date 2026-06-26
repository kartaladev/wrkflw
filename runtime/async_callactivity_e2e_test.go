package runtime_test

// async_callactivity_e2e_test.go — cross-cutting e2e scenarios for the async
// call-activity track (Task 8). All scenarios run on the in-memory path:
// MemStore + MemCallLinkStore + runtime.CallNotifier.
//
// Scenarios:
//  1. Nested async (parent → child → grandchild): cascade resume via DrainOnce
//     until parent reaches StatusCompleted; depth increments per level.
//  2. Failure path: child that errors → parent receives SubInstanceFailed →
//     parent reaches StatusFailed; Err non-empty.
//  3. Runaway guard: self-calling definition is bounded at maxCallDepth (64);
//     chain terminates via SubInstanceFailed, not infinitely.
//  4. Opt-out preserved: runner WITHOUT WithCallLinks whose child parks returns
//     the synchronous "does not support parked children" error.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// ── scenario 1: nested async ─────────────────────────────────────────────────

// e2eGrandchildDef is the leaf: parks at a human task.
//
//	gc-start → gc-task (KindUserTask, role "worker") → gc-end
func e2eGrandchildDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "e2e-grandchild",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("gc-start"),
			model.NewUserTask("gc-task", []string{"worker"}),
			model.NewEndEvent("gc-end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "gcf1", Source: "gc-start", Target: "gc-task"},
			{ID: "gcf2", Source: "gc-task", Target: "gc-end"},
		},
	}
}

// e2eChildDef calls the grandchild via a call activity.
//
//	c-start → c-call (KindCallActivity, DefRef:"e2e-grandchild") → c-end
func e2eChildDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "e2e-child",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("c-start"),
			model.NewCallActivity("c-call", "e2e-grandchild"),
			model.NewEndEvent("c-end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "cf1", Source: "c-start", Target: "c-call"},
			{ID: "cf2", Source: "c-call", Target: "c-end"},
		},
	}
}

// e2eParentDef calls the child via a call activity.
//
//	p-start → p-call (KindCallActivity, DefRef:"e2e-child") → p-end
func e2eParentDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "e2e-parent",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("p-start"),
			model.NewCallActivity("p-call", "e2e-child"),
			model.NewEndEvent("p-end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "pf1", Source: "p-start", Target: "p-call"},
			{ID: "pf2", Source: "p-call", Target: "p-end"},
		},
	}
}

// TestNestedAsyncCallActivity verifies that a three-level async call chain
// (parent → child → grandchild) correctly cascades resume all the way up
// to the parent after the leaf grandchild's human task is completed.
//
// Assertions:
//   - grandchild depth = 2, child depth = 1 (depth increments per level).
//   - completing grandchild's task + two DrainOnce calls → parent StatusCompleted.
func TestNestedAsyncCallActivity(t *testing.T) {
	ctx := t.Context()

	// ── wiring ───────────────────────────────────────────────────────────────
	clk := clock.System()
	cl := runtime.NewMemCallLinkStore()
	store := runtime.NewMemStoreWithCallLinks(cl)

	gcDef := e2eGrandchildDef()
	cDef := e2eChildDef()
	pDef := e2eParentDef()

	worker := authz.Actor{ID: "alice", Roles: []string{"worker"}}
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"worker": {worker},
	})
	tasks := humantask.NewMemTaskStore()
	az := authz.RoleAuthorizer{}

	// Registry: definitions are looked up by "defID:version" format for the notifier,
	// and by plain "defID" for runtime call-activity DefRef resolution.
	reg := runtime.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		"e2e-grandchild":   gcDef,
		"e2e-child":        cDef,
		"e2e-parent":       pDef,
		"e2e-grandchild:1": gcDef,
		"e2e-child:1":      cDef,
		"e2e-parent:1":     pDef,
	})

	runner := runtime.NewRunner(nil, clk, store,
		runtime.WithCallLinks(cl),
		runtime.WithDefinitions(reg),
		runtime.WithHumanTasks(resolver, tasks, az),
	)

	deliverFn := runtime.CallDeliverFunc(func(ctx2 context.Context, def *model.ProcessDefinition, instanceID string, trg engine.Trigger) error {
		_, err := runner.Deliver(ctx2, def, instanceID, trg)
		return err
	})
	notifier := runtime.NewCallNotifier(cl, deliverFn, reg)

	// ── step 1: run parent; parks because grandchild parks at human task ─────
	const parentID = "e2e-nested-parent-i1"
	st, err := runner.Run(ctx, pDef, parentID, nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, st.Status, "parent must be StatusRunning (parked at call activity)")

	// Derive child and grandchild instance IDs using the "<parentID>-sub-c1" scheme.
	childID := parentID + "-sub-c1"
	grandchildID := childID + "-sub-c1"

	// ── step 2: verify depths ────────────────────────────────────────────────
	childLink, ok, err := cl.LookupChild(ctx, childID)
	require.NoError(t, err)
	require.True(t, ok, "child call link must exist")
	assert.Equal(t, 1, childLink.Depth, "first-level child must have depth 1")
	assert.Equal(t, parentID, childLink.ParentInstanceID)

	gcLink, ok, err := cl.LookupChild(ctx, grandchildID)
	require.NoError(t, err)
	require.True(t, ok, "grandchild call link must exist")
	assert.Equal(t, 2, gcLink.Depth, "grandchild must have depth 2")
	assert.Equal(t, childID, gcLink.ParentInstanceID)

	// ── step 3: verify grandchild is parked at the human task ────────────────
	gcSt, _, err := store.Load(ctx, grandchildID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, gcSt.Status, "grandchild must be StatusRunning at human task")

	// ── step 4: complete the grandchild's human task ──────────────────────────
	claimable, err := tasks.ClaimableBy(ctx, worker)
	require.NoError(t, err)
	require.Len(t, claimable, 1, "exactly one human task must be pending (grandchild's task)")
	taskToken := claimable[0].TaskToken

	svc := runtime.NewTaskService(tasks, az, clk)
	completeTrg, err := svc.Complete(ctx, taskToken, worker, map[string]any{"gcResult": "done"})
	require.NoError(t, err)

	gcFinalSt, err := runner.Deliver(ctx, gcDef, grandchildID, completeTrg)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, gcFinalSt.Status, "grandchild must be StatusCompleted after human task completion")

	// ── step 5: first DrainOnce — resumes child (grandchild completed) ────────
	notified, err := notifier.DrainOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, notified, "first DrainOnce must report 1 (grandchild → child)")

	// Child must now be StatusCompleted (no other parks after the call activity).
	childSt, _, err := store.Load(ctx, childID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, childSt.Status, "child must be StatusCompleted after grandchild completed")

	// ── step 6: second DrainOnce — resumes parent (child completed) ──────────
	notified2, err := notifier.DrainOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, notified2, "second DrainOnce must report 1 (child → parent)")

	// Parent must now be StatusCompleted.
	parentFinalSt, _, err := store.Load(ctx, parentID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, parentFinalSt.Status,
		"parent must reach StatusCompleted after cascade resume through both DrainOnce calls")

	// Third DrainOnce is a no-op.
	notified3, err := notifier.DrainOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, notified3, "third DrainOnce must be a no-op (all links notified)")
}

// ── scenario 2: failure path ─────────────────────────────────────────────────

// TestFailurePathCallActivity verifies that when a child process FAILS:
//   - The child's call link is flipped to terminal with Completed=false.
//   - DrainOnce delivers SubInstanceFailed to the parent.
//   - The parent reaches StatusFailed (grounded in engine/step.go SubInstanceFailed
//     handling: sets StatusFailed + emits FailInstance + clears all tokens/timers).
//   - The delivered Err text is non-empty.
//
// This scenario reuses asyncFailingChildDef / asyncFailingParentDef fixtures
// from async_callactivity_test.go.
func TestFailurePathCallActivity(t *testing.T) {
	ctx := t.Context()

	clk := clock.System()
	cl := runtime.NewMemCallLinkStore()
	store := runtime.NewMemStoreWithCallLinks(cl)

	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"fail-action": &failAction{msg: "e2e child service error"},
	})

	child := asyncFailingChildDef()
	parent := asyncFailingParentDef()

	// Register under both plain "defID" (for call activity resolution) and
	// "defID:version" (for notifier parent-def lookup).
	reg := runtime.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		"async-fail-child":    child,
		"async-fail-parent":   parent,
		"async-fail-parent:1": parent,
	})

	runner := runtime.NewRunner(cat, clk, store,
		runtime.WithCallLinks(cl),
		runtime.WithDefinitions(reg),
	)

	deliverFn := runtime.CallDeliverFunc(func(ctx2 context.Context, def *model.ProcessDefinition, instanceID string, trg engine.Trigger) error {
		_, err := runner.Deliver(ctx2, def, instanceID, trg)
		return err
	})
	notifier := runtime.NewCallNotifier(cl, deliverFn, reg)

	// ── step 1: run parent; child fails immediately during its first burst ────
	const parentID = "e2e-fail-parent-i1"
	st, err := runner.Run(ctx, parent, parentID, nil)
	require.NoError(t, err, "runner.Run must not return a hard error")
	// The parent parks at the call node because the async path returns nil,nil.
	// The child's first burst causes it to fail, flipping its link to terminal.
	assert.Equal(t, engine.StatusRunning, st.Status, "parent must be StatusRunning (async path parks parent regardless of child outcome)")

	// ── step 2: verify child link is terminal with failure ───────────────────
	childID := parentID + "-sub-c1"
	pending, err := cl.ClaimPending(ctx, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1, "exactly one terminal link expected (the failed child)")

	n := pending[0]
	assert.Equal(t, childID, n.Link.ChildInstanceID)
	assert.False(t, n.Outcome.Completed, "Outcome.Completed must be false for a failed child")
	assert.NotEmpty(t, n.Outcome.Err, "Outcome.Err must be non-empty")

	// ── step 3: DrainOnce delivers SubInstanceFailed → parent fails ──────────
	// Engine behavior (engine/step.go case SubInstanceFailed): finds the parked
	// token by CommandID, sets StatusFailed, emits FailInstance, cancels all
	// timers/arms. So parent must reach StatusFailed after DrainOnce.
	notified, err := notifier.DrainOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, notified, "DrainOnce must report 1 notified link")

	// ── step 4: parent must now be StatusFailed ───────────────────────────────
	parentFinalSt, _, err := store.Load(ctx, parentID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusFailed, parentFinalSt.Status,
		"parent must be StatusFailed after child failure is delivered (SubInstanceFailed → engine sets StatusFailed)")
}

// ── scenario 3: runaway guard ─────────────────────────────────────────────────

// selfCallDef returns a definition that calls itself via a call activity —
// a self-referencing cycle that would loop forever without the depth guard.
//
//	self-start → self-call (KindCallActivity, DefRef:"self-call") → self-end
func selfCallDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "self-call",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("self-start"),
			model.NewCallActivity("self-call", "self-call"),
			model.NewEndEvent("self-end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "sf1", Source: "self-start", Target: "self-call"},
			{ID: "sf2", Source: "self-call", Target: "self-end"},
		},
	}
}

// TestRunawayGuardCallActivity verifies that a self-referencing call activity
// is bounded by the maxCallDepth guard (64) and does NOT spawn unlimited children.
//
// The async path computes depth by looking up the calling instance's own link.
// When depth exceeds maxCallDepth (64), perform returns SubInstanceFailed immediately
// rather than calling runChild, so the chain terminates at a finite depth.
//
// Assertions:
//   - After runner.Run, the root is StatusRunning and the total number of call
//     links is exactly maxCallDepth (one per child; the (maxCallDepth+1)th child
//     is never created because the guard returns SubInstanceFailed synchronously).
//   - After draining the notifier maxCallDepth times, the root reaches StatusFailed
//     (cascade of failures from the deepest child back up to the root).
func TestRunawayGuardCallActivity(t *testing.T) {
	ctx := t.Context()

	clk := clock.System()
	cl := runtime.NewMemCallLinkStore()
	store := runtime.NewMemStoreWithCallLinks(cl)

	def := selfCallDef()

	// The registry must answer for:
	// - "self-call"   (call activity DefRef resolution during child spawning)
	// - "self-call:1" (parent def lookup by CallNotifier)
	reg := runtime.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		"self-call":   def,
		"self-call:1": def,
	})

	runner := runtime.NewRunner(nil, clk, store,
		runtime.WithCallLinks(cl),
		runtime.WithDefinitions(reg),
	)

	deliverFn := runtime.CallDeliverFunc(func(ctx2 context.Context, def2 *model.ProcessDefinition, instanceID string, trg engine.Trigger) error {
		_, err := runner.Deliver(ctx2, def2, instanceID, trg)
		return err
	})
	notifier := runtime.NewCallNotifier(cl, deliverFn, reg)

	// ── step 1: run the self-calling root ─────────────────────────────────────
	// During Run, each child synchronously spawns its own child (via runChild
	// during the parent's deliverLoop → perform). The chain terminates when
	// depth > maxCallDepth (64), at which point perform returns SubInstanceFailed
	// without calling runChild. The deepest real child gets SubInstanceFailed and
	// reaches StatusFailed during its first burst.
	const rootID = "e2e-self-root-i1"
	st, err := runner.Run(ctx, def, rootID, nil)
	require.NoError(t, err, "runner.Run must not return a hard error even for self-calling definition")

	// Root is StatusRunning (parked at its call activity).
	assert.Equal(t, engine.StatusRunning, st.Status,
		"root must be StatusRunning (parked at call activity node)")

	// ── step 2: verify call-link count is bounded ─────────────────────────────
	// The guard fires at depth > 64, so exactly 64 children are created
	// (depth 1 through 64). The deepest child (depth 64) fires SubInstanceFailed
	// synchronously within its perform; it reaches StatusFailed during runChild.
	const maxCallDepth = 64 // mirrors runtime.maxCallDepth (unexported)
	allPending, err := cl.ClaimPending(ctx, maxCallDepth+10)
	require.NoError(t, err)
	// Exactly one link should be terminal at this point: the deepest child (depth 64)
	// whose perform returned SubInstanceFailed, making it StatusFailed.
	assert.Len(t, allPending, 1, "exactly one terminal link exists after Run: the depth-limited child")
	assert.NotEmpty(t, allPending[0].Outcome.Err, "depth-limited child's link must carry a non-empty error")
	// The Outcome.Err is populated by terminalErr(st) which returns "instance failed"
	// (the generic fallback) because SubInstanceFailed does not create an Incident.
	// The depth-limit message is carried in the trigger itself but is not preserved
	// in the call-link outcome. We assert the child failed (non-empty Err) which is
	// the observable behavior.
	assert.False(t, allPending[0].Outcome.Completed, "depth-limited child's link outcome must not be Completed")

	// Count total call links by checking all depths: links are created for
	// depths 1 through 64. Verify the count is bounded at maxCallDepth.
	// We do this by counting all links via LookupChild for derived IDs.
	totalLinks := countCallLinks(ctx, t, cl, rootID, maxCallDepth+5)
	assert.Equal(t, maxCallDepth, totalLinks,
		"total call links must be exactly maxCallDepth (chain is bounded, not infinite)")

	// ── step 3: drain failures cascade back to root ──────────────────────────
	// Each DrainOnce delivers one SubInstanceFailed up the chain:
	// child64 fails → drain1 fails child63 → drain2 fails child62 → ... →
	// drain64 fails root.
	// We mark the already-claimed pending links as notified manually so DrainOnce
	// can process them naturally in subsequent rounds, OR we drain maxCallDepth
	// times and verify the root ends up failed.
	//
	// Note: ClaimPending above already claimed the deepest-child link. MarkNotified
	// is called by DrainOnce internally. Since ClaimPending with limit=10 above
	// consumed the deepest link WITHOUT calling MarkNotified, we need to perform
	// the drain fresh. Recreate by calling DrainOnce starting from a fresh notifier
	// state — but we've already claimed the link. To avoid test contamination we
	// call MarkNotified on what we claimed, then proceed.
	//
	// Actually: MemCallLinkStore.ClaimPending does NOT mark as notified; it just
	// returns pending. The link is still claimable until MarkNotified is called.
	// So DrainOnce will re-claim it and call MarkNotified properly.

	// Drain up to maxCallDepth+1 times; each drain should cascade one level.
	totalNotified := 0
	for range maxCallDepth + 1 {
		n, drainErr := notifier.DrainOnce(ctx)
		require.NoError(t, drainErr)
		totalNotified += n
		if n == 0 {
			break // no more pending links
		}
	}

	// Root must now be StatusFailed (cascade of SubInstanceFailed all the way up).
	rootFinalSt, _, loadErr := store.Load(ctx, rootID)
	require.NoError(t, loadErr)
	assert.Equal(t, engine.StatusFailed, rootFinalSt.Status,
		"root must reach StatusFailed after depth-limit failure cascades up the chain")
	assert.Equal(t, maxCallDepth, totalNotified,
		"total notified links must equal maxCallDepth (one per level in the cascade)")
}

// countCallLinks counts how many call links exist for the chain rooted at rootID
// by walking the derived child IDs. It checks at most maxCheck depth levels.
// Each child ID follows the scheme: "<parentID>-sub-c1".
func countCallLinks(ctx context.Context, t *testing.T, cl *runtime.MemCallLinkStore, rootID string, maxCheck int) int {
	t.Helper()
	count := 0
	currentID := rootID
	for range maxCheck {
		childID := currentID + "-sub-c1"
		_, ok, err := cl.LookupChild(ctx, childID)
		if err != nil || !ok {
			break
		}
		count++
		currentID = childID
	}
	return count
}

// ── scenario 4: opt-out preserved ────────────────────────────────────────────

// TestOptOutCallActivityPreservesError verifies that a runner configured WITHOUT
// WithCallLinks (the opt-out / synchronous path) returns a descriptive error
// when its child parks at a human task, rather than succeeding silently.
//
// The synchronous path (engine/step.go → runner.go perform StartSubInstance
// synchronous branch) translates a child StatusRunning into SubInstanceFailed
// with the message "the synchronous runner does not support children that wait
// on human tasks, timers, or events". The parent then receives SubInstanceFailed
// and reaches StatusFailed.
//
// This test asserts behavior-preserving opt-out: the default (no WithCallLinks)
// is unchanged by the async track.
func TestOptOutCallActivityPreservesError(t *testing.T) {
	ctx := t.Context()

	clk := clock.System()
	// Standard MemStore — NO call-link tracking.
	store := runtime.NewMemStore()

	// Child that parks at a human task; parent calls it via a call activity.
	child := asyncChildDef()
	parent := asyncParentDef()

	reg := runtime.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		"async-child": child,
	})

	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()

	// Runner built WITHOUT WithCallLinks → synchronous call-activity path.
	runner := runtime.NewRunner(nil, clk, store,
		runtime.WithDefinitions(reg),
		runtime.WithHumanTasks(resolver, tasks, nil),
	)

	const parentID = "e2e-optout-parent-i1"
	// The child parks at the human task → synchronous runner translates that to
	// SubInstanceFailed → parent receives SubInstanceFailed → StatusFailed.
	// runner.Run does NOT return a hard error; it returns the terminal state.
	finalSt, err := runner.Run(ctx, parent, parentID, nil)
	require.NoError(t, err, "runner.Run must not return a hard Go error; the failure is reflected in the terminal status")

	// Parent must be StatusFailed (SubInstanceFailed set it to failed).
	assert.Equal(t, engine.StatusFailed, finalSt.Status,
		"parent must be StatusFailed when child parks and runner uses the synchronous (opt-out) path")

	// The child instance should exist in the store and be StatusRunning
	// (it parked at the human task; no async mechanism to continue it).
	childID := parentID + "-sub-c1"
	childSt, _, loadErr := store.Load(ctx, childID)
	require.NoError(t, loadErr, "child instance must exist even on the synchronous failure path")
	assert.Equal(t, engine.StatusRunning, childSt.Status,
		"child must be StatusRunning (parked at human task; the parent failed because it couldn't wait for the child)")
}
