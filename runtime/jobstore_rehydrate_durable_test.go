package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/processtest"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/scheduling"
)

// TestSelfRehydrateOnStart proves that a Scheduler wired with WithJobStore
// re-arms all durable timers automatically on Start, without an explicit
// ProcessDriver.RehydrateTimers call. This is the durable e2e test for
// ADR-0102 scheduler self-rehydration (Task 3).
//
// Scenario: a timer is armed against a SQLite store (row persists). The driver
// and scheduler are discarded ("crash"). A fresh scheduling.Scheduler is
// constructed with WithJobStore capturing a fresh driver2. Start fires the
// self-rehydrate path; advancing the fake clock triggers the re-armed timer
// and the instance resumes to completion.
func TestSelfRehydrateOnStart(t *testing.T) {
	t.Parallel()

	db := dbtest.RunTestSQLite(t) // already migrated
	sqlStore, err := store.New(db, dialect.NewSQLite())
	require.NoError(t, err)
	timerStore, err := store.NewTimerStore(db, dialect.NewSQLite())
	require.NoError(t, err)

	def := runtimetest.TimerIntermediateDef()
	reg := kernel.NewMapDefinitionRegistry(def)
	cat := action.NewCatalog(map[string]action.Action{
		"greet": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"greeted": true}, nil
		}),
	})

	startAt := time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	// Phase 1 — arm the timer then "crash": discard driver + scheduler.
	// The wrkflw_timers row persists in the SQL store.
	{
		sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
		driver := runtimetest.MustRunner(t, cat, sqlStore,
			runtime.WithClock(fc),
			runtime.WithScheduler(sched),
			runtime.WithTimerStore(timerStore),
			runtime.WithDefinitions(reg))
		_, err := driver.Drive(t.Context(), def, "rh-self-1", nil)
		require.NoError(t, err)
	}

	// Phase 2 — fresh REAL scheduler + driver2, wired via thunk so the
	// construction cycle is broken (driver2 pointer captured before assignment).
	var driver2 *runtime.ProcessDriver
	sched2, err := scheduling.NewScheduler(
		scheduling.WithClock(fc),
		scheduling.WithJobStore(func() kernel.JobStore { return runtime.NewJobStore(driver2) }),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sched2.Close() })

	driver2 = runtimetest.MustRunner(t, cat, sqlStore,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched2),
		runtime.WithTimerStore(timerStore),
		runtime.WithDefinitions(reg))

	// Start triggers self-rehydration — NO explicit RehydrateTimers call.
	require.NoError(t, sched2.Start(t.Context()))

	// Wait for gocron to arm its internal waiter on the fake clock (exactly
	// one BlockUntil waiter is expected — the rehydrated one-shot timer).
	require.NoError(t, fc.BlockUntilContext(t.Context(), 1))

	// Advance the fake clock well past the original fire instant (startAt+1h).
	fc.Advance(5 * time.Hour)

	// Assert the instance completes.
	require.Eventually(t,
		func() bool {
			st, _, e := sqlStore.Load(t.Context(), "rh-self-1")
			return e == nil && st.Status == engine.StatusCompleted
		},
		2*time.Second, 10*time.Millisecond,
		"self-rehydrated timer must resume instance rh-self-1 to completion")
}
