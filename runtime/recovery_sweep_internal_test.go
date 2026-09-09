// Internal test for the recovery sweep's LEASE.
//
// The lease is only reachable through RunRecoverySweep's ticker, and driving a
// ticker to prove a time-based rule buys a scheduling race for no extra
// coverage. sweepPendingCommands takes the lease as a parameter precisely so it
// can be exercised directly, against a fake clock, with no waiting at all —
// which is also what docs/agents/eventually-waits.md and the per-package wait
// budget both prefer.
package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// markedInstance seeds store with one running instance carrying a
// pending-command mark stamped at markedAt, without going through a driver: the
// lease rule is about the mark's AGE, so manufacturing the mark directly keeps
// the test about that one rule.
func markedInstance(t *testing.T, store *kernel.MemInstanceStore, def *model.ProcessDefinition, id string, markedAt time.Time) {
	t.Helper()
	markedInstanceWith(t, store, def, id, markedAt, []engine.PendingCommand{{
		Kind:          engine.PendingInvokeAction,
		CommandID:     "cmd-1",
		Name:          "lease-action",
		FireAndForget: true, // no token awaits it, so no follow-up trigger to apply
	}})
}

// markedInstanceWith is markedInstance with an explicit mark.
func markedInstanceWith(t *testing.T, store *kernel.MemInstanceStore, def *model.ProcessDefinition, id string, markedAt time.Time, cmds []engine.PendingCommand) {
	t.Helper()
	st := engine.InstanceState{
		InstanceID:        id,
		DefID:             def.ID,
		DefVersion:        def.Version,
		Status:            engine.StatusRunning,
		StartedAt:         markedAt,
		PendingCommands:   cmds,
		PendingCommandsAt: markedAt,
	}
	_, err := store.Create(t.Context(), kernel.AppliedStep{
		State:   st,
		Trigger: engine.NewStartInstance(markedAt, nil),
	})
	require.NoError(t, err)
}

// TestRecoverySweepGraceWindowGatesEveryPass pins the rule that keeps a pass
// from overtaking a perform that is still legitimately running — and pins that
// the BOOT pass obeys it too.
//
// The boot pass used to skip the window, on the reasoning that a mark found at
// boot cannot belong to a perform this process is running. That is true and
// beside the point: it can belong to a perform another live replica is running,
// and the durable path this sweep is wired for is the multi-replica one. A
// rolling restart under the old rule re-performed every in-flight command of
// every surviving replica.
func TestRecoverySweepGraceWindowGatesEveryPass(t *testing.T) {
	t.Parallel()

	markedAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	type testCase struct {
		name   string
		now    time.Time
		lease  time.Duration
		boot   bool // drive through RecoverPendingCommands rather than the pass
		assert func(t *testing.T, recovered int, calls int, st engine.InstanceState)
	}

	cases := []testCase{
		{
			name:  "inside the window the mark is left alone",
			now:   markedAt.Add(time.Minute),
			lease: 5 * time.Minute,
			assert: func(t *testing.T, recovered, calls int, st engine.InstanceState) {
				assert.Zero(t, recovered, "a mark younger than the window must not be re-driven")
				assert.Zero(t, calls, "the action must not be invoked while the window holds")
				assert.NotEmpty(t, st.PendingCommands, "the mark must survive a declined pass")
			},
		},
		{
			name:  "past the window the mark is re-driven",
			now:   markedAt.Add(6 * time.Minute),
			lease: 5 * time.Minute,
			assert: func(t *testing.T, recovered, calls int, st engine.InstanceState) {
				assert.Equal(t, 1, recovered)
				assert.Equal(t, 1, calls, "the abandoned action must be invoked exactly once")
				assert.Empty(t, st.PendingCommands, "a re-driven mark must be cleared")
			},
		},
		{
			name:  "the boot pass obeys the same window",
			now:   markedAt.Add(time.Minute),
			lease: 5 * time.Minute,
			boot:  true,
			assert: func(t *testing.T, recovered, calls int, st engine.InstanceState) {
				assert.Zero(t, recovered,
					"a boot pass must not steal a mark another live replica may still be performing")
				assert.Zero(t, calls)
				assert.NotEmpty(t, st.PendingCommands)
			},
		},
		{
			name:  "the boot pass re-drives once the window has passed",
			now:   markedAt.Add(6 * time.Minute),
			lease: 5 * time.Minute,
			boot:  true,
			assert: func(t *testing.T, recovered, calls int, st engine.InstanceState) {
				assert.Equal(t, 1, recovered)
				assert.Equal(t, 1, calls)
				assert.Empty(t, st.PendingCommands)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			calls := 0
			def := &model.ProcessDefinition{
				ID: "lease-def", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewServiceTask("work", activity.WithTaskAction("lease-action")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "work"},
					{ID: "f2", Source: "work", Target: "end"},
				},
			}
			cat := action.NewCatalog(map[string]action.Action{
				"lease-action": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
					calls++
					return nil, nil
				}),
			})
			reg := kernel.NewMemDefinitionRegistry()
			require.NoError(t, reg.Register(def))

			store, err := kernel.NewMemInstanceStore()
			require.NoError(t, err)
			markedInstance(t, store, def, "i-lease", markedAt)

			driver, err := NewProcessDriver(
				WithActionCatalog(cat),
				WithInstanceStore(store),
				WithDefinitions(reg),
				WithClock(clockwork.NewFakeClockAt(tc.now)),
				WithRecoveryLease(tc.lease),
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })

			var recovered int
			if tc.boot {
				recovered, err = driver.RecoverPendingCommands(ctx)
			} else {
				recovered, err = driver.sweepPendingCommands(ctx)
			}
			require.NoError(t, err)

			st, _, err := store.Load(ctx, "i-lease")
			require.NoError(t, err)
			tc.assert(t, recovered, calls, st)
		})
	}
}

