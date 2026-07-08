package calllink_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/calllink"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// notifierChildDef returns a child def whose single task is a human task with
// candidate role "worker" so a known actor can claim/complete it in the test.
//
//	child-start → child-task (KindUserTask, role "worker") → child-end
func notifierChildDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "notifier-child",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("child-start"),
			activity.NewUserTask("child-task", []string{"worker"}),
			event.NewEnd("child-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "ncf1", Source: "child-start", Target: "child-task"},
			{ID: "ncf2", Source: "child-task", Target: "child-end"},
		},
	}
}

// notifierParentDef returns a parent def calling notifierChildDef.
//
//	parent-start → call (KindCallActivity, DefRef:"notifier-child") → parent-end
func notifierParentDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "notifier-parent",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("parent-start"),
			activity.NewCallActivity("call", model.Latest("notifier-child")),
			event.NewEnd("parent-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "npf1", Source: "parent-start", Target: "call"},
			{ID: "npf2", Source: "call", Target: "parent-end"},
		},
	}
}

// TestCallNotifierResumesParkedParent is the headline e2e for Task 4.
//
// Sequence:
//  1. Parent calls a child that parks on a human task → parent is StatusRunning.
//  2. ApplyTrigger HumanCompleted to the child → child completes, link flips to terminal.
//  3. Build a CallNotifier and call DrainOnce → parent resumes, reaches StatusCompleted.
//  4. Assert parent is StatusCompleted.
//  5. Second DrainOnce is a no-op (link is marked notified).
func TestCallNotifierResumesParkedParent(t *testing.T) {
	ctx := t.Context()

	// ── wiring ───────────────────────────────────────────────────────────────
	clk := clock.System()
	cl := kernel.NewMemCallLinkStore()
	store := runtimetest.MustMemStore(t, kernel.WithCallLinks(cl))

	worker := authz.Actor{ID: "bob", Roles: []string{"worker"}}
	child := notifierChildDef()
	parent := notifierParentDef()

	// Parent definition must be resolvable under the "id:version" ref format.
	reg := kernel.NewMapDefinitionRegistry(child, parent)

	// Wire human tasks: "worker" role → bob.
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"worker": {worker},
	})
	tasks := humantask.NewMemTaskStore()
	az := authz.RoleAuthorizer{}

	driver := runtimetest.MustRunner(t, nil, store,
		runtime.WithClock(clk),
		runtime.WithCallLinkStore(cl),
		runtime.WithDefinitions(reg),
		runtime.WithHumanTasks(resolver, tasks, az),
	)

	// ── Step 1: run parent; it parks because the child parks at the human task ──
	const parentID = "notifier-parent-i1"
	st, err := driver.Drive(ctx, parent, parentID, nil)
	require.NoError(t, err, "runner.Run must not error")
	assert.Equal(t, engine.StatusRunning, st.Status, "parent must be StatusRunning (parked at call activity)")

	// Derive child ID (scheme: "<parentID>-sub-c1").
	childID := parentID + "-sub-c1"

	// The child must be parked at the human task.
	childSt, _, loadErr := store.Load(ctx, childID)
	require.NoError(t, loadErr, "child instance must exist")
	assert.Equal(t, engine.StatusRunning, childSt.Status, "child must be StatusRunning at human task")

	// Retrieve the pending human task via the worker actor.
	claimable, err := tasks.ClaimableBy(ctx, worker)
	require.NoError(t, err)
	require.Len(t, claimable, 1, "exactly one human task should be pending (child's task)")
	taskToken := claimable[0].TaskToken

	// ── Step 2: complete the human task → child completes, link flips ────────
	svc := runtimetest.MustTaskService(t, tasks, az)
	completeTrg, err := svc.Complete(ctx, taskToken, worker, map[string]any{"childResult": "done"})
	require.NoError(t, err)

	childFinalSt, err := driver.ApplyTrigger(ctx, child, childID, completeTrg)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, childFinalSt.Status, "child must be StatusCompleted after human task completion")

	// The call link must now be terminal.
	pending, err := cl.ClaimPending(ctx, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1, "exactly one pending notify after child completes")
	assert.True(t, pending[0].Outcome.Completed, "link outcome must be Completed")

	// ── Step 3: build CallNotifier and DrainOnce → parent resumes ─────────
	deliverFn := calllink.CallDeliverFunc(func(ctx2 context.Context, def *model.ProcessDefinition, instanceID string, trg engine.Trigger) error {
		_, err2 := driver.ApplyTrigger(ctx2, def, instanceID, trg)
		return err2
	})

	notifier := runtimetest.MustCallNotifier(t, cl, deliverFn, reg)

	notified, err := notifier.DrainOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, notified, "DrainOnce must report 1 notified link")

	// ── Step 4: parent must now be StatusCompleted ────────────────────────────
	parentFinalSt, _, loadErr := store.Load(ctx, parentID)
	require.NoError(t, loadErr)
	assert.Equal(t, engine.StatusCompleted, parentFinalSt.Status,
		"parent must be StatusCompleted after CallNotifier resumes it")

	// ── Step 5: second DrainOnce is a no-op (link is marked notified) ────────
	notified2, err := notifier.DrainOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, notified2, "second DrainOnce must be a no-op (link already notified)")
}

