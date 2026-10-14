package runtime_test

import (
	"slices"
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
	"github.com/kartaladev/wrkflw/scheduler"
)

// TestBaselineRehydrateTimersArmsResolvableAndReportsTheRest characterizes the
// restart-recovery scenario "RehydrateTimers re-arms resolvable timers and
// reports the rest" (openspec/specs/restart-recovery): with 2 persisted timers
// whose definition is registered and 1 whose definition is not, a fresh
// driver's RehydrateTimers schedules the 2 resolvable timers and returns an
// error wrapping scheduler.ErrUnresolvedTimerDefinitions.
func TestBaselineRehydrateTimersArmsResolvableAndReportsTheRest(t *testing.T) {
	t.Parallel()

	startAt := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)
	mts := kernel.NewMemTimerStore()
	store := runtimetest.MustMemStore(t, kernel.WithTimers(mts))

	def := runtimetest.TimerIntermediateDef()
	reg := kernel.NewMapDefinitionRegistry(def)
	cat := action.NewCatalog(map[string]action.Action{"greet": greetAction})

	// Two resolvable timers, armed by driving two instances to their park.
	for _, id := range []string{"baseline-rh-1", "baseline-rh-2"} {
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
	// One timer whose definition the registry cannot resolve.
	const ghostTimerID = "baseline-ghost-timer"
	mts.Arm(kernel.ArmedTimer{
		InstanceID: "baseline-ghost-instance",
		DefID:      "nonexistent-def",
		DefVersion: 99,
		TimerID:    ghostTimerID,
		NextRun:    startAt.Add(2 * time.Hour),
	})

	// Fresh process: new scheduler, same timer store, same registry.
	sched2 := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	driver2 := runtimetest.MustProcessDriver(t, cat, runtimetest.MustMemStore(t, kernel.WithTimers(mts)),
		runtime.WithClock(fc),
		runtime.WithScheduler(sched2),
		runtime.WithTimerStore(mts),
		runtime.WithDefinitions(reg),
	)

	err := driver2.RehydrateTimers(t.Context())

	require.ErrorIs(t, err, scheduler.ErrUnresolvedTimerDefinitions)
	scheduled := slices.Collect(sched2.List(t.Context()))
	assert.Len(t, scheduled, 2, "both resolvable timers must be scheduled")
	_, ghostArmed := sched2.Pending(ghostTimerID)
	assert.False(t, ghostArmed, "the unresolvable timer must not be scheduled")
}
