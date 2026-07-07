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
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

func TestRehydrateTimersResumesAfterRestart(t *testing.T) {
	startAt := time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)
	mts := kernel.NewMemTimerStore()
	store := runtimetest.MustMemStore(t, kernel.WithTimers(mts))
	def := runtimetest.TimerIntermediateDef()
	reg := kernel.NewMapDefinitionRegistry(def) // auto-indexed by both "DefID" and "DefID:1"

	cat := action.NewMapCatalog(map[string]action.Action{
		"greet": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"greeted": true}, nil
		}),
	})

	// Original process: arm the timer, then it "crashes" — discard runner + scheduler.
	{
		sched := kernel.NewMemScheduler(kernel.WithMemSchedulerClock(fc))
		r := runtimetest.MustRunner(t, cat, store,
			runtime.WithClock(fc),
			runtime.WithScheduler(sched), runtime.WithTimerStore(mts), runtime.WithDefinitions(reg))
		_, err := r.Run(t.Context(), def, "rh-1", nil)
		require.NoError(t, err)
	}

	// New process: fresh runner + fresh scheduler, same store + timer store.
	sched2 := kernel.NewMemScheduler(kernel.WithMemSchedulerClock(fc))
	r2 := runtimetest.MustRunner(t, cat, store,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched2), runtime.WithTimerStore(mts), runtime.WithDefinitions(reg))

	require.NoError(t, r2.RehydrateTimers(t.Context()))

	// Advance + tick the NEW scheduler: the rehydrated timer fires and resumes.
	fc.Advance(time.Hour + time.Second)
	require.NoError(t, sched2.Tick(t.Context()))

	final, _, err := store.Load(t.Context(), "rh-1")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status, "rehydrated timer must resume the instance")
}

func TestRehydrateTimersRequiresWiring(t *testing.T) {
	store := runtimetest.MustMemStore(t)
	r := runtimetest.MustRunner(t, action.NewMapCatalog(nil), store, runtime.WithClock(clockwork.NewFakeClock()))
	err := r.RehydrateTimers(t.Context())
	require.Error(t, err, "RehydrateTimers without scheduler/timer-store/registry must error")
}
