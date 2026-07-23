package pgelector_test

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/dbtest"
	sched "github.com/kartaladev/wrkflw/scheduler/internal/gocron/pgelector"
)

// TestPostgresElectorHeartbeatStepsDownOnConnLoss proves ADR-0061: a leader whose
// dedicated connection is severed out-of-band (here via pg_terminate_backend on a
// side connection, which auto-releases its advisory lock server-side) STEPS DOWN
// on the next heartbeat tick — its sticky IsLeader flips back to ErrNotLeader.
//
// Without the heartbeat (the ADR-0059 split-brain caveat) IsLeader would wrongly
// keep returning nil from the in-memory flag, leaving a two-leader window. The
// heartbeat is what catches the silent loss.
func TestPostgresElectorHeartbeatStepsDownOnConnLoss(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	ctx := t.Context()

	clk := clockwork.NewFakeClock()
	elector, err := sched.NewPostgresElector(ctx, pool,
		sched.WithElectorClock(clk),
		sched.WithHeartbeatInterval(time.Second))
	require.NoError(t, err)
	t.Cleanup(func() { _ = elector.Close() })

	// Become leader: the heartbeat starts on first acquisition.
	require.NoError(t, elector.IsLeader(ctx), "first instance must be elected leader")
	// Sticky: still leader, no round-trip.
	require.NoError(t, elector.IsLeader(ctx), "leader must stay leader on repeat ask")

	// Sever the elector's dedicated backend out-of-band: find its PID and terminate
	// it from a side connection. Postgres auto-releases the leader advisory lock when
	// the backend dies, so the elector silently no longer holds leadership.
	pid := elector.BackendPID()
	require.NotZero(t, pid)

	side, err := pool.Acquire(ctx)
	require.NoError(t, err)
	t.Cleanup(side.Release)
	_, err = side.Exec(ctx, `SELECT pg_terminate_backend($1)`, pid)
	require.NoError(t, err)

	// Wait for the heartbeat goroutine to be parked on the ticker, then fire one tick.
	require.NoError(t, clk.BlockUntilContext(ctx, 1))
	clk.Advance(time.Second)

	// After the tick caught the dead connection, IsLeader must step down.
	require.Eventually(t, func() bool {
		return elector.IsLeader(ctx) != nil
	}, 3*time.Second, 10*time.Millisecond,
		"heartbeat must detect the severed connection and step the elector down")
}

// TestPostgresElectorHeartbeatNoLeakAfterClose proves the heartbeat goroutine's
// lifecycle is bounded by Close: a leader that started its heartbeat and is then
// Closed leaves no goroutine behind (the package's goleak VerifyTestMain enforces
// this across the suite; this test exercises the start+stop path explicitly).
func TestPostgresElectorHeartbeatNoLeakAfterClose(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	ctx := t.Context()

	clk := clockwork.NewFakeClock()
	elector, err := sched.NewPostgresElector(ctx, pool,
		sched.WithElectorClock(clk),
		sched.WithHeartbeatInterval(time.Second))
	require.NoError(t, err)

	// Acquire leadership so the heartbeat goroutine starts, then ensure Close stops it.
	require.NoError(t, elector.IsLeader(ctx))
	require.NoError(t, clk.BlockUntilContext(ctx, 1))
	require.NoError(t, elector.Close())
	require.NoError(t, elector.Close(), "second Close must be a no-op")
}
