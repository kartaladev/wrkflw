package gocron_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sched "github.com/kartaladev/wrkflw/scheduler/internal/gocron"
)

// TestGocronScheduler_ScheduleJob covers the Job-shaped scheduling entry
// point (ScheduleJob): a zero-parameter `func(context.Context) error` task
// registered against a TriggerDef, upsert-by-id semantics, singleton overrun
// protection, and the invalid-trigger error path. See TestGocronScheduler_
// Behaviour in scheduler_test.go for the identical per-case assert-closure
// shape this mirrors.
func TestGocronScheduler_ScheduleJob(t *testing.T) {
	type tc struct {
		name   string
		assert func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock)
	}

	cases := []tc{
		{
			name: "one-shot fires exactly once with a live, non-nil ctx",
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var fired atomic.Int32
				// Snapshot ctx's liveness synchronously, from inside the task,
				// before it returns. gocron cancels a completed one-shot job's
				// ctx asynchronously shortly after the task function returns
				// (its #925 fix defers that cancellation past task completion,
				// but does not delay it further) — inspecting the ctx object
				// itself from the test goroutine after receiving it would race
				// against that cleanup, so the task records what it observed
				// at fire time instead.
				nonNilCh := make(chan bool, 1)
				liveCh := make(chan bool, 1)
				task := func(ctx context.Context) error {
					fired.Add(1)
					nonNilCh <- ctx != nil
					liveCh <- ctx != nil && ctx.Err() == nil
					return nil
				}

				next, err := s.ScheduleJob(t.Context(), "job-one-shot", sched.After(5*time.Second), task, false)
				require.NoError(t, err)
				require.False(t, next.IsZero(), "ScheduleJob must return the live first-run time")

				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				clk.Advance(6 * time.Second)

				require.Eventually(t, func() bool { return fired.Load() >= 1 }, time.Second, 5*time.Millisecond)

				select {
				case nonNil := <-nonNilCh:
					require.True(t, nonNil, "task must receive a non-nil ctx from gocron")
				default:
					t.Fatal("expected the fired task to have captured a ctx")
				}
				assert.True(t, <-liveCh, "ctx must not already be done at fire time")

				require.Never(t, func() bool { return fired.Load() > 1 }, 150*time.Millisecond, 10*time.Millisecond,
					"one-shot must fire exactly once")
			},
		},
		{
			name: "upsert-by-id replaces the prior registration; only the second fires",
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var firstFired, secondFired atomic.Int32
				first := func(context.Context) error { firstFired.Add(1); return nil }
				second := func(context.Context) error { secondFired.Add(1); return nil }

				_, err := s.ScheduleJob(t.Context(), "job-upsert", sched.At(clk.Now().Add(5*time.Second)), first, false)
				require.NoError(t, err)
				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))

				_, err = s.ScheduleJob(t.Context(), "job-upsert", sched.At(clk.Now().Add(10*time.Second)), second, false)
				require.NoError(t, err)
				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))

				clk.Advance(5 * time.Second) // old T+5 fire time — must NOT fire (replaced)
				require.Never(t, func() bool { return firstFired.Load() > 0 }, 150*time.Millisecond, 10*time.Millisecond,
					"the replaced registration must never fire")

				clk.Advance(5 * time.Second) // now at T+10 — the replacement fires
				require.Eventually(t, func() bool { return secondFired.Load() >= 1 }, time.Second, 5*time.Millisecond)

				assert.EqualValues(t, 0, firstFired.Load())
				assert.EqualValues(t, 1, secondFired.Load())
			},
		},
		{
			name: "singleton=true recurring job never overlaps a slow run",
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var running, maxConcurrent atomic.Int32
				gate := make(chan struct{})

				task := func(context.Context) error {
					n := running.Add(1)
					defer running.Add(-1)
					for {
						cur := maxConcurrent.Load()
						if n <= cur || maxConcurrent.CompareAndSwap(cur, n) {
							break
						}
					}
					<-gate
					return nil
				}

				_, err := s.ScheduleJob(t.Context(), "job-singleton", sched.Every(time.Second), task, true)
				require.NoError(t, err)

				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				clk.Advance(time.Second) // first due instant — starts the run, blocks on gate

				require.Eventually(t, func() bool { return running.Load() >= 1 }, time.Second, 5*time.Millisecond)

				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				clk.Advance(time.Second) // second due instant while the first run is still blocked

				require.Never(t, func() bool { return running.Load() > 1 }, 200*time.Millisecond, 10*time.Millisecond,
					"singleton mode must never run two fires concurrently")

				close(gate) // release the blocked run
				require.Eventually(t, func() bool { return running.Load() == 0 }, time.Second, 5*time.Millisecond)

				assert.LessOrEqual(t, maxConcurrent.Load(), int32(1))
			},
		},
		{
			// Regression for a review Minor: WithSingletonMode used to be
			// appended even for one-shot jobs, which is meaningless (a
			// one-shot already runs at most once via WithLimitedRuns(1) —
			// there is nothing for it to overlap). ScheduleJob now guards the
			// option with singleton && !oneShot. This case only proves the
			// combination is still accepted without error and still fires
			// exactly once; it does not distinguish "guard applied" from
			// "gocron silently tolerates the redundant option" — see the
			// package doc note on this test for that caveat.
			name: "singleton=true is a no-op for one-shot triggers",
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var fired atomic.Int32
				task := func(context.Context) error { fired.Add(1); return nil }

				next, err := s.ScheduleJob(t.Context(), "job-singleton-oneshot", sched.After(5*time.Second), task, true)
				require.NoError(t, err, "singleton=true combined with a one-shot trigger must not error")
				require.False(t, next.IsZero())

				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				clk.Advance(6 * time.Second)

				require.Eventually(t, func() bool { return fired.Load() >= 1 }, time.Second, 5*time.Millisecond)
				require.Never(t, func() bool { return fired.Load() > 1 }, 150*time.Millisecond, 10*time.Millisecond,
					"one-shot must still fire exactly once regardless of singleton")
			},
		},
		{
			name: "zero TriggerDef is rejected wrapping the engine's unsupported-trigger sentinel",
			assert: func(t *testing.T, s *sched.GocronScheduler, _ *clockwork.FakeClock) {
				next, err := s.ScheduleJob(t.Context(), "job-invalid", sched.TriggerDef{}, func(context.Context) error { return nil }, false)
				require.Error(t, err)
				require.ErrorIs(t, err, sched.ErrUnsupportedTrigger)
				assert.True(t, next.IsZero())
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk := clockwork.NewFakeClock()
			s, err := sched.NewGocronScheduler(sched.WithClock(clk))
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })
			c.assert(t, s, clk)
		})
	}
}
