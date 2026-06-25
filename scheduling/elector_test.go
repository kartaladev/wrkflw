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
			s, err := scheduling.NewScheduler(clk,
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

// TestSchedulerLockAndElectorConflict proves the façade rejects requesting both
// distributed modes at once with a clear error.
func TestSchedulerLockAndElectorConflict(t *testing.T) {
	pool := database.RunTestDatabase(t)

	clk := clockwork.NewFakeClock()
	_, err := scheduling.NewScheduler(clk,
		scheduling.WithDistributedTimerLock(pool),
		scheduling.WithTimerElector(pool),
	)
	require.Error(t, err, "requesting both a distributed lock and an elector must fail")
}
