package gocron_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sched "github.com/zakyalvan/krtlwrkflw/internal/scheduling/gocron"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

func TestGocronNativeTriggers(t *testing.T) {
	t.Run("cron fires at least twice using NextRun-driven advances", func(t *testing.T) {
		clk := clockwork.NewFakeClockAt(time.Date(2026, 1, 1, 8, 59, 0, 0, time.UTC))
		s, err := sched.NewGocronScheduler(sched.WithClock(clk))
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		ctx := t.Context()
		_ = ctx

		var fired atomic.Int32
		nr, err := s.Schedule(ctx, "cron1", schedule.Cron(`0 9 * * *`), func() { fired.Add(1) })
		require.NoError(t, err)
		require.False(t, nr.IsZero(), "NextRun must be returned")

		for i := 0; i < 2; i++ {
			next, ok := s.NextRun("cron1")
			require.True(t, ok)
			clk.Advance(next.Sub(clk.Now()) + time.Millisecond)
			require.Eventually(t, func() bool { return fired.Load() >= int32(i+1) }, time.Second, 5*time.Millisecond)
		}
	})

	t.Run("one-shot fires once then disarms", func(t *testing.T) {
		clk := clockwork.NewFakeClockAt(time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))
		s, err := sched.NewGocronScheduler(sched.WithClock(clk))
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		ctx := t.Context()

		var fired atomic.Int32
		nr, err := s.Schedule(ctx, "oneshot1", schedule.AfterDuration(5*time.Second), func() { fired.Add(1) })
		require.NoError(t, err)
		require.False(t, nr.IsZero(), "NextRun must be returned for one-shot")

		// Wait until gocron has armed the timer before advancing.
		require.NoError(t, clk.BlockUntilContext(ctx, 1))
		clk.Advance(6 * time.Second)

		// Wait for it to fire.
		require.Eventually(t, func() bool { return fired.Load() >= 1 }, time.Second, 5*time.Millisecond)

		// After firing, the one-shot should be removed from NextRun.
		require.Eventually(t, func() bool {
			_, ok := s.NextRun("oneshot1")
			return !ok
		}, time.Second, 5*time.Millisecond, "one-shot must be disarmed after firing")

		assert.EqualValues(t, 1, fired.Load(), "must fire exactly once")
	})

	t.Run("Every (Duration) triggers recurring fires", func(t *testing.T) {
		clk := clockwork.NewFakeClockAt(time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))
		s, err := sched.NewGocronScheduler(sched.WithClock(clk))
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		ctx := t.Context()

		var fired atomic.Int32
		nr, err := s.Schedule(ctx, "every1", schedule.Every(time.Hour), func() { fired.Add(1) })
		require.NoError(t, err)
		require.False(t, nr.IsZero(), "NextRun must be returned for Duration trigger")

		for i := 0; i < 2; i++ {
			next, ok := s.NextRun("every1")
			require.True(t, ok)
			clk.Advance(next.Sub(clk.Now()) + time.Millisecond)
			require.Eventually(t, func() bool { return fired.Load() >= int32(i+1) }, time.Second, 5*time.Millisecond)
		}
	})

	t.Run("Daily trigger fires at scheduled time", func(t *testing.T) {
		clk := clockwork.NewFakeClockAt(time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC))
		s, err := sched.NewGocronScheduler(sched.WithClock(clk))
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		ctx := t.Context()

		var fired atomic.Int32
		nr, err := s.Schedule(ctx, "daily1",
			schedule.Daily(1, schedule.ClockTime{Hour: 9, Minute: 0, Second: 0}),
			func() { fired.Add(1) })
		require.NoError(t, err)
		require.False(t, nr.IsZero(), "NextRun must be returned for Daily trigger")

		next, ok := s.NextRun("daily1")
		require.True(t, ok)
		clk.Advance(next.Sub(clk.Now()) + time.Millisecond)
		require.Eventually(t, func() bool { return fired.Load() >= 1 }, time.Second, 5*time.Millisecond)
	})
}