// TestNewCallNotifierDefaultClockNoPanic verifies that NewCallNotifier works
// without a positional clock argument (ADR-0003: clock defaults to clock.System()).
func TestNewCallNotifierDefaultClockNoPanic(t *testing.T) {
	cl := kernel.NewMemCallLinkStore()
	deliver := calllink.CallDeliverFunc(func(_ context.Context, _ *model.ProcessDefinition, _ string, _ engine.Trigger) error {
		return nil
	})
	reg := kernel.NewMapDefinitionRegistry()

	n := runtimetest.MustCallNotifier(t, cl, deliver, reg)
	assert.NotNil(t, n)
}

// TestNewCallNotifierWithClockOption verifies that WithClock injects
// a fake clock whose time flows into delivered trigger timestamps (ADR-0003).
func TestNewCallNotifierWithClockOption(t *testing.T) {
	ctx := t.Context()

	fakeTime := time.Unix(1000, 0).UTC()
	fake := clockwork.NewFakeClockAt(fakeTime)

	cl := kernel.NewMemCallLinkStore()
	var capturedTrigger engine.Trigger
	deliver := calllink.CallDeliverFunc(func(_ context.Context, _ *model.ProcessDefinition, _ string, trg engine.Trigger) error {
		capturedTrigger = trg
		return nil
	})

	// Wire minimal parent def so the registry resolves the parent ref.
	parentDef := &model.ProcessDefinition{ID: "opt-parent", Version: 1}
	reg := kernel.NewMapDefinitionRegistry(parentDef)

	n := runtimetest.MustCallNotifier(t, cl, deliver, reg, calllink.WithClock(fake))
	require.NotNil(t, n)

	// Seed a terminal call link so DrainOnce delivers a trigger.
	link := kernel.CallLink{
		ChildInstanceID:  "child-1",
		ParentInstanceID: "parent-1",
		ParentDefID:      "opt-parent",
		ParentDefVersion: 1,
		ParentCommandID:  "cmd-1",
	}
	runtimetest.SeedTerminalCallLink(t, cl, link, kernel.CallOutcome{
		Completed: true,
		Output:    map[string]any{"k": "v"},
	})

	notified, err := n.DrainOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, notified, "DrainOnce must report 1 notified link")
	require.NotNil(t, capturedTrigger, "deliver must have been called with a trigger")

	// The trigger timestamp must equal the fake clock's time.
	assert.Equal(t, fakeTime, capturedTrigger.OccurredAt(),
		"trigger timestamp must reflect the injected fake clock time")
}

func TestNewCallNotifierFailsFast(t *testing.T) {
	t.Parallel()

	cl := kernel.NewMemCallLinkStore()
	var deliver calllink.CallDeliverFunc = func(_ context.Context, _ *model.ProcessDefinition, _ string, _ engine.Trigger) error {
		return nil
	}
	reg := kernel.NewMapDefinitionRegistry(nil)

	type testCase struct {
		name    string
		cl      kernel.CallLinkStore
		deliver calllink.CallDeliverFunc
		reg     kernel.DefinitionRegistry
		assert  func(t *testing.T, n *calllink.CallNotifier, err error)
	}
	cases := []testCase{
		{
			name:    "nil call link store",
			cl:      nil,
			deliver: deliver,
			reg:     reg,
			assert: func(t *testing.T, n *calllink.CallNotifier, err error) {
				require.ErrorIs(t, err, kernel.ErrNilDependency)
				require.Nil(t, n)
			},
		},
		{
			name:    "nil deliver func",
			cl:      cl,
			deliver: nil,
			reg:     reg,
			assert: func(t *testing.T, n *calllink.CallNotifier, err error) {
				require.ErrorIs(t, err, kernel.ErrNilDependency)
				require.Nil(t, n)
			},
		},
		{
			name:    "nil registry",
			cl:      cl,
			deliver: deliver,
			reg:     nil,
			assert: func(t *testing.T, n *calllink.CallNotifier, err error) {
				require.ErrorIs(t, err, kernel.ErrNilDependency)
				require.Nil(t, n)
			},
		},
		{
			name:    "valid args",
			cl:      cl,
			deliver: deliver,
			reg:     reg,
			assert: func(t *testing.T, n *calllink.CallNotifier, err error) {
				require.NoError(t, err)
				require.NotNil(t, n)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n, err := calllink.NewCallNotifier(tc.cl, tc.deliver, tc.reg)
			tc.assert(t, n, err)
		})
	}
}
