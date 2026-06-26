package gocron_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	sched "github.com/zakyalvan/krtlwrkflw/internal/scheduling/gocron"
)

// TestGocronSchedulerLockerGatesFire proves the end-to-end wiring: when a
// scheduler is built WithLocker, a fired timer runs its callback only if the
// per-timer advisory lock (key = timerID) is obtainable. Cases share one pool and
// run sequentially (each holds a pooled connection), so they are not parallel.
func TestGocronSchedulerLockerGatesFire(t *testing.T) {
	pool := database.RunTestDatabase(t)

	type testCase struct {
		name      string
		timerID   string
		preHold   bool // pre-acquire the timer's advisory lock on a side connection
		wantFired bool
	}

	cases := []testCase{
		{name: "uncontended fires", timerID: "free-timer", preHold: false, wantFired: true},
		{name: "contended is skipped", timerID: "held-timer", preHold: true, wantFired: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()

			if tc.preHold {
				// Hold the timer's advisory lock on a dedicated connection for the
				// whole case, so the scheduler's locker can never obtain it.
				conn, err := pool.Acquire(ctx)
				require.NoError(t, err)
				t.Cleanup(conn.Release)
				_, err = conn.Exec(ctx,
					`SELECT pg_advisory_lock(hashtextextended($1, 0))`, tc.timerID)
				require.NoError(t, err)
				t.Cleanup(func() {
					_, _ = conn.Exec(context.Background(),
						`SELECT pg_advisory_unlock(hashtextextended($1, 0))`, tc.timerID)
				})
			}

			clk := clockwork.NewFakeClock()
			s, err := sched.NewGocronScheduler(sched.WithClock(clk), sched.WithLocker(sched.NewPostgresLocker(pool)))
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })

			fired := make(chan struct{}, 1)
			s.Schedule(tc.timerID, clk.Now().Add(time.Second), func() { fired <- struct{}{} })
			require.NoError(t, clk.BlockUntilContext(ctx, 1))
			clk.Advance(time.Second)

			if tc.wantFired {
				select {
				case <-fired:
				case <-time.After(3 * time.Second):
					t.Fatal("uncontended timer must fire")
				}
			} else {
				select {
				case <-fired:
					t.Fatal("contended timer must be skipped by the locker")
				case <-time.After(500 * time.Millisecond):
				}
			}
		})
	}
}
