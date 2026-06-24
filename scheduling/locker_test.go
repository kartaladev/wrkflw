package scheduling_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/scheduling"
)

// TestSchedulerWithDistributedTimerLock proves the public façade plumbs the
// Postgres-backed locker down to gocron: a timer whose advisory lock is already
// held elsewhere is skipped, while an uncontended timer fires normally.
func TestSchedulerWithDistributedTimerLock(t *testing.T) {
	pool := database.RunTestDatabase(t)
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

	clk := clockwork.NewFakeClock()
	s, err := scheduling.NewScheduler(clk, scheduling.WithDistributedTimerLock(pool))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	heldFired := make(chan struct{}, 1)
	freeFired := make(chan struct{}, 1)
	s.Schedule("held", clk.Now().Add(time.Second), func() { heldFired <- struct{}{} })
	s.Schedule("free", clk.Now().Add(time.Second), func() { freeFired <- struct{}{} })
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
