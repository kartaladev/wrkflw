package myelector_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/dbtest"
	sched "github.com/kartaladev/wrkflw/scheduler/internal/gocron/myelector"
)

// TestMySQLElectorHeartbeatKeepsLeadershipAlive proves the positive path of
// the heartbeat (mirrors TestPostgresElectorHeartbeatStepsDownOnConnLoss's
// setup, without severing the connection): while the elector's dedicated
// connection stays healthy, each heartbeat tick's mysqlRevalidate ping
// succeeds and leadership survives — the heartbeat must never spuriously
// step a healthy leader down.
//
// The discriminating signal is the on-leadership-acquired callback's
// invocation count, not a bare IsLeader poll: IsLeader is self-healing (a
// step-down immediately re-acquires on the next ask, since nothing else
// contends for the lock), so polling IsLeader alone cannot distinguish
// "never stepped down" from "stepped down and instantly won it back". A
// spurious step-down would fire the callback a second time; this test
// asserts it fires exactly once across the whole heartbeat window.
func TestMySQLElectorHeartbeatKeepsLeadershipAlive(t *testing.T) {
	db := dbtest.RunTestMySQL(t)
	ctx := t.Context()

	var acquisitions atomic.Int32
	clk := clockwork.NewFakeClock()
	elector, err := sched.NewMySQLElector(ctx, db,
		sched.WithMySQLElectorClock(clk),
		sched.WithMySQLHeartbeatInterval(time.Second),
		sched.WithMySQLOnLeadershipAcquired(func(context.Context) { acquisitions.Add(1) }),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = elector.Close() })

	// Become leader: the heartbeat starts on first acquisition.
	require.NoError(t, elector.IsLeader(ctx), "first instance must be elected leader")
	require.Eventually(t, func() bool { return acquisitions.Load() == 1 }, eventuallyBudget, 10*time.Millisecond,
		"on-leadership-acquired callback must fire exactly once for the initial win")

	// Wait for the heartbeat goroutine to be parked on the ticker, then fire
	// several ticks. The dedicated connection is healthy, so every
	// mysqlRevalidate ping succeeds.
	//
	// ⚠ THIS SITE HAS NO POSITIVE LIVENESS PRECONDITION (the only one of the
	// package family left without one). A heartbeat goroutine
	// that died on tick 1 also never spuriously steps down, so the
	// require.Never below passes on a corpse. The obvious barrier does NOT
	// close that hole and was MEASURED not to: with `return` injected after
	// the first mysqlRevalidate, a per-tick clk.BlockUntilContext(ctx, 1)
	// still PASSED, because clockwork's fakeTicker.expire re-arms itself from
	// inside Advance (clockwork@v0.5.0 ticker.go:60-67) — the waiter count
	// proves a TICKER exists, never that a goroutine is consuming it.
	//
	// The precondition that would work is making the heartbeat ACT: sever the
	// dedicated connection after this window and require a step-down (what the
	// pgelector sibling, TestPostgresElectorHeartbeatStepsDownOnConnLoss, does
	// via pg_terminate_backend). MySQL has no equivalent in this package yet —
	// killing the elector's connection needs its CONNECTION_ID(), which is not
	// reachable from outside MySQLElector. Tracked, not faked.
	//
	// The loop below is still preferable to three back-to-back Advances: a
	// fakeTicker drops a tick whose channel is already full, so a burst of
	// Advances can deliver ONE tick while the comment claims three.
	for range 3 {
		requireTickerArmed(t, clk, ctx)
		clk.Advance(time.Second)
	}

	// Still leader after the ticks.
	require.Eventually(t, func() bool { return elector.IsLeader(ctx) == nil }, eventuallyBudget, 10*time.Millisecond,
		"leadership must survive heartbeat ticks while the connection is healthy")

	// The acquisition count must NOT have grown: a spurious step-down would
	// have immediately re-acquired (nothing else contends for the lock) and
	// fired the callback again.
	require.Never(t, func() bool { return acquisitions.Load() > 1 }, 300*time.Millisecond, 10*time.Millisecond,
		"heartbeat must not spuriously step down (and re-acquire) a healthy leader")
}

// requireTickerArmed blocks until the heartbeat's fake-clock ticker is
// registered, and FAILS BY NAME if it never is. The bounded context is the
// point: a bare clk.BlockUntilContext(t.Context(), 1) that never unblocks
// waits for the test itself to end, so the run dies as an unnamed "panic: test
// timed out" carrying no assertion message at all.
//
// ⚠ Named for exactly what it proves. It is NOT a liveness check on the
// heartbeat goroutine — measured, see the ⚠ block in the test above.
// clockwork's BlockUntilContext(ctx, n) returns as soon as len(waiters) >= n
// (clockwork@v0.5.0 clockwork.go:255-258, a LOWER bound), and a fakeTicker
// re-arms itself from inside Advance whether or not anything is listening.
func requireTickerArmed(t *testing.T, clk *clockwork.FakeClock, ctx context.Context) {
	t.Helper()
	bctx, cancel := context.WithTimeout(ctx, eventuallyBudget)
	defer cancel()
	require.NoError(t, clk.BlockUntilContext(bctx, 1),
		"the heartbeat's ticker must be armed before advancing, else the Advance outruns the arm and the tick is never delivered")
}