// TestRecoverySweepSkipsInstancesOwnedByAnotherReplica pins the exclusion
// mechanism the grace window is NOT: with an ownership port configured, a
// process that does not own an instance must not re-drive it.
//
// The repo already ships this port for exactly this guarantee
// (kernel.InstanceOwnership, persistence.NewAdvisoryLockOwnership), and a sweep
// that ignored it would hand a deployment that bought single-writer safety N
// concurrent re-drives on a rolling restart.
func TestRecoverySweepSkipsInstancesOwnedByAnotherReplica(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	markedAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	calls := 0
	def := &model.ProcessDefinition{
		ID: "owned-def", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("work", activity.WithTaskAction("lease-action")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "work"},
			{ID: "f2", Source: "work", Target: "end"},
		},
	}
	cat := action.NewCatalog(map[string]action.Action{
		"lease-action": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
			calls++
			return nil, nil
		}),
	})
	reg := kernel.NewMemDefinitionRegistry()
	require.NoError(t, reg.Register(def))

	store, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	markedInstance(t, store, def, "i-owned", markedAt)

	driver, err := NewProcessDriver(
		WithActionCatalog(cat),
		WithInstanceStore(store),
		WithDefinitions(reg),
		WithClock(clockwork.NewFakeClockAt(markedAt.Add(time.Hour))),
		WithInstanceOwnership(ownedByNobody{}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })

	recovered, err := driver.RecoverPendingCommands(ctx)
	require.NoError(t, err)
	assert.Zero(t, recovered, "an instance another replica owns must not be re-driven")
	assert.Zero(t, calls, "the action must not be invoked by a non-owner")

	st, _, err := store.Load(ctx, "i-owned")
	require.NoError(t, err)
	assert.NotEmpty(t, st.PendingCommands, "the mark must survive for the owner to find")
}

// ownedByNobody is an InstanceOwnership that never grants ownership — the
// stand-in for "another replica holds the lease".
type ownedByNobody struct{}

func (ownedByNobody) Acquire(context.Context, string) (bool, error) { return false, nil }
func (ownedByNobody) Release(context.Context, string) error         { return nil }

