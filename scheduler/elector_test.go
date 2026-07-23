package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/persistence"
	"github.com/kartaladev/wrkflw/scheduler"
	pgbackend "github.com/kartaladev/wrkflw/scheduler/backend/postgres"
)

// TestSchedulerWithTimerElector proves the public façade plumbs the Postgres-backed
// backend elector down to gocron in single-leader mode: when this instance cannot
// win leadership (a side connection holds the leader lock) its timers are skipped;
// when uncontended it is the leader and timers fire. Cases share one pool and run
// sequentially, so they are not parallel.
func TestSchedulerWithTimerElector(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)

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
			elector, err := pgbackend.NewElector(ctx, pool,
				pgbackend.WithElectorKey(leaderKey), pgbackend.WithClock(clk))
			require.NoError(t, err)
			s, err := scheduler.NewScheduler(
				scheduler.WithClock(clk),
				scheduler.WithElector(elector))
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })

			fired := make(chan struct{}, 1)
			_, err = s.Schedule(ctx, mustJob(t, "timer", surfaceKind,
				scheduler.At(clk.Now().Add(time.Second)), func() { fired <- struct{}{} }))
			require.NoError(t, err)
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
// clock and a configurable heartbeat interval into the backend leader elector
// (ADR-0061): after the leader's dedicated backend is severed out-of-band,
// advancing the shared fake clock past one heartbeat interval makes the elector
// step down — closing the split-brain window through the public façade.
func TestSchedulerElectorHeartbeatStepsDown(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	ctx := t.Context()

	const leaderKey = "facade-heartbeat"
	clk := clockwork.NewFakeClock()
	elector, err := pgbackend.NewElector(ctx, pool,
		pgbackend.WithElectorKey(leaderKey),
		pgbackend.WithClock(clk),
		pgbackend.WithHeartbeatInterval(time.Second))
	require.NoError(t, err)
	s, err := scheduler.NewScheduler(
		scheduler.WithClock(clk),
		scheduler.WithElector(elector))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Fire a timer so the leader actually wins leadership (gocron calls IsLeader on
	// the job run), starting the heartbeat.
	fired := make(chan struct{}, 1)
	_, err = s.Schedule(ctx, mustJob(t, "timer", surfaceKind,
		scheduler.At(clk.Now().Add(time.Second)), func() { fired <- struct{}{} }))
	require.NoError(t, err)
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
	pid := scheduler.ElectorBackendPID(s)
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
		return !scheduler.SchedulerIsLeader(ctx, s)
	}, 3*time.Second, 10*time.Millisecond,
		"the façade elector must step down after its connection is severed")
}

// TestSchedulerElectorOnLeadershipAcquired proves the backend elector's
// on-leadership-acquired hook (Option A, ADR-0072) fires through the façade: when
// this instance wins leadership the registered callback fires. Wiring it to
// ProcessDriver.RehydrateTimers re-arms persisted timers on a new leader after
// failover.
func TestSchedulerElectorOnLeadershipAcquired(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	ctx := t.Context()

	acquired := make(chan struct{}, 1)
	clk := clockwork.NewFakeClock()
	elector, err := pgbackend.NewElector(ctx, pool,
		pgbackend.WithElectorKey("facade-onacquire"),
		pgbackend.WithClock(clk),
		pgbackend.WithOnLeadershipAcquired(func(context.Context) { acquired <- struct{}{} }))
	require.NoError(t, err)
	s, err := scheduler.NewScheduler(
		scheduler.WithClock(clk),
		scheduler.WithElector(elector))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// A timer run makes gocron call IsLeader, so the leader wins leadership and the
	// hook fires (in production this is wired to ProcessDriver.RehydrateTimers).
	_, err = s.Schedule(ctx, mustJob(t, "timer", surfaceKind,
		scheduler.At(clk.Now().Add(time.Second)), nil))
	require.NoError(t, err)
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
	pool := dbtest.RunTestDatabase(t)
	ctx := t.Context()

	locker := persistence.NewPostgresSchedulerLocker(pool)

	elector, err := pgbackend.NewElector(ctx, pool)
	require.NoError(t, err)
	t.Cleanup(func() { _ = elector.Close() })

	clk := clockwork.NewFakeClock()
	_, err = scheduler.NewScheduler(
		scheduler.WithClock(clk),
		scheduler.WithLocker(locker),
		scheduler.WithElector(elector),
	)
	require.ErrorIs(t, err, scheduler.ErrTimerLockElectorConflict,
		"requesting both a Locker and an Elector must fail")
}
