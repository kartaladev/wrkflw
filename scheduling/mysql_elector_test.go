package scheduling_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/persistence"
	"github.com/kartaladev/wrkflw/scheduling"
	mysqlbackend "github.com/kartaladev/wrkflw/scheduling/backend/mysql"
	pgbackend "github.com/kartaladev/wrkflw/scheduling/backend/postgres"
)

// TestSchedulerWithMySQLTimerElector proves the public façade plumbs the
// MySQL-backed backend elector down to gocron in single-leader mode: when this
// instance cannot win leadership (a side connection holds the leader lock) its
// timers are skipped; when uncontended it is the leader and timers fire. Mirrors
// TestSchedulerWithTimerElector for the Postgres elector.
func TestSchedulerWithMySQLTimerElector(t *testing.T) {
	db := dbtest.RunTestMySQL(t)

	type testCase struct {
		name      string
		preHold   bool
		wantFired bool
	}

	cases := []testCase{
		{name: "leader fires", preHold: false, wantFired: true},
		{name: "follower is skipped", preHold: true, wantFired: false},
	}

	const leaderKey = "facade-mysql-leader"

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()

			if tc.preHold {
				// Acquire the same key on a separate dedicated connection to simulate
				// another replica holding leadership. MySQL GET_LOCK is session-scoped
				// so we use *sql.Conn to ensure the lock is held for the test duration.
				holdConn, err := db.Conn(ctx)
				require.NoError(t, err)
				t.Cleanup(func() {
					_, _ = holdConn.ExecContext(context.Background(), `SELECT RELEASE_ALL_LOCKS()`)
					_ = holdConn.Close()
				})
				var result int64
				require.NoError(t, holdConn.QueryRowContext(ctx, `SELECT GET_LOCK(?, 0)`, leaderKey).Scan(&result))
				require.Equal(t, int64(1), result, "pre-hold must succeed")
			}

			clk := clockwork.NewFakeClock()
			elector, err := mysqlbackend.NewElector(ctx, db,
				mysqlbackend.WithElectorKey(leaderKey), mysqlbackend.WithClock(clk))
			require.NoError(t, err)
			s, err := scheduling.NewScheduler(
				scheduling.WithClock(clk),
				scheduling.WithElector(elector))
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })

			fired := make(chan struct{}, 1)
			_, err = s.Schedule(ctx, "timer", schedule.At(clk.Now().Add(time.Second)), func() { fired <- struct{}{} })
			require.NoError(t, err)
			require.NoError(t, clk.BlockUntilContext(ctx, 1))
			clk.Advance(time.Second)

			if tc.wantFired {
				select {
				case <-fired:
				case <-time.After(3 * time.Second):
					t.Fatal("leader timer must fire through the MySQL façade elector")
				}
			} else {
				select {
				case <-fired:
					t.Fatal("follower timer must be skipped by the MySQL façade elector")
				case <-time.After(500 * time.Millisecond):
				}
			}
		})
	}
}

// TestSchedulerMySQLElectorOnLeadershipAcquired proves the MySQL backend elector's
// on-leadership-acquired hook (Option A, ADR-0072) fires through the façade: when
// this instance wins leadership the registered callback fires.
func TestSchedulerMySQLElectorOnLeadershipAcquired(t *testing.T) {
	db := dbtest.RunTestMySQL(t)
	ctx := t.Context()

	acquired := make(chan struct{}, 1)
	clk := clockwork.NewFakeClock()
	elector, err := mysqlbackend.NewElector(ctx, db,
		mysqlbackend.WithElectorKey("facade-mysql-onacquire"),
		mysqlbackend.WithClock(clk),
		mysqlbackend.WithOnLeadershipAcquired(func(context.Context) { acquired <- struct{}{} }))
	require.NoError(t, err)
	s, err := scheduling.NewScheduler(
		scheduling.WithClock(clk),
		scheduling.WithElector(elector))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// A timer run makes gocron call IsLeader, so the leader wins leadership and
	// the hook fires.
	_, err = s.Schedule(ctx, "timer", schedule.At(clk.Now().Add(time.Second)), func() {})
	require.NoError(t, err)
	require.NoError(t, clk.BlockUntilContext(ctx, 1))
	clk.Advance(time.Second)

	select {
	case <-acquired:
	case <-time.After(3 * time.Second):
		t.Fatal("on-leadership-acquired hook must fire through the MySQL façade elector")
	}
}

// TestSchedulerMySQLElectorLockConflict proves the façade rejects requesting both
// distributed modes at once with a clear error — an Elector (MySQL) is mutually
// exclusive with a Locker.
func TestSchedulerMySQLElectorLockConflict(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	db := dbtest.RunTestMySQL(t)
	ctx := t.Context()

	t.Run("mysql elector + distributed lock", func(t *testing.T) {
		locker := persistence.NewPostgresSchedulerLocker(pool)
		elector, err := mysqlbackend.NewElector(ctx, db)
		require.NoError(t, err)
		t.Cleanup(func() { _ = elector.Close() })

		clk := clockwork.NewFakeClock()
		_, err = scheduling.NewScheduler(
			scheduling.WithClock(clk),
			scheduling.WithLocker(locker),
			scheduling.WithElector(elector),
		)
		require.ErrorIs(t, err, scheduling.ErrTimerLockElectorConflict,
			"requesting both a Locker and a MySQL elector must fail")
	})

	t.Run("mysql elector + postgres elector last wins (no conflict)", func(t *testing.T) {
		// Two electors are not a conflict at the façade: the last WithElector wins.
		// This documents that the mutual-exclusion is Locker-vs-Elector, not
		// elector-vs-elector (the DB-specific pairing lived in the old options).
		pgElector, err := pgbackend.NewElector(ctx, pool)
		require.NoError(t, err)
		t.Cleanup(func() { _ = pgElector.Close() })
		myElector, err := mysqlbackend.NewElector(ctx, db)
		require.NoError(t, err)
		t.Cleanup(func() { _ = myElector.Close() })

		clk := clockwork.NewFakeClock()
		s, err := scheduling.NewScheduler(
			scheduling.WithClock(clk),
			scheduling.WithElector(pgElector),
			scheduling.WithElector(myElector),
		)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
	})
}
