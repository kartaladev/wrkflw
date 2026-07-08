package scheduling_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/scheduling"
)

// TestSchedulerWithDistributedTimerLock proves the public façade plumbs a
// persistence-backed neutral Locker down to gocron: a timer whose advisory lock is
// already held elsewhere is skipped, while an uncontended timer fires normally.
// The Locker reuses the engine's Postgres advisory-lock SQL via the persistence
// bridge — no lock code lives in the scheduling package (ADR-0102).
func TestSchedulerWithDistributedTimerLock(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	ctx := t.Context()

	// Pre-hold the lock for "held" on a side connection.
	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	t.Cleanup(conn.Release)
	_, err = conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, "held")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, "held")
	})

	locker := persistence.NewPostgresSchedulerLocker(pool)

	clk := clockwork.NewFakeClock()
	s, err := scheduling.NewScheduler(scheduling.WithClock(clk), scheduling.WithLocker(locker))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	heldFired := make(chan struct{}, 1)
	freeFired := make(chan struct{}, 1)
	_, err = s.Schedule(ctx, "held", schedule.At(clk.Now().Add(time.Second)), func() { heldFired <- struct{}{} })
	require.NoError(t, err)
	_, err = s.Schedule(ctx, "free", schedule.At(clk.Now().Add(time.Second)), func() { freeFired <- struct{}{} })
	require.NoError(t, err)
	require.NoError(t, clk.BlockUntilContext(ctx, 2))
	clk.Advance(time.Second)

	// The uncontended timer must fire.
	select {
	case <-freeFired:
	case <-time.After(3 * time.Second):
		t.Fatal("uncontended timer must fire through the façade locker")
	}
	// The contended timer must be skipped.
	select {
	case <-heldFired:
		t.Fatal("contended timer must be skipped by the façade locker")
	case <-time.After(300 * time.Millisecond):
	}
}

// TestSchedulerWithMySQLDistributedTimerLock proves the same per-timer exclusion
// through the MySQL persistence-backed neutral Locker (NewMySQLSchedulerLocker):
// a timer whose GET_LOCK is already held elsewhere is skipped while an
// uncontended timer fires. Mirrors the Postgres locker test.
func TestSchedulerWithMySQLDistributedTimerLock(t *testing.T) {
	db := dbtest.RunTestMySQL(t)
	ctx := t.Context()

	// Pre-hold the GET_LOCK for "held" on a side session; keep it for the test.
	// The bridge SHA-256 hashes keys to fit MySQL's 64-char GET_LOCK limit, so we
	// must hold the SAME hashed name the store uses. Reuse the store's locker to
	// acquire "held" on a side session deterministically.
	sideLocker := persistence.NewMySQLSchedulerLocker(db)
	sideLock, err := sideLocker.Lock(ctx, "held")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sideLock.Unlock(context.Background()) })

	locker := persistence.NewMySQLSchedulerLocker(db)

	clk := clockwork.NewFakeClock()
	s, err := scheduling.NewScheduler(scheduling.WithClock(clk), scheduling.WithLocker(locker))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	heldFired := make(chan struct{}, 1)
	freeFired := make(chan struct{}, 1)
	_, err = s.Schedule(ctx, "held", schedule.At(clk.Now().Add(time.Second)), func() { heldFired <- struct{}{} })
	require.NoError(t, err)
	_, err = s.Schedule(ctx, "free", schedule.At(clk.Now().Add(time.Second)), func() { freeFired <- struct{}{} })
	require.NoError(t, err)
	require.NoError(t, clk.BlockUntilContext(ctx, 2))
	clk.Advance(time.Second)

	select {
	case <-freeFired:
	case <-time.After(3 * time.Second):
		t.Fatal("uncontended timer must fire through the MySQL façade locker")
	}
	select {
	case <-heldFired:
		t.Fatal("contended timer must be skipped by the MySQL façade locker")
	case <-time.After(300 * time.Millisecond):
	}
}
