package runtime_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/processtest"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
)

// TestRecurringReminderSurvivesFireAndCancelsOnComplete is the user-requirement
// regression test for recurrence-aware timer cancel. It drives, end-to-end
// through the ProcessDriver + a fake-clock MemScheduler, a user task whose in-wait
// reminder is armed once with a RECURRING trigger (schedule.Every) and proves:
//
//	(a) survive-fire — when the reminder fires, timerOpsFor does NOT cancel it;
//	    it stays armed in the scheduler and fires again on the next interval; and
//	(b) cancel-on-complete — when the host user task completes, the engine emits a
//	    CancelTimer and the runtime cancels it, so the recurring native job stops
//	    (no leak) and the MemScheduler no longer holds it pending.
func TestRecurringReminderSurvivesFireAndCancelsOnComplete(t *testing.T) {
	ctx := t.Context()
	startAt := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	const reminderEvery = 15 * time.Minute
	var reminderRuns atomic.Int64

	cat := action.NewCatalog(map[string]action.Action{
		"ping": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			reminderRuns.Add(1)
			return nil, nil
		}),
	})

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"manager": {manager},
	})
	az := authz.RoleAuthorizer{}
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	store := runtimetest.MustMemStore(t)

	r := runtimetest.MustRunner(t, cat, store,
		runtime.WithClock(fc),
		runtime.WithHumanTasks(resolver, taskStore, az),
		runtime.WithScheduler(sched),
	)

	def := runtimetest.ApprovalWithReminderDef(reminderEvery, "ping")
	const instanceID = "rem-1"

	// --- Run: parks at the user task with the recurring reminder armed. ---
	parked, err := r.Drive(ctx, def, instanceID, nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status)

	// The reminder timer is pending in the scheduler.
	fireAt, ok := sched.NextFireAt()
	require.True(t, ok, "a reminder timer must be armed")
	require.Equal(t, startAt.Add(reminderEvery), fireAt, "reminder armed at now+interval")

	// --- (a) survive-fire: fire the reminder twice; it must re-arm natively. ---
	fc.Advance(reminderEvery + time.Second)
	require.NoError(t, sched.Tick(ctx))
	assert.Equal(t, int64(1), reminderRuns.Load(), "reminder action must run on first fire")
	_, stillPending := sched.NextFireAt()
	require.True(t, stillPending, "a recurring reminder must survive its fire (not consumed)")

	fc.Advance(reminderEvery + time.Second)
	require.NoError(t, sched.Tick(ctx))
	assert.Equal(t, int64(2), reminderRuns.Load(),
		"the recurring reminder must fire again — proof it was not cancelled on the first fire")
	_, stillPending = sched.NextFireAt()
	require.True(t, stillPending, "recurring reminder still armed after the second fire")

	// --- (b) cancel-on-complete: completing the task must stop the reminder. ---
	svc := runtimetest.MustTaskService(t, taskStore, az)
	claimable, err := taskStore.ClaimableBy(ctx, manager)
	require.NoError(t, err)
	require.Len(t, claimable, 1)
	taskToken := claimable[0].TaskToken

	claimTrg, err := svc.Claim(ctx, taskToken, manager)
	require.NoError(t, err)
	_, err = r.ApplyTrigger(ctx, def, instanceID, claimTrg)
	require.NoError(t, err)

	completeTrg, err := svc.Complete(ctx, taskToken, manager, map[string]any{"approved": true})
	require.NoError(t, err)
	final, err := r.ApplyTrigger(ctx, def, instanceID, completeTrg)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status)

	// The recurring reminder timer was cancelled on completion: nothing pending.
	_, pendingAfterComplete := sched.NextFireAt()
	assert.False(t, pendingAfterComplete, "completing the host task must cancel the recurring reminder (no leak)")

	// A further Tick after completion must NOT run the reminder again.
	before := reminderRuns.Load()
	fc.Advance(reminderEvery + time.Second)
	require.NoError(t, sched.Tick(ctx))
	assert.Equal(t, before, reminderRuns.Load(), "no reminder fire after the timer is cancelled")
}
