package gocron_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	sched "github.com/zakyalvan/krtlwrkflw/internal/scheduling/gocron"
)

func TestGocronScheduler_FiresAtTime(t *testing.T) {
	fakeClock := clockwork.NewFakeClock()
	s, err := sched.NewGocronScheduler(fakeClock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var wg sync.WaitGroup
	wg.Add(1)
	s.Schedule("t1", fakeClock.Now().Add(5*time.Second), func() { wg.Done() })

	// MANDATORY barrier: wait until gocron armed its timer (1 waiter) before
	// advancing, else Advance can outrun the arm and the timer never fires.
	require.NoError(t, fakeClock.BlockUntilContext(t.Context(), 1))
	fakeClock.Advance(5 * time.Second)
	wg.Wait() // executor goroutine actually ran the task
}

func TestGocronScheduler_Behaviour(t *testing.T) {
	type tc struct {
		name   string
		assert func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock)
	}

	// counter returns an atomically-incrementing fire callback and a reader.
	counter := func() (func(), func() int64) {
		var n atomic.Int64
		return func() { n.Add(1) }, func() int64 { return n.Load() }
	}

	cases := []tc{
		{
			name: "cancel prevents fire",
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				fire, count := counter()
				s.Schedule("c1", clk.Now().Add(5*time.Second), fire)
				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				s.Cancel("c1")
				clk.Advance(10 * time.Second)
				// No barrier to wait on; assert it never fires within a short window.
				require.Never(t, func() bool { return count() > 0 },
					200*time.Millisecond, 10*time.Millisecond)
			},
		},
		{
			name: "replace reschedules and fires once",
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var wg sync.WaitGroup
				wg.Add(1)
				var n atomic.Int64
				fire := func() { n.Add(1); wg.Done() }

				s.Schedule("r1", clk.Now().Add(5*time.Second), func() { t.Error("stale timer fired") })
				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				s.Schedule("r1", clk.Now().Add(10*time.Second), fire) // replace
				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))

				clk.Advance(5 * time.Second)
				require.Never(t, func() bool { return n.Load() > 0 },
					150*time.Millisecond, 10*time.Millisecond) // old T+5 must not fire
				clk.Advance(5 * time.Second)                    // now at T+10
				wg.Wait()
				require.Equal(t, int64(1), n.Load())
			},
		},
		{
			name: "cancel unknown is a no-op",
			assert: func(t *testing.T, s *sched.GocronScheduler, _ *clockwork.FakeClock) {
				require.NotPanics(t, func() { s.Cancel("does-not-exist") })
			},
		},
		{
			name: "callback runs exactly once",
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var wg sync.WaitGroup
				wg.Add(1)
				var n atomic.Int64
				s.Schedule("o1", clk.Now().Add(time.Second), func() { n.Add(1); wg.Done() })
				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				clk.Advance(time.Second)
				wg.Wait()
				require.Never(t, func() bool { return n.Load() > 1 },
					150*time.Millisecond, 10*time.Millisecond)
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk := clockwork.NewFakeClock()
			s, err := sched.NewGocronScheduler(clk)
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })
			c.assert(t, s, clk)
		})
	}
}
