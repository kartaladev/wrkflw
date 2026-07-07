package scheduling_test

import (
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/scheduling"
)

// TestNewScheduler_WithSchedulerClock_NilFallback verifies that supplying
// WithSchedulerClock(nil) (or no clock option at all) does not panic and the
// scheduler constructs successfully, defaulting to a real clock.
func TestNewScheduler_WithSchedulerClock_NilFallback(t *testing.T) {
	type tc struct {
		name string
		opts []scheduling.Option
	}

	cases := []tc{
		{
			name: "no clock option — uses real clock by default",
			opts: nil,
		},
		{
			name: "explicit nil clock — falls back to real clock",
			opts: []scheduling.Option{scheduling.WithSchedulerClock(nil)},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := scheduling.NewScheduler(c.opts...)
			require.NoError(t, err, "construction must not fail")
			require.NotNil(t, s)
			t.Cleanup(func() { _ = s.Close() })
		})
	}
}

// TestNewScheduler_WithSchedulerClock_FakeClockDrivesFiring verifies that
// supplying a fake clock via WithSchedulerClock makes scheduled jobs fire only
// when the fake clock is advanced — demonstrating the option actually controls
// timer scheduling in the façade.
func TestNewScheduler_WithSchedulerClock_FakeClockDrivesFiring(t *testing.T) {
	clk := clockwork.NewFakeClock()

	s, err := scheduling.NewScheduler(scheduling.WithSchedulerClock(clk))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var wg sync.WaitGroup
	wg.Add(1)
	_, err = s.Schedule(t.Context(), "facade-clk-t1", schedule.At(clk.Now().Add(5*time.Second)), func() { wg.Done() })
	require.NoError(t, err)

	// MANDATORY barrier: wait until gocron armed its waiter on the fake clock.
	require.NoError(t, clk.BlockUntilContext(t.Context(), 1))

	// Advance the fake clock past the fire time — the job must fire.
	clk.Advance(5 * time.Second)
	wg.Wait()
}

// TestNewScheduler_WithSchedulerClock_NotFiredWithoutAdvance verifies that a job
// scheduled via WithSchedulerClock(fake) does NOT fire before the fake clock is
// advanced (confirming the fake clock is actually used, not the real clock).
func TestNewScheduler_WithSchedulerClock_NotFiredWithoutAdvance(t *testing.T) {
	clk := clockwork.NewFakeClock()

	s, err := scheduling.NewScheduler(scheduling.WithSchedulerClock(clk))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var fired bool
	_, err = s.Schedule(t.Context(), "facade-clk-t2", schedule.At(clk.Now().Add(5*time.Second)), func() { fired = true })
	require.NoError(t, err)

	// Wait until gocron armed its waiter, then assert the job hasn't fired.
	require.NoError(t, clk.BlockUntilContext(t.Context(), 1))

	// Do NOT advance the clock — the job must not fire within a short window.
	assert.Never(t, func() bool { return fired }, 200*time.Millisecond, 10*time.Millisecond,
		"job must not fire before the fake clock is advanced")
}
