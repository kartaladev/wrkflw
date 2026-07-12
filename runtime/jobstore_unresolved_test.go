package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/processtest"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/scheduling"
)

// greetAction is a trivial action used across rehydration tests.
var greetAction = action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
	return map[string]any{"greeted": true}, nil
})

// TestLoadScheduledUnresolvedDefinition_ReturnsSentinel proves that LoadScheduled
// returns (partialJobs, err wrapping ErrUnresolvedTimerDefinitions) when all armed
// timers reference definitions not present in the registry.
func TestLoadScheduledUnresolvedDefinition_ReturnsSentinel(t *testing.T) {
	t.Parallel()

	startAt := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)
	mts := kernel.NewMemTimerStore()
	store := runtimetest.MustMemStore(t, kernel.WithTimers(mts))

	def := runtimetest.TimerIntermediateDef()
	reg := kernel.NewMapDefinitionRegistry(def)
	cat := action.NewCatalog(map[string]action.Action{"greet": greetAction})

	// Arm a timer by driving to the parked state.
	{
		sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
		driver := runtimetest.MustProcessDriver(t, cat, store,
			runtime.WithClock(fc),
			runtime.WithScheduler(sched),
			runtime.WithTimerStore(mts),
			runtime.WithDefinitions(reg),
		)
		_, err := driver.Drive(t.Context(), def, "unresolved-sentinel-1", nil)
		require.NoError(t, err)
	}

	// Build a fresh driver WITHOUT the definition registered — simulates unresolved defs.
	emptyReg := kernel.NewMapDefinitionRegistry() // empty — no definitions
	freshStore := runtimetest.MustMemStore(t, kernel.WithTimers(mts))
	driver2 := runtimetest.MustProcessDriver(t, cat, freshStore,
		runtime.WithClock(fc),
		runtime.WithTimerStore(mts),
		runtime.WithDefinitions(emptyReg),
	)

	js := runtime.NewJobStore(driver2)
	jobs, err := js.LoadScheduled(t.Context())

	// Must return the sentinel error (not nil, not some generic DB error).
	require.Error(t, err)
	assert.True(t, errors.Is(err, kernel.ErrUnresolvedTimerDefinitions),
		"expected ErrUnresolvedTimerDefinitions, got: %v", err)

	// No resolvable jobs.
	assert.Empty(t, jobs, "no resolvable jobs expected when all definitions are missing")
}

// TestLoadScheduledPartialUnresolved_ResolvableJobsReturned proves that when some
// timers reference a known definition and some reference an unknown one, LoadScheduled
// returns the resolvable jobs AND the sentinel error.
func TestLoadScheduledPartialUnresolved_ResolvableJobsReturned(t *testing.T) {
	t.Parallel()

	startAt := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)
	mts := kernel.NewMemTimerStore()
	store := runtimetest.MustMemStore(t, kernel.WithTimers(mts))

	def := runtimetest.TimerIntermediateDef() // id="timer-intermediate"
	reg := kernel.NewMapDefinitionRegistry(def)
	cat := action.NewCatalog(map[string]action.Action{"greet": greetAction})

	// Arm two timer instances — both reference the same "timer-intermediate" def.
	for _, id := range []string{"partial-1", "partial-2"} {
		sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
		driver := runtimetest.MustProcessDriver(t, cat, store,
			runtime.WithClock(fc),
			runtime.WithScheduler(sched),
			runtime.WithTimerStore(mts),
			runtime.WithDefinitions(reg),
		)
		_, err := driver.Drive(t.Context(), def, id, nil)
		require.NoError(t, err)
	}

	// Manually inject a stale/phantom timer referencing a nonexistent definition.
	mts.Arm(kernel.ArmedTimer{
		InstanceID: "ghost-instance",
		DefID:      "nonexistent-def",
		DefVersion: 99,
		TimerID:    "ghost-timer",
		NextRun:    startAt.Add(2 * time.Hour),
	})

	// Build fresh driver with ONLY the known definition — ghost timer cannot resolve.
	freshStore := runtimetest.MustMemStore(t, kernel.WithTimers(mts))
	driver2 := runtimetest.MustProcessDriver(t, cat, freshStore,
		runtime.WithClock(fc),
		runtime.WithTimerStore(mts),
		runtime.WithDefinitions(reg), // has "timer-intermediate" but not "nonexistent-def"
	)

	js := runtime.NewJobStore(driver2)
	jobs, err := js.LoadScheduled(t.Context())

	// Sentinel error must be returned.
	require.Error(t, err)
	assert.True(t, errors.Is(err, kernel.ErrUnresolvedTimerDefinitions),
		"expected ErrUnresolvedTimerDefinitions, got: %v", err)

	// The two resolvable timers must still be returned.
	assert.Len(t, jobs, 2, "resolvable jobs must be returned even alongside the sentinel")
}

// TestSchedulerStart_UnresolvedDefinitions_NonFatal proves that scheduling.Scheduler.Start
// returns nil (non-fatal) when LoadScheduled returns ErrUnresolvedTimerDefinitions,
// so unresolved-def timers do not block startup.
func TestSchedulerStart_UnresolvedDefinitions_NonFatal(t *testing.T) {
	t.Parallel()

	startAt := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)
	mts := kernel.NewMemTimerStore()
	store := runtimetest.MustMemStore(t, kernel.WithTimers(mts))

	def := runtimetest.TimerIntermediateDef()
	reg := kernel.NewMapDefinitionRegistry(def)
	cat := action.NewCatalog(map[string]action.Action{"greet": greetAction})

	// Arm a timer so the durable store is non-empty.
	{
		sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
		driver := runtimetest.MustProcessDriver(t, cat, store,
			runtime.WithClock(fc),
			runtime.WithScheduler(sched),
			runtime.WithTimerStore(mts),
			runtime.WithDefinitions(reg),
		)
		_, err := driver.Drive(t.Context(), def, "start-nonfatal-1", nil)
		require.NoError(t, err)
	}

	// Fresh driver WITHOUT the definition — simulates a consumer that hasn't registered defs yet.
	emptyReg := kernel.NewMapDefinitionRegistry() // no defs
	var driver2 *runtime.ProcessDriver
	sched2, err := scheduling.NewScheduler(
		scheduling.WithClock(fc),
		scheduling.WithJobStore(func() kernel.JobStore { return runtime.NewJobStore(driver2) }),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sched2.Close() })

	freshStore := runtimetest.MustMemStore(t, kernel.WithTimers(mts))
	driver2 = runtimetest.MustProcessDriver(t, cat, freshStore,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched2),
		runtime.WithTimerStore(mts),
		runtime.WithDefinitions(emptyReg),
	)

	// THE KEY ASSERTION: Start must return nil even though LoadScheduled returns the sentinel.
	err = sched2.Start(t.Context())
	assert.NoError(t, err, "Start must succeed (non-fatal) when only unresolved-def timers are present")
}
