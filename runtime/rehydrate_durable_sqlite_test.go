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
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
	"github.com/zakyalvan/krtlwrkflw/processtest"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// TestRehydrateTimersDurableSQLite proves the durability regression closer: a
// SQL-backed (SQLite) one-shot AfterDuration timer, armed then "crashed", is
// re-armed by a FRESH driver over the SAME store and fires at its ORIGINAL
// absolute instant — not restart-time + duration. Without the persisted
// next_run + trigger descriptor the fresh driver would either not re-arm at all
// (Plan-2 regression) or restart the delay from now.
func TestRehydrateTimersDurableSQLite(t *testing.T) {
	db := dbtest.RunTestSQLite(t) // already migrated
	sqlStore, err := store.New(db, dialect.NewSQLite())
	require.NoError(t, err)
	timerStore, err := store.NewTimerStore(db, dialect.NewSQLite())
	require.NoError(t, err)

	// The timer catch resolves AfterExpr("1h") → AfterDuration(1h): a one-shot
	// whose descriptor carries only a duration, so faithful rehydration MUST
	// rely on the persisted next_run for the absolute fire instant.
	def := runtimetest.TimerIntermediateDef()
	reg := kernel.NewMapDefinitionRegistry(def)
	cat := action.NewMapCatalog(map[string]action.Action{
		"greet": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"greeted": true}, nil
		}),
	})

	startAt := time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	// Original process: arm the one-shot timer, then "crash" (discard runner +
	// scheduler). The wrkflw_timers row persists in the SQLite store.
	{
		sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
		driver := runtimetest.MustRunner(t, cat, sqlStore,
			runtime.WithClock(fc),
			runtime.WithScheduler(sched), runtime.WithTimerStore(timerStore), runtime.WithDefinitions(reg))
		_, err := driver.Drive(t.Context(), def, "rh-sqlite-1", nil)
		require.NoError(t, err)
	}

	// Simulate a long downtime: advance the clock PAST the original fire time.
	// If rehydration restarted the delay from "now", the timer would fire at
	// now+1h (much later) instead of the original startAt+1h.
	fc.Advance(5 * time.Hour) // now = startAt + 5h, well past startAt + 1h

	// Fresh process: new runner + new scheduler over the SAME store.
	sched2 := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	r2 := runtimetest.MustRunner(t, cat, sqlStore,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched2), runtime.WithTimerStore(timerStore), runtime.WithDefinitions(reg))

	require.NoError(t, r2.RehydrateTimers(t.Context()))

	// The rehydrated one-shot's next run must be the ORIGINAL absolute instant
	// (startAt + 1h), which is already in the past — so a single Tick fires it.
	next, ok := sched2.NextRun("rh-sqlite-1-tm1")
	require.True(t, ok, "rehydrated timer must be pending on the fresh scheduler")
	wantFire := startAt.Add(time.Hour)
	assert.True(t, next.Equal(wantFire),
		"AfterDuration one-shot must fire at ORIGINAL instant %v, not restart+duration; got %v", wantFire, next)

	require.NoError(t, sched2.Tick(t.Context()))

	final, _, err := sqlStore.Load(t.Context(), "rh-sqlite-1")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status, "rehydrated durable timer must resume the instance to completion")
}
