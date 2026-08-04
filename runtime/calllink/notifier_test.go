package calllink_test

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
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/calllink"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
	"github.com/kartaladev/wrkflw/runtime/kernel"
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
			activity.NewUserTask("child-task", activity.WithEligibleRoles("worker")),
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
	clk := clockwork.NewRealClock()
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

	driver := runtimetest.MustProcessDriver(t, nil, store,
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

	// The child id is derived from the call command's id, which the driver's
	// IDGenerator mints (ADR-0149) and is opaque — read it off the recorded link.
	children, childrenErr := cl.ChildrenOf(ctx, parentID)
	require.NoError(t, childrenErr)
	require.Len(t, children, 1, "the parent must have recorded exactly one child link")
	childID := children[0].ChildInstanceID

	// The child must be parked at the human task.
	childSt, _, loadErr := store.Load(ctx, childID)
	require.NoError(t, loadErr, "child instance must exist")
	assert.Equal(t, engine.StatusRunning, childSt.Status, "child must be StatusRunning at human task")

	// Retrieve the pending human task via the worker actor.
	claimable, err := tasks.ClaimableBy(ctx, worker)
	require.NoError(t, err)
	require.Len(t, claimable, 1, "exactly one human task should be pending (child's task)")
	taskID := claimable[0].TaskID

	// ── Step 2: complete the human task → child completes, link flips ────────
	svc := runtimetest.MustTaskService(t, tasks, az)
	completeTrg, err := svc.Complete(ctx, taskID, worker, engine.CompletionInput{Output: map[string]any{"childResult": "done"}})
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
// without a positional clock argument (ADR-0138: clock defaults to clockwork.NewRealClock()).
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
// a fake clock whose time flows into delivered trigger timestamps (ADR-0138).
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

// drainSignalingCallLinkStore wraps a *kernel.MemCallLinkStore and signals on
// drained after every ClaimPending call, so a test can synchronize on each
// drain (immediate + ticker-driven) without sleeping or bare-counting.
type drainSignalingCallLinkStore struct {
	*kernel.MemCallLinkStore
	drained chan struct{}
}

func (s *drainSignalingCallLinkStore) ClaimPending(ctx context.Context, limit int) ([]kernel.PendingNotify, error) {
	pending, err := s.MemCallLinkStore.ClaimPending(ctx, limit)
	s.drained <- struct{}{}
	return pending, err
}

// TestCallNotifier_TickIsClockDriven proves Run's poll ticker is routed
// through the injected clock (ADR-0138): under a clockwork.FakeClock, no wall
// time passes, so only fc.Advance(poll) — not real time — can produce the
// second drain.
func TestCallNotifier_TickIsClockDriven(t *testing.T) {
	const poll = time.Second
	fc := clockwork.NewFakeClock()
	cl := &drainSignalingCallLinkStore{
		MemCallLinkStore: kernel.NewMemCallLinkStore(),
		drained:          make(chan struct{}, 1),
	}
	reg := kernel.NewMapDefinitionRegistry()
	deliver := calllink.CallDeliverFunc(func(context.Context, *model.ProcessDefinition, string, engine.Trigger) error {
		return nil
	})

	n, err := calllink.NewCallNotifier(cl, deliver, reg,
		calllink.WithClock(fc),
		calllink.WithCallNotifierPollInterval(poll),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = n.Run(ctx) }()

	// Immediate drain, before the first tick.
	<-cl.drained

	// Confirm the ticker waiter is armed (registered at NewTicker, i.e. from
	// Run's start), then advance the fake clock by exactly one poll interval.
	require.NoError(t, fc.BlockUntilContext(t.Context(), 1))
	fc.Advance(poll)

	// This receive IS the assertion: a stdlib time.NewTicker never fires under
	// fc.Advance, so an unrouted Run would never deliver this second drain.
	select {
	case <-cl.drained:
	case <-time.After(2 * time.Second):
		t.Fatal("no clock-driven tick")
	}
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

// terminalParentDef returns a parent that calls notifierChildDef on one branch
// of a fork while the other branch can be driven into an unhandled error.
//
//	parent-start → fork ⇒ { call(CallActivity notifier-child) → parent-end
//	                        gate(UserTask "worker") → parent-boom(error end E9) }
//
// The fork is what makes the scenario reachable: handleUnhandledError's
// immediate-fail branch sets StatusFailed WITHOUT dropping tokens, so the
// call-activity token outlives the parent's death still awaiting its child.
func terminalParentDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "notifier-terminal-parent",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("parent-start"),
			gateway.NewParallel("fork"),
			activity.NewCallActivity("call", model.Latest("notifier-child")),
			event.NewEnd("parent-end"),
			activity.NewUserTask("gate", activity.WithEligibleRoles("worker")),
			event.NewEnd("parent-boom", event.WithErrorCode("E9")),
		},
		Flows: []flow.SequenceFlow{
			{ID: "ntf0", Source: "parent-start", Target: "fork"},
			{ID: "ntf1", Source: "fork", Target: "call"},
			{ID: "ntf2", Source: "call", Target: "parent-end"},
			{ID: "ntf3", Source: "fork", Target: "gate"},
			{ID: "ntf4", Source: "gate", Target: "parent-boom"},
		},
	}
}

