package runtime_test

import (
	"context"
	"testing"
	"time"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/processtest"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
	"github.com/kartaladev/wrkflw/runtime/kernel"
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
		driver := runtimetest.MustProcessDriver(t, cat, store,
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
	driver2 := runtimetest.MustProcessDriver(t, cat, store,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched2),
		runtime.WithTimerStore(mts),
		runtime.WithDefinitions(reg),
	)

	// The main assertion: Load returns exactly one ScheduledJob.
	js := runtime.NewJobStore(driver2)
	jobs, err := js.Load(t.Context())
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, timerID, jobs[0].ID())

	data, err := jobs[0].Data().Get(t.Context())
	require.NoError(t, err)
	assert.Equal(t, instanceID, data["instance_id"])
	assert.Equal(t, def.ID, data["def_id"])
	assert.Equal(t, def.Version, data["def_version"])

	// Firing the rebuilt callback must advance the parked instance to completion.
	// Action() delivers a TimerFired trigger; the engine then runs the "greet"
	// service task inline and the instance reaches StatusCompleted.
	require.NoError(t, jobs[0].Action()(t.Context(), jobs[0].Data()))

	final, _, loadErr := store.Load(t.Context(), instanceID)
	require.NoError(t, loadErr)
	assert.Equal(t, engine.StatusCompleted, final.Status, "instance must complete after Fire()")
}

// TestJobStoreLoadPreservesKindAndNextRun proves that Load's rebuilt
// descriptor faithfully carries the persisted ArmedTimer's Kind and NextRun
// (ADR-0134 B1 constraint 5) rather than defaulting to the zero TimerKind. It
// observes this externally by round-tripping the rebuilt job through Save
// into a fresh MemTimerStore and inspecting the resulting row — Load and Save
// are both exercised through the public scheduler.JobStore surface only.
func TestJobStoreLoadPreservesKindAndNextRun(t *testing.T) {
	t.Parallel()

	def := runtimetest.TimerIntermediateDef()
	reg := kernel.NewMapDefinitionRegistry(def)
	nextRun := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		kind engine.TimerKind
	}{
		{name: "deadline-timer", kind: engine.TimerDeadline},
		{name: "in-wait-timer", kind: engine.TimerInWait},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mts := kernel.NewMemTimerStore()
			mts.Arm(kernel.ArmedTimer{
				InstanceID: "kind-" + tc.name,
				DefID:      def.ID,
				DefVersion: def.Version,
				TimerID:    "kind-" + tc.name + "-tm1",
				Trigger:    schedule.AfterDuration(time.Hour),
				NextRun:    nextRun,
				Kind:       tc.kind,
			})

			store := runtimetest.MustMemStore(t)
			driver := runtimetest.MustProcessDriver(t, action.NewCatalog(nil), store,
				runtime.WithTimerStore(mts),
				runtime.WithDefinitions(reg),
			)

			jobs, err := runtime.NewJobStore(driver).Load(t.Context())
			require.NoError(t, err)
			require.Len(t, jobs, 1)

			// Round-trip through Save into a fresh, independent store: the only
			// way to observe the rebuilt descriptor's fields from outside the
			// runtime package.
			dst := kernel.NewMemTimerStore()
			dstStore := runtimetest.MustMemStore(t)
			dstDriver := runtimetest.MustProcessDriver(t, action.NewCatalog(nil), dstStore,
				runtime.WithTimerStore(dst),
			)
			require.NoError(t, runtime.NewJobStore(dstDriver).Save(t.Context(), jobs[0]))

			got, lerr := dst.ListArmed(t.Context())
			require.NoError(t, lerr)
			require.Len(t, got, 1)
			assert.Equal(t, tc.kind, got[0].Kind)
			assert.True(t, got[0].NextRun.Equal(nextRun))
		})
	}
}

func TestJobStoreLoadScheduledNilStoreReturnsNil(t *testing.T) {
	// A driver with no TimerStore should return (nil, nil) — no durable timers configured.
	store := runtimetest.MustMemStore(t)
	driver := runtimetest.MustProcessDriver(t, action.NewCatalog(nil), store)

	js := runtime.NewJobStore(driver)
	jobs, err := js.Load(t.Context())
	require.NoError(t, err)
	assert.Nil(t, jobs)
}
