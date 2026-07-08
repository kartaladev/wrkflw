package runtime_test

import (
	"context"
	"testing"
	"time"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/processtest"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

func TestJobStoreLoadScheduledRebuildsExecutableFire(t *testing.T) {
	const instanceID = "js-1"
	startAt := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)
	mts := kernel.NewMemTimerStore()
	store := runtimetest.MustMemStore(t, kernel.WithTimers(mts))

	def := runtimetest.TimerIntermediateDef()
	reg := kernel.NewMapDefinitionRegistry(def)

	cat := action.NewCatalog(map[string]action.Action{
		"greet": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"greeted": true}, nil
		}),
	})

	// Arm the timer by driving the instance so it parks at the intermediate timer catch.
	{
		sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
		driver := runtimetest.MustRunner(t, cat, store,
			runtime.WithClock(fc),
			runtime.WithScheduler(sched),
			runtime.WithTimerStore(mts),
			runtime.WithDefinitions(reg),
		)
		_, err := driver.Drive(t.Context(), def, instanceID, nil)
		require.NoError(t, err)
	}

	// Verify the timer store has exactly one armed timer before testing LoadScheduled.
	armed, err := mts.ListArmed(t.Context())
	require.NoError(t, err)
	require.Len(t, armed, 1, "expected one armed timer after Drive")
	timerID := armed[0].TimerID

	// Build a new driver (simulating a restart) pointing at the same store.
	sched2 := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	driver2 := runtimetest.MustRunner(t, cat, store,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched2),
		runtime.WithTimerStore(mts),
		runtime.WithDefinitions(reg),
	)

	// The main assertion: LoadScheduled returns exactly one ScheduledJob.
	js := runtime.NewJobStore(driver2)
	jobs, err := js.LoadScheduled(t.Context())
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, timerID, jobs[0].Spec.TimerID)
	assert.Equal(t, instanceID, jobs[0].Spec.InstanceID)
	assert.Equal(t, def.ID, jobs[0].Spec.DefID)
	assert.Equal(t, def.Version, jobs[0].Spec.DefVersion)

	// Firing the rebuilt callback must advance the parked instance to completion.
	// Fire() calls ApplyTrigger(TimerFired) directly; the engine then runs the
	// "greet" service task inline and the instance reaches StatusCompleted.
	jobs[0].Fire()

	final, _, loadErr := store.Load(t.Context(), instanceID)
	require.NoError(t, loadErr)
	assert.Equal(t, engine.StatusCompleted, final.Status, "instance must complete after Fire()")
}

func TestJobStoreLoadScheduledNilStoreReturnsNil(t *testing.T) {
	// A driver with no TimerStore should return (nil, nil) — no durable timers configured.
	store := runtimetest.MustMemStore(t)
	driver := runtimetest.MustRunner(t, action.NewCatalog(nil), store)

	js := runtime.NewJobStore(driver)
	jobs, err := js.LoadScheduled(t.Context())
	require.NoError(t, err)
	assert.Nil(t, jobs)
}
