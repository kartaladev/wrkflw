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

// TestSchedulerWithTimerElector proves the public façade plumbs the Postgres-backed
// elector down to gocron in single-leader mode: when this instance cannot win
// leadership (a side connection holds the leader lock) its timers are skipped;
// when uncontended it is the leader and timers fire. Cases share one pool and run
// sequentially, so they are not parallel.
func TestSchedulerWithTimerElector(t *testing.T) {
	pool := database.RunTestDatabase(t)

	type testCase struct {
		name      string
		preHold   bool
		wantFired bool
	}

	cases := []testCase{
		{name: "leader fires", preHold: false, wantFired: true},
		{name: "follower is skipped", preHold: true, wantFired: false},
	}

	const leaderKey = "facade-leader"

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()

			if tc.preHold {
				conn, err := pool.Acquire(ctx)
				require.NoError(t, err)
				t.Cleanup(conn.Release)
				_, err = conn.Exec(ctx,
					`SELECT pg_advisory_lock(hashtextextended($1, 0))`, leaderKey)
				require.NoError(t, err)
				t.Cleanup(func() {
					_, _ = conn.Exec(context.Background(),
						`SELECT pg_advisory_unlock(hashtextextended($1, 0))`, leaderKey)
				})
			}

			clk := clockwork.NewFakeClock()
			s, err := scheduling.NewScheduler(
				scheduling.WithSchedulerClock(clk),
				scheduling.WithTimerElector(pool, scheduling.WithElectorKey(leaderKey)))
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })

			fired := make(chan struct{}, 1)
			s.Schedule("timer", clk.Now().Add(time.Second), func() { fired <- struct{}{} })
			require.NoError(t, clk.BlockUntilContext(ctx, 1))
			clk.Advance(time.Second)

			if tc.wantFired {
				select {
				case <-fired:
				case <-time.After(3 * time.Second):
					t.Fatal("leader timer must fire through the façade elector")
				}
			} else {
				select {
				case <-fired:
					t.Fatal("follower timer must be skipped by the façade elector")
				case <-time.After(500 * time.Millisecond):
				}
			}
		})
	}
}

// TestSchedulerElectorHeartbeatStepsDown proves the façade threads its scheduler
// clock and a configurable heartbeat interval into the leader elector (ADR-0061):
// after the leader's dedicated backend is severed out-of-band, advancing the shared
// fake clock past one heartbeat interval makes the elector step down — closing the
// split-brain window through the public façade.
func TestSchedulerElectorHeartbeatStepsDown(t *testing.T) {
	pool := database.RunTestDatabase(t)
	ctx := t.Context()

	const leaderKey = "facade-heartbeat"
	clk := clockwork.NewFakeClock()
	s, err := scheduling.NewScheduler(
		scheduling.WithSchedulerClock(clk),
		scheduling.WithTimerElector(pool,
			scheduling.WithElectorKey(leaderKey),
			scheduling.WithElectorHeartbeatInterval(time.Second)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Fire a timer so the leader actually wins leadership (gocron calls IsLeader on
	// the job run), starting the heartbeat.
	fired := make(chan struct{}, 1)
	s.Schedule("timer", clk.Now().Add(time.Second), func() { fired <- struct{}{} })
	require.NoError(t, clk.BlockUntilContext(ctx, 1))
	clk.Advance(time.Second)
	select {
	case <-fired:
	case <-time.After(3 * time.Second):
		t.Fatal("leader timer must fire before severing the connection")
	}

	// Find and terminate the elector's dedicated backend out-of-band; Postgres
	// auto-releases its leader lock. The leader still believes it leads until the
	// heartbeat re-validates.
	pid := scheduling.ElectorBackendPID(s)
	require.NotZero(t, pid)
	side, err := pool.Acquire(ctx)
	require.NoError(t, err)
	t.Cleanup(side.Release)
	_, err = side.Exec(ctx, `SELECT pg_terminate_backend($1)`, pid)
	require.NoError(t, err)

	// Advance past one heartbeat interval; the heartbeat must catch the dead conn so
	// the elector steps down (its sticky leadership is revoked).
	require.NoError(t, clk.BlockUntilContext(ctx, 1))
	clk.Advance(time.Second)

	require.Eventually(t, func() bool {
		return !scheduling.SchedulerIsLeader(ctx, s)
	}, 3*time.Second, 10*time.Millisecond,
		"the façade elector must step down after its connection is severed")
}

// TestSchedulerElectorOnLeadershipAcquired proves the façade plumbs the
// on-leadership-acquired hook (Option A, ADR-0072) down to the elector: when this
// instance wins leadership the registered callback fires. Wiring it to
// Runner.RehydrateTimers re-arms persisted timers on a new leader after failover.
func TestSchedulerElectorOnLeadershipAcquired(t *testing.T) {
	pool := database.RunTestDatabase(t)
	ctx := t.Context()

	acquired := make(chan struct{}, 1)
	clk := clockwork.NewFakeClock()
	s, err := scheduling.NewScheduler(
		scheduling.WithSchedulerClock(clk),
		scheduling.WithTimerElector(pool,
			scheduling.WithElectorKey("facade-onacquire"),
			scheduling.WithOnLeadershipAcquired(func(context.Context) { acquired <- struct{}{} })))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// A timer run makes gocron call IsLeader, so the leader wins leadership and the
	// hook fires (in production this is wired to Runner.RehydrateTimers).
	s.Schedule("timer", clk.Now().Add(time.Second), func() {})
	require.NoError(t, clk.BlockUntilContext(ctx, 1))
	clk.Advance(time.Second)

	select {
	case <-acquired:
	case <-time.After(3 * time.Second):
		t.Fatal("on-leadership-acquired hook must fire through the façade elector")
	}
}

// TestSchedulerLockAndElectorConflict proves the façade rejects requesting both
// distributed modes at once with a clear error.
func TestSchedulerLockAndElectorConflict(t *testing.T) {
	pool := database.RunTestDatabase(t)

	clk := clockwork.NewFakeClock()
	_, err := scheduling.NewScheduler(
		scheduling.WithSchedulerClock(clk),
		scheduling.WithDistributedTimerLock(pool),
		scheduling.WithTimerElector(pool),
	)
	require.Error(t, err, "requesting both a distributed lock and an elector must fail")
}