// TestRedrivenStartSubInstanceDoesNotFabricateAFailureAgainstALiveChild is the
// red for the call-activity half of the re-drive.
//
// The child instance id is derived deterministically from (parent, commandID),
// so re-performing a StartSubInstance whose child already exists hits
// kernel.ErrInstanceExists inside runChild. That used to be mapped, with every
// other runChild error, to engine.SubInstanceFailed — a FABRICATED call-activity
// failure delivered to the parent while the child was alive and would later
// notify a token that no longer awaited it. Not a benign duplicate: a wrong
// outcome.
//
// Two guards close it, and this test would go red if either regressed: the sweep
// skips a command whose child is already there, and startSubInstanceAsync reads
// ErrInstanceExists as "already started" rather than as a failure.
func TestRedrivenStartSubInstanceDoesNotFabricateAFailureAgainstALiveChild(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	markedAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	childDef := &model.ProcessDefinition{
		ID: "child-def", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("hold", activity.WithEligibleRoles("manager")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "hold"},
			{ID: "f2", Source: "hold", Target: "end"},
		},
	}
	parentDef := &model.ProcessDefinition{
		ID: "parent-def", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewCallActivity("call", model.Version("child-def", 1)),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "call"},
			{ID: "f2", Source: "call", Target: "end"},
		},
	}
	reg := kernel.NewMemDefinitionRegistry()
	require.NoError(t, reg.Register(childDef))
	require.NoError(t, reg.Register(parentDef))

	links := kernel.NewMemCallLinkStore()
	store, err := kernel.NewMemInstanceStore(kernel.WithCallLinks(links))
	require.NoError(t, err)

	tasks := humantask.NewMemTaskStore()
	driver, err := NewProcessDriver(
		WithInstanceStore(store),
		WithDefinitions(reg),
		WithCallLinkStore(links),
		WithHumanTasks(humantask.NewStaticActorResolver(map[string][]authz.Actor{
			"manager": {{ID: "alice", Roles: []string{"manager"}}},
		}), tasks, authz.AllowAll{}),
		WithClock(clockwork.NewFakeClockAt(markedAt)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })

	// Drive the parent normally: it parks at the call activity and the child is
	// started and parked on its user task.
	parent, err := driver.Drive(ctx, parentDef, "i-parent", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parent.Status)

	// Recover the command id the parent parked on, and put the mark back by hand
	// — this is the state a crash between the durable clear and the next commit
	// leaves, and the state a second replica reads while the first is mid-perform.
	var commandID string
	for _, tok := range parent.Tokens {
		if tok.AwaitCommand != "" {
			commandID = tok.AwaitCommand
		}
	}
	require.NotEmpty(t, commandID, "the parent must be parked on a call-activity command")
	childID := childInstanceIDFor("i-parent", commandID)
	_, _, err = store.Load(ctx, childID)
	require.NoError(t, err, "the child must already exist before the re-drive")

	_, token, err := store.Load(ctx, "i-parent")
	require.NoError(t, err)
	require.NoError(t, store.WritePendingCommands(ctx, "i-parent", token, []engine.PendingCommand{{
		Kind:      engine.PendingStartSubInstance,
		CommandID: commandID,
		DefRef:    model.Version("child-def", 1),
	}}, markedAt))

	// Sweep past the grace window.
	swept, err := NewProcessDriver(
		WithInstanceStore(store),
		WithDefinitions(reg),
		WithCallLinkStore(links),
		WithHumanTasks(humantask.NewStaticActorResolver(nil), tasks, authz.AllowAll{}),
		WithClock(clockwork.NewFakeClockAt(markedAt.Add(time.Hour))),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = swept.Shutdown(context.Background()) })

	_, err = swept.RecoverPendingCommands(ctx)
	require.NoError(t, err)

	after, _, err := store.Load(ctx, "i-parent")
	require.NoError(t, err)
	assert.NotEqual(t, engine.StatusFailed, after.Status,
		"re-driving a StartSubInstance whose child is alive must not fail the parent")
	stillWaiting := false
	for _, tok := range after.Tokens {
		if tok.AwaitCommand == commandID {
			stillWaiting = true
		}
	}
	assert.True(t, stillWaiting,
		"the parent must stay parked on the call activity, awaiting the live child's real outcome")

	child, _, err := store.Load(ctx, childID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, child.Status, "the live child must be untouched")
}

