package postgres_test

import (
	"context"
	"testing"
	"time"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/database"
	"github.com/zakyalvan/krtlwrkflw/engine"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// TestPostgresTimerRehydrationResumesAfterRestart proves that after a simulated
// process restart (sched1 discarded, fresh store2+sched2+r2 created), calling
// r2.RehydrateTimers re-arms the persisted timer on sched2 so that advancing
// the clock and calling sched2.Tick drives the instance to completion — without
// any manual r2.Deliver(TimerFired) call.
func TestPostgresTimerRehydrationResumesAfterRestart(t *testing.T) {
	t.Parallel()

	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	startAt := time.Date(2026, 6, 22, 16, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	ran := false
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"finish": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			ran = true
			return map[string]any{"done": true}, nil
		}),
	})

	// timerResumeDef is defined in resume_test.go (same package postgres_test):
	// start → wait1h (IntermediateCatchEvent, "1h") → finish (ServiceTask, action "finish") → end.
	def := timerResumeDef()
	ts := pg.NewTimerStore(pool)
	reg := runtime.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		def.ID + ":1": def,
	})

	// Original "process": Run with store1+sched1 so the timer arm persists to Postgres.
	// sched1 is discarded at end of block, simulating a crash/restart.
	store1 := pg.NewStore(pool)
	{
		sched1 := runtime.NewMemScheduler(fc)
		r1 := runtime.NewRunner(cat, fc, store1,
			runtime.WithScheduler(sched1),
			runtime.WithTimerStore(ts),
			runtime.WithDefinitions(reg),
		)
		parked, err := r1.Run(t.Context(), def, "pgrh-1", nil)
		require.NoError(t, err)
		require.Equal(t, engine.StatusRunning, parked.Status, "instance must be running (parked at timer)")
		require.False(t, ran, "finish action must not run while timer is pending")
	}

	// Assert the timer was persisted to Postgres before the "restart".
	armed, err := ts.ListArmed(t.Context())
	require.NoError(t, err)
	require.Len(t, armed, 1, "timer must be persisted to Postgres after Run parks the instance")

	// "Restart": brand-new Store + Scheduler + Runner reading the same Postgres DB.
	// sched1 and store1's in-memory state are gone — only Postgres rows survive.
	store2 := pg.NewStore(pool)
	sched2 := runtime.NewMemScheduler(fc)
	r2 := runtime.NewRunner(cat, fc, store2,
		runtime.WithScheduler(sched2),
		runtime.WithTimerStore(ts),
		runtime.WithDefinitions(reg),
	)

	// RehydrateTimers re-arms the persisted timer on sched2 — no manual Deliver call.
	require.NoError(t, r2.RehydrateTimers(t.Context()))

	// Advance the fake clock past the 1-hour timer and tick sched2.
	// The rehydrated job fires, delivers TimerFired internally, and resumes the instance.
	fc.Advance(time.Hour + time.Second)
	require.NoError(t, sched2.Tick(t.Context()))

	// Instance must now be completed.
	final, _, err := store2.Load(t.Context(), "pgrh-1")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status, "instance must reach StatusCompleted after rehydrated timer fires")
	assert.True(t, ran, "finish action must have run after the timer fired")

	// The fired timer must be cleared from Postgres (committed atomically with state).
	armed, err = ts.ListArmed(t.Context())
	require.NoError(t, err)
	assert.Empty(t, armed, "fired timer must be cleared from Postgres after completion")
}
