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
// ADR-0061's heartbeat (mirrors TestPostgresElectorHeartbeatStepsDownOnConnLoss's
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
	require.Eventually(t, func() bool { return acquisitions.Load() == 1 }, time.Second, 10*time.Millisecond,
		"on-leadership-acquired callback must fire exactly once for the initial win")

	// Wait for the heartbeat goroutine to be parked on the ticker, then fire
	// several ticks. The dedicated connection is healthy, so every
	// mysqlRevalidate ping succeeds.
	require.NoError(t, clk.BlockUntilContext(ctx, 1))
	for range 3 {
		clk.Advance(time.Second)
	}

	// Still leader after the ticks.
	require.Eventually(t, func() bool { return elector.IsLeader(ctx) == nil }, time.Second, 10*time.Millisecond,
		"leadership must survive heartbeat ticks while the connection is healthy")

	// The acquisition count must NOT have grown: a spurious step-down would
	// have immediately re-acquired (nothing else contends for the lock) and
	// fired the callback again.
	require.Never(t, func() bool { return acquisitions.Load() > 1 }, 300*time.Millisecond, 10*time.Millisecond,
		"heartbeat must not spuriously step down (and re-acquire) a healthy leader")
}