// TestRedrivenAwaitHumanDoesNotEraseAnExistingClaim is the red for the
// human-task half of the re-drive, reproduced in security review.
//
// performAwaitHuman rebuilds the projected task from scratch with State:
// Unclaimed and no Claim, and TaskStore.Upsert REPLACES the row — so re-driving
// one after an actor claimed the task silently erased the claimant from every
// inbox and from what TaskService loads before it authorizes. The engine's own
// state did not fork (a second HumanClaimed is still rejected), but the
// projection re-opened the task to every other eligible actor and lost the audit
// of who held it.
//
// Reachable on a store that omits the optional kernel.PendingCommandWriter — a
// configuration that interface documents as supported — and through the
// concurrent-replica window on one that implements it.
func TestRedrivenAwaitHumanDoesNotEraseAnExistingClaim(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	markedAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	def := &model.ProcessDefinition{
		ID: "claim-def", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("approve", activity.WithEligibleRoles("manager")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}
	reg := kernel.NewMemDefinitionRegistry()
	require.NoError(t, reg.Register(def))

	store, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	tasks := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"manager": {{ID: "alice", Roles: []string{"manager"}}},
	})

	driver, err := NewProcessDriver(
		WithInstanceStore(store),
		WithDefinitions(reg),
		WithHumanTasks(resolver, tasks, authz.AllowAll{}),
		WithClock(clockwork.NewFakeClockAt(markedAt)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })

	st, err := driver.Drive(ctx, def, "i-claim", nil)
	require.NoError(t, err)
	require.Len(t, st.Tasks, 1)
	taskID := st.Tasks[0].TaskID

	// Alice claims the task.
	claimed, err := tasks.Get(ctx, taskID)
	require.NoError(t, err)
	claimed.State = humantask.Claimed
	claimed.Claim = &humantask.Claim{Actor: authz.Actor{ID: "alice"}, At: markedAt}
	require.NoError(t, tasks.Upsert(ctx, claimed))

	// Put the mark back by hand: the state a crash between the durable clear and
	// the next commit leaves, and the state a second replica reads.
	_, token, err := store.Load(ctx, "i-claim")
	require.NoError(t, err)
	require.NoError(t, store.WritePendingCommands(ctx, "i-claim", token, []engine.PendingCommand{{
		Kind:        engine.PendingAwaitHuman,
		TaskID:      taskID,
		Eligibility: st.Tasks[0].Eligibility,
	}}, markedAt))

	swept, err := NewProcessDriver(
		WithInstanceStore(store),
		WithDefinitions(reg),
		WithHumanTasks(resolver, tasks, authz.AllowAll{}),
		WithClock(clockwork.NewFakeClockAt(markedAt.Add(time.Hour))),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = swept.Shutdown(context.Background()) })

	_, err = swept.RecoverPendingCommands(ctx)
	require.NoError(t, err)

	after, err := tasks.Get(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, humantask.Claimed, after.State,
		"a re-driven AwaitHuman must not revert a claimed task to unclaimed")
	require.NotNil(t, after.Claim, "a re-driven AwaitHuman must not drop the claimant")
	assert.Equal(t, "alice", after.Claim.Actor.ID)
}

// TestFailedRecoveryRecordsProgressAndRestampsTheMark is the red for the
// unbounded-re-drive defect reproduced in security review: a payment action ran
// eleven times for one committed step.
//
// Two halves, both asserted here. A pass that succeeds on one command and fails
// on the next must (a) remove the succeeded one from the durable mark, so the
// next pass resumes rather than restarts, and (b) re-stamp the mark, so the
// grace window measures from the last ATTEMPT and a permanently failing instance
// is spaced out instead of retried on every tick forever.
func TestFailedRecoveryRecordsProgressAndRestampsTheMark(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	markedAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	sweptAt := markedAt.Add(time.Hour)

	def := &model.ProcessDefinition{
		ID: "progress-def", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("work", activity.WithTaskAction("charge-card")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "work"},
			{ID: "f2", Source: "work", Target: "end"},
		},
	}
	reg := kernel.NewMemDefinitionRegistry()
	require.NoError(t, reg.Register(def))

	charges := 0
	cat := action.NewCatalog(map[string]action.Action{
		"charge-card": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
			charges++
			return nil, nil
		}),
	})

	store, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	// A mark whose FIRST command succeeds (fire-and-forget, so no follow-up
	// trigger commits over the mark) and whose second can never be performed:
	// there is no TaskStore, so AwaitHuman fails on every attempt.
	markedInstanceWith(t, store, def, "i-progress", markedAt, []engine.PendingCommand{
		{Kind: engine.PendingInvokeAction, CommandID: "cmd-1", Name: "charge-card", FireAndForget: true},
		{Kind: engine.PendingAwaitHuman, TaskID: "task-never"},
	})

	driver, err := NewProcessDriver(
		WithActionCatalog(cat),
		WithInstanceStore(store),
		WithDefinitions(reg),
		WithClock(clockwork.NewFakeClockAt(sweptAt)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })

	_, err = driver.sweepPendingCommands(ctx)
	require.NoError(t, err, "one unrecoverable instance must not abort the batch")
	require.Equal(t, 1, charges, "the recoverable command must run exactly once")

	st, _, err := store.Load(ctx, "i-progress")
	require.NoError(t, err)
	require.Len(t, st.PendingCommands, 1,
		"the succeeded command must be removed from the mark so the next pass resumes")
	assert.Equal(t, engine.PendingAwaitHuman, st.PendingCommands[0].Kind)
	assert.True(t, st.PendingCommandsAt.Equal(sweptAt),
		"a failed attempt must re-stamp the mark so the grace window measures from the attempt")

	// A second pass at the same instant is inside the re-stamped window and must
	// do nothing at all — the amplification is gone.
	_, err = driver.sweepPendingCommands(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, charges, "the succeeded command must never be re-executed")
}
