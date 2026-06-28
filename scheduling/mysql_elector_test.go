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

// TestSchedulerWithMySQLTimerElector proves the public façade plumbs the
// MySQL-backed elector down to gocron in single-leader mode: when this instance
// cannot win leadership (a side connection holds the leader lock) its timers are
// skipped; when uncontended it is the leader and timers fire. Mirrors
// TestSchedulerWithTimerElector for the Postgres elector.
func TestSchedulerWithMySQLTimerElector(t *testing.T) {
	db := database.RunTestMySQL(t)

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
			s, err := scheduling.NewScheduler(
				scheduling.WithSchedulerClock(clk),
				scheduling.WithMySQLTimerElector(db, scheduling.WithElectorKey(leaderKey)))
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

// TestSchedulerMySQLElectorOnLeadershipAcquired proves the façade plumbs the
// on-leadership-acquired hook (Option A, ADR-0072) down to the MySQL elector:
// when this instance wins leadership the registered callback fires.
func TestSchedulerMySQLElectorOnLeadershipAcquired(t *testing.T) {
	db := database.RunTestMySQL(t)
	ctx := t.Context()

	acquired := make(chan struct{}, 1)
	clk := clockwork.NewFakeClock()
	s, err := scheduling.NewScheduler(
		scheduling.WithSchedulerClock(clk),
		scheduling.WithMySQLTimerElector(db,
			scheduling.WithElectorKey("facade-mysql-onacquire"),
			scheduling.WithOnLeadershipAcquired(func(context.Context) { acquired <- struct{}{} })))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// A timer run makes gocron call IsLeader, so the leader wins leadership and
	// the hook fires.
	s.Schedule("timer", clk.Now().Add(time.Second), func() {})
	require.NoError(t, clk.BlockUntilContext(ctx, 1))
	clk.Advance(time.Second)

	select {
	case <-acquired:
	case <-time.After(3 * time.Second):
		t.Fatal("on-leadership-acquired hook must fire through the MySQL façade elector")
	}
}

// TestSchedulerMySQLElectorLockConflict proves the façade rejects requesting
// both distributed modes at once with a clear error — MySQL elector is mutually
// exclusive with WithDistributedTimerLock and with WithTimerElector.
func TestSchedulerMySQLElectorLockConflict(t *testing.T) {
	pool := database.RunTestDatabase(t)
	db := database.RunTestMySQL(t)

	t.Run("mysql elector + distributed lock", func(t *testing.T) {
		clk := clockwork.NewFakeClock()
		_, err := scheduling.NewScheduler(
			scheduling.WithSchedulerClock(clk),
			scheduling.WithDistributedTimerLock(pool),
			scheduling.WithMySQLTimerElector(db),
		)
		require.Error(t, err, "requesting both a distributed lock and a MySQL elector must fail")
	})

	t.Run("mysql elector + postgres elector", func(t *testing.T) {
		clk := clockwork.NewFakeClock()
		_, err := scheduling.NewScheduler(
			scheduling.WithSchedulerClock(clk),
			scheduling.WithTimerElector(pool),
			scheduling.WithMySQLTimerElector(db),
		)
		require.Error(t, err, "requesting both a Postgres elector and a MySQL elector must fail")
	})
}
