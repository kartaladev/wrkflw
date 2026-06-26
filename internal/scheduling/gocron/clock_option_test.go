package gocron_test

import (
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sched "github.com/zakyalvan/krtlwrkflw/internal/scheduling/gocron"
)

// TestGocronScheduler_WithClock_NilFallback verifies that supplying WithClock(nil)
// (or no clock option at all) does not panic and the scheduler constructs
// successfully, defaulting to a real clock.
func TestGocronScheduler_WithClock_NilFallback(t *testing.T) {
	type tc struct {
		name string
		opts []sched.Option
	}

	cases := []tc{
		{
			name: "no clock option — uses real clock by default",
			opts: nil,
		},
		{
			name: "explicit nil clock — falls back to real clock",
			opts: []sched.Option{sched.WithClock(nil)},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := sched.NewGocronScheduler(c.opts...)
			require.NoError(t, err, "construction must not fail")
			require.NotNil(t, s)
			t.Cleanup(func() { _ = s.Close() })
		})
	}
}

// TestGocronScheduler_WithClock_FakeClockDrivesFiring verifies that supplying a
// fake clock via WithClock causes a scheduled job to fire only when the fake clock
// is advanced — demonstrating the clock option actually controls timer scheduling.
func TestGocronScheduler_WithClock_FakeClockDrivesFiring(t *testing.T) {
	clk := clockwork.NewFakeClock()

	s, err := sched.NewGocronScheduler(sched.WithClock(clk))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var wg sync.WaitGroup
	wg.Add(1)
	s.Schedule("clk-opt-t1", clk.Now().Add(5*time.Second), func() { wg.Done() })

	// MANDATORY barrier: wait until gocron armed its waiter on the fake clock.
	require.NoError(t, clk.BlockUntilContext(t.Context(), 1))

	// Advance the fake clock past the fire time — the job must fire.
	clk.Advance(5 * time.Second)
	wg.Wait()
}

// TestGocronScheduler_WithClock_NotFiredWithoutAdvance verifies that a job
// scheduled via WithClock(fake) does NOT fire before the fake clock is advanced
// (confirming that the fake clock is actually used by gocron, not the real clock).
func TestGocronScheduler_WithClock_NotFiredWithoutAdvance(t *testing.T) {
	clk := clockwork.NewFakeClock()

	s, err := sched.NewGocronScheduler(sched.WithClock(clk))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var fired bool
	s.Schedule("clk-opt-t2", clk.Now().Add(5*time.Second), func() { fired = true })

	// Wait until gocron armed its waiter, then assert the job hasn't fired.
	require.NoError(t, clk.BlockUntilContext(t.Context(), 1))

	// Do NOT advance the clock — the job must not fire within a short window.
	assert.Never(t, func() bool { return fired }, 200*time.Millisecond, 10*time.Millisecond,
		"job must not fire before the fake clock is advanced")
}
