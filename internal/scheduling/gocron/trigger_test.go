package gocron_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	sched "github.com/zakyalvan/krtlwrkflw/internal/scheduling/gocron"
)

func TestGocronNativeTriggers(t *testing.T) {
	t.Run("cron fires at least twice using NextRun-driven advances", func(t *testing.T) {
		clk := clockwork.NewFakeClockAt(time.Date(2026, 1, 1, 8, 59, 0, 0, time.UTC))
		s, err := sched.NewGocronScheduler(sched.WithClock(clk))
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		ctx := t.Context()

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

	t.Run("Weekly (normal) fires on both weekdays and recurs", func(t *testing.T) {
		clk := clockwork.NewFakeClockAt(time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)) // Thursday
		s, err := sched.NewGocronScheduler(sched.WithClock(clk))
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		ctx := t.Context()

		var fired atomic.Int32
		nr, err := s.Schedule(ctx, "weekly1",
			schedule.Weekly(1, []time.Weekday{time.Monday, time.Friday}, schedule.ClockTime{Hour: 9}),
			func() { fired.Add(1) })
		require.NoError(t, err)
		require.False(t, nr.IsZero(), "NextRun must be returned for Weekly trigger")

		for i := 0; i < 2; i++ {
			next, ok := s.NextRun("weekly1")
			require.True(t, ok)
			clk.Advance(next.Sub(clk.Now()) + time.Millisecond)
			require.Eventually(t, func() bool { return fired.Load() >= int32(i+1) }, time.Second, 5*time.Millisecond)
		}
	})

	t.Run("Weekly (empty-weekdays guard) defaults to Sunday and does not panic", func(t *testing.T) {
		clk := clockwork.NewFakeClockAt(time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC))
		s, err := sched.NewGocronScheduler(sched.WithClock(clk))
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		ctx := t.Context()

		// nil weekdays → guard supplies time.Sunday (the default)
		nr, err := s.Schedule(ctx, "weekly-guard",
			schedule.Weekly(1, nil, schedule.ClockTime{Hour: 9}),
			func() {})
		require.NoError(t, err)
		require.False(t, nr.IsZero(), "guard must supply a default weekday (Sunday) and return a valid NextRun")

		next, ok := s.NextRun("weekly-guard")
		require.True(t, ok)
		assert.Equal(t, time.Sunday, next.Weekday(), "default weekday guard must schedule on Sunday")
	})

	t.Run("Monthly (normal) fires on specified days and recurs", func(t *testing.T) {
		clk := clockwork.NewFakeClockAt(time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC))
		s, err := sched.NewGocronScheduler(sched.WithClock(clk))
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		ctx := t.Context()

		var fired atomic.Int32
		nr, err := s.Schedule(ctx, "monthly1",
			schedule.Monthly(1, []int{1, 15}, schedule.ClockTime{Hour: 9}),
			func() { fired.Add(1) })
		require.NoError(t, err)
		require.False(t, nr.IsZero(), "NextRun must be returned for Monthly trigger")

		for i := 0; i < 2; i++ {
			next, ok := s.NextRun("monthly1")
			require.True(t, ok)
			clk.Advance(next.Sub(clk.Now()) + time.Millisecond)
			require.Eventually(t, func() bool { return fired.Load() >= int32(i+1) }, time.Second, 5*time.Millisecond)
		}
	})

	t.Run("Monthly (empty-days guard) defaults to day 1 and does not panic", func(t *testing.T) {
		clk := clockwork.NewFakeClockAt(time.Date(2026, 1, 2, 8, 0, 0, 0, time.UTC)) // start on 2nd so next fire is 1st of next month
		s, err := sched.NewGocronScheduler(sched.WithClock(clk))
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		ctx := t.Context()

		// nil days → guard supplies 1 (the default day-of-month)
		nr, err := s.Schedule(ctx, "monthly-guard",
			schedule.Monthly(1, nil, schedule.ClockTime{Hour: 9}),
			func() {})
		require.NoError(t, err)
		require.False(t, nr.IsZero(), "guard must supply default day 1 and return a valid NextRun")

		next, ok := s.NextRun("monthly-guard")
		require.True(t, ok)
		assert.Equal(t, 1, next.Day(), "default day-of-month guard must schedule on day 1")
	})

	t.Run("DurationRand fires at least twice (recurring)", func(t *testing.T) {
		clk := clockwork.NewFakeClockAt(time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))
		s, err := sched.NewGocronScheduler(sched.WithClock(clk))
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		ctx := t.Context()

		var fired atomic.Int32
		nr, err := s.Schedule(ctx, "rand1",
			schedule.EveryRandom(time.Minute, 2*time.Minute),
			func() { fired.Add(1) })
		require.NoError(t, err)
		require.False(t, nr.IsZero(), "NextRun must be returned for DurationRand trigger")

		for i := 0; i < 2; i++ {
			next, ok := s.NextRun("rand1")
			require.True(t, ok)
			clk.Advance(next.Sub(clk.Now()) + time.Millisecond)
			require.Eventually(t, func() bool { return fired.Load() >= int32(i+1) }, time.Second, 5*time.Millisecond)
		}
	})
}
