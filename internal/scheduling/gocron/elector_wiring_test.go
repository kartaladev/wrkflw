package gocron_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	sched "github.com/zakyalvan/krtlwrkflw/internal/scheduling/gocron"
)

// TestGocronSchedulerElectorGatesFire proves the end-to-end wiring: when a
// scheduler is built WithElector, a fired timer runs its callback only if this
// instance is the elected leader. A leader instance fires; a follower (whose
// elector cannot win the single leader lock because a side connection holds it)
// skips every fire. Cases share one pool and run sequentially, so they are not
// parallel.
func TestGocronSchedulerElectorGatesFire(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)

	type testCase struct {
		name      string
		preHold   bool // pre-acquire the leader lock so the scheduler's elector loses
		wantFired bool
	}

	cases := []testCase{
		{name: "leader fires", preHold: false, wantFired: true},
		{name: "follower is skipped", preHold: true, wantFired: false},
	}

	const leaderKey = "wiring-leader"

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()

			if tc.preHold {
				// Hold the leader lock on a dedicated connection for the whole case,
				// so the scheduler's elector can never win leadership.
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

			elector, err := sched.NewPostgresElector(ctx, pool, sched.WithElectorKey(leaderKey))
			require.NoError(t, err)
			t.Cleanup(func() { _ = elector.Close() })

			clk := clockwork.NewFakeClock()
			s, err := sched.NewGocronScheduler(sched.WithClock(clk), sched.WithElector(elector))
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
					t.Fatal("leader timer must fire")
				}
			} else {
				select {
				case <-fired:
					t.Fatal("follower timer must be skipped by the elector")
				case <-time.After(500 * time.Millisecond):
				}
			}
		})
	}
}

// TestGocronSchedulerLockerElectorMutuallyExclusive proves the two distributed
// modes cannot be combined: configuring both a Locker and an Elector is a
// construction error (ErrLockerElectorConflict), not silent precedence.
func TestGocronSchedulerLockerElectorMutuallyExclusive(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	ctx := t.Context()

	elector, err := sched.NewPostgresElector(ctx, pool, sched.WithElectorKey("conflict"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = elector.Close() })

	clk := clockwork.NewFakeClock()
	_, err = sched.NewGocronScheduler(
		sched.WithClock(clk),
		sched.WithLocker(sched.NewPostgresLocker(pool)),
		sched.WithElector(elector),
	)
	require.ErrorIs(t, err, sched.ErrLockerElectorConflict,
		"setting both a locker and an elector must be rejected")
}