// TestCallNotifierRetiresLinkWhenParentIsTerminal pins the CROSS-LAYER contract
// that ADR-0164's terminal guard on handleSubInstanceCompleted depends on.
//
// DrainOnce keys its idempotency off the delivery error
// (runtime/calllink/notifier.go): it retries only when the error is non-nil AND
// not engine.ErrTokenNotFound, and marks the link notified on success OR
// ErrTokenNotFound alike. Before ADR-0164 a terminal parent produced
// ErrTokenNotFound (or, worse, silently resumed); it now produces a nil error
// from the guard. Both land on the SAME branch, so the link is still retired.
//
// This test exists because that is a load-bearing inference, and an inference is
// not evidence. Without it, a future change to either side could silently turn a
// terminal no-op into an infinite redelivery loop (link never marked notified)
// or a dropped notification (link marked notified while the parent was in fact
// resumable). It asserts BOTH failure modes are absent: the first drain reports
// the link notified, and the second drain is a clean no-op.
func TestCallNotifierRetiresLinkWhenParentIsTerminal(t *testing.T) {
	ctx := t.Context()

	clk := clockwork.NewRealClock()
	cl := kernel.NewMemCallLinkStore()
	store := runtimetest.MustMemStore(t, kernel.WithCallLinks(cl))

	worker := authz.Actor{ID: "bob", Roles: []string{"worker"}}
	child := notifierChildDef()
	parent := terminalParentDef()
	reg := kernel.NewMapDefinitionRegistry(child, parent)

	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{"worker": {worker}})
	tasks := humantask.NewMemTaskStore()
	az := authz.RoleAuthorizer{}

	driver := runtimetest.MustProcessDriver(t, nil, store,
		runtime.WithClock(clk),
		runtime.WithCallLinkStore(cl),
		runtime.WithDefinitions(reg),
		runtime.WithHumanTasks(resolver, tasks, az),
	)
	svc := runtimetest.MustTaskService(t, tasks, az)

	// ── Step 1: the parent parks on BOTH branches; the child starts and parks ──
	const parentID = "notifier-terminal-parent-i1"
	st, err := driver.Drive(ctx, parent, parentID, nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, st.Status, "parent must park running on both branches")

	children, err := cl.ChildrenOf(ctx, parentID)
	require.NoError(t, err)
	require.Len(t, children, 1, "the call activity must have recorded exactly one child link")
	childID := children[0].ChildInstanceID

	// Two open human tasks: the parent's "gate" and the child's "child-task".
	claimable, err := tasks.ClaimableBy(ctx, worker)
	require.NoError(t, err)
	require.Len(t, claimable, 2, "setup: the parent's gate and the child's task must both be open")
	var parentGateTaskID, childTaskID string
	for _, ht := range claimable {
		switch ht.NodeID {
		case "gate":
			parentGateTaskID = ht.TaskID
		case "child-task":
			childTaskID = ht.TaskID
		}
	}
	require.NotEmpty(t, parentGateTaskID, "setup: the parent's gate task must be claimable")
	require.NotEmpty(t, childTaskID, "setup: the child's task must be claimable")

	// ── Step 2: kill the PARENT while the call token is still in flight ───────
	gateTrg, err := svc.Complete(ctx, parentGateTaskID, worker, engine.CompletionInput{})
	require.NoError(t, err)
	parentFailed, err := driver.ApplyTrigger(ctx, parent, parentID, gateTrg)
	require.NoError(t, err)
	require.Equal(t, engine.StatusFailed, parentFailed.Status,
		"control: the unhandled error must have FAILED the parent")

	var callTokenSurvives bool
	for _, tok := range parentFailed.Tokens {
		if tok.NodeID == "call" {
			callTokenSurvives = true
		}
	}
	require.True(t, callTokenSurvives,
		"control: the call-activity token must outlive the parent's failure — "+
			"that survival is what makes a late SubInstanceCompleted reach the handler")

	// ── Step 3: the child completes, flipping its link to terminal ────────────
	childTrg, err := svc.Complete(ctx, childTaskID, worker, engine.CompletionInput{Output: map[string]any{"childResult": "done"}})
	require.NoError(t, err)
	childFinal, err := driver.ApplyTrigger(ctx, child, childID, childTrg)
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, childFinal.Status, "control: the child must complete")

	// ── Step 4: DrainOnce delivers to a parent that has been dead since step 2 ─
	deliverFn := calllink.CallDeliverFunc(func(ctx2 context.Context, def *model.ProcessDefinition, instanceID string, trg engine.Trigger) error {
		_, derr := driver.ApplyTrigger(ctx2, def, instanceID, trg)
		return derr
	})
	notifier := runtimetest.MustCallNotifier(t, cl, deliverFn, reg)

	notified, err := notifier.DrainOnce(ctx)
	require.NoError(t, err, "delivering to a terminal parent must not surface as a drain error")
	assert.Equal(t, 1, notified,
		"the link must be RETIRED: a terminal parent's no-op lands on DrainOnce's "+
			"success branch exactly as ErrTokenNotFound did, so the link is marked notified")

	// Failure mode 1 — an infinite redelivery loop, if the link stayed claimable.
	notified2, err := notifier.DrainOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, notified2,
		"second DrainOnce must be a clean no-op — a still-claimable link would retry forever")

	// Failure mode 2 — a resurrected parent, if the guard were absent.
	parentAfter, _, err := store.Load(ctx, parentID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusFailed, parentAfter.Status,
		"the parent must still be Failed: a child completing after its parent died "+
			"must never flip the parent to Completed (ADR-0164)")
	assert.NotContains(t, parentAfter.Variables, "childResult",
		"a dead parent must not absorb the child's output either")
}
