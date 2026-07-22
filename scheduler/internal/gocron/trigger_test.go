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

// asJobFunc adapts an old-style zero-argument fire callback (used throughout
// TestGocronNativeTriggers, which predates the Job-shaped ScheduleJob entry
// point) into the func(context.Context) error shape ScheduleJob accepts.
func asJobFunc(fn func()) func(context.Context) error {
	return func(context.Context) error { fn(); return nil }
}

func TestGocronNativeTriggers(t *testing.T) {
	t.Run("cron fires at least twice using NextRun-driven advances", func(t *testing.T) {
		clk := clockwork.NewFakeClockAt(time.Date(2026, 1, 1, 8, 59, 0, 0, time.UTC))
		s, err := sched.NewGocronScheduler(sched.WithClock(clk))
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		ctx := t.Context()

		var fired atomic.Int32
		nr, err := s.ScheduleJob(ctx, "cron1", sched.Cron(`0 9 * * *`), asJobFunc(func() { fired.Add(1) }), false)
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
		nr, err := s.ScheduleJob(ctx, "oneshot1", sched.After(5*time.Second), asJobFunc(func() { fired.Add(1) }), false)
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
		nr, err := s.ScheduleJob(ctx, "every1", sched.Every(time.Hour), asJobFunc(func() { fired.Add(1) }), false)
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
		nr, err := s.ScheduleJob(ctx, "daily1",
			sched.Daily(1, sched.ClockTime{Hour: 9, Minute: 0, Second: 0}),
			asJobFunc(func() { fired.Add(1) }), false)
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
		nr, err := s.ScheduleJob(ctx, "weekly1",
			sched.Weekly(1, []time.Weekday{time.Monday, time.Friday}, sched.ClockTime{Hour: 9}),
			asJobFunc(func() { fired.Add(1) }), false)
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
		nr, err := s.ScheduleJob(ctx, "weekly-guard",
			sched.Weekly(1, nil, sched.ClockTime{Hour: 9}),
			asJobFunc(func() {}), false)
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
		nr, err := s.ScheduleJob(ctx, "monthly1",
			sched.Monthly(1, []int{1, 15}, sched.ClockTime{Hour: 9}),
			asJobFunc(func() { fired.Add(1) }), false)
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
		nr, err := s.ScheduleJob(ctx, "monthly-guard",
			sched.Monthly(1, nil, sched.ClockTime{Hour: 9}),
			asJobFunc(func() {}), false)
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
		nr, err := s.ScheduleJob(ctx, "rand1",
			sched.EveryRandom(time.Minute, 2*time.Minute),
			asJobFunc(func() { fired.Add(1) }), false)
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

// TestTriggerDef_IsZero exercises TriggerDef.IsZero directly: unlike the
// other accessors (Duration/AbsTime/Random/CronExpr/Calendar), which
// jobDefinition calls internally for every constructed kind above,
// IsZero has no caller inside this package — its only consumer, ScheduleJob's
// zero-trigger rejection, goes through jobDefinition's default switch case
// instead (see the "zero TriggerDef" case below), not through IsZero itself.
func TestTriggerDef_IsZero(t *testing.T) {
	type tc struct {
		name   string
		trig   sched.TriggerDef
		assert func(t *testing.T, isZero bool)
	}

	cases := []tc{
		{
			name: "zero-value TriggerDef reports IsZero true",
			trig: sched.TriggerDef{},
			assert: func(t *testing.T, isZero bool) {
				assert.True(t, isZero)
			},
		},
		{
			name: "a constructed TriggerDef reports IsZero false",
			trig: sched.After(time.Second),
			assert: func(t *testing.T, isZero bool) {
				assert.False(t, isZero)
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.assert(t, c.trig.IsZero())
		})
	}
}

// TestGocronScheduleJobTriggers exercises jobDefinition's TriggerDef-based
// mapping through ScheduleJob directly, entering via the TriggerDef
// constructors (At/After/Every/EveryRandom/Cron/Daily/Weekly/Monthly) rather
// than through TestGocronNativeTriggers' higher-level ScheduleJob calls
// above. Each subtest here mirrors its TestGocronNativeTriggers analogue
// one-for-one — same fake-clock-advance + NextRun + Eventually technique —
// so jobDefinition's individual branches are each exercised directly and in
// isolation, in addition to the end-to-end coverage above.
func TestGocronScheduleJobTriggers(t *testing.T) {
	type tc struct {
		name   string
		start  time.Time
		assert func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock)
	}

	cases := []tc{
		{
			name:  "At (future) fires at the absolute time then disarms",
			start: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var fired atomic.Int32
				fireAt := clk.Now().Add(5 * time.Second)
				next, err := s.ScheduleJob(t.Context(), "at-future", sched.At(fireAt),
					func(context.Context) error { fired.Add(1); return nil }, false)
				require.NoError(t, err)
				require.False(t, next.IsZero(), "ScheduleJob must return the live first-run time")

				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				clk.Advance(6 * time.Second)
				require.Eventually(t, func() bool { return fired.Load() >= 1 }, time.Second, 5*time.Millisecond)

				require.Eventually(t, func() bool {
					_, ok := s.NextRun("at-future")
					return !ok
				}, time.Second, 5*time.Millisecond, "one-shot At must be disarmed after firing")
			},
		},
		{
			// Beyond the default 5-minute time-skew tolerance: jobDefinition's
			// past-due branch (!at.After(now)) must still fire immediately
			// (never-drop invariant) rather than reject or hang waiting for a
			// clock advance. WARN-log-on-breach itself is ScheduleJob's own
			// concern (already covered by TestGocronScheduler_TimeSkew); this
			// case targets jobDefinition's mapping only.
			name:  "At (past-due) fires immediately (time-skew branch)",
			start: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var fired atomic.Int32
				fireAt := clk.Now().Add(-10 * time.Minute)
				next, err := s.ScheduleJob(t.Context(), "at-pastdue", sched.At(fireAt),
					func(context.Context) error { fired.Add(1); return nil }, false)
				require.NoError(t, err)
				require.False(t, next.IsZero())

				require.Eventually(t, func() bool { return fired.Load() >= 1 }, time.Second, 5*time.Millisecond,
					"past-due At must fire immediately without a clock advance")
			},
		},
		{
			name:  "After fires after the fixed duration",
			start: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var fired atomic.Int32
				next, err := s.ScheduleJob(t.Context(), "after-job", sched.After(5*time.Second),
					func(context.Context) error { fired.Add(1); return nil }, false)
				require.NoError(t, err)
				require.False(t, next.IsZero())

				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				clk.Advance(6 * time.Second)
				require.Eventually(t, func() bool { return fired.Load() >= 1 }, time.Second, 5*time.Millisecond)
			},
		},
		{
			name:  "Every recurs on a fixed interval",
			start: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var fired atomic.Int32
				next, err := s.ScheduleJob(t.Context(), "every-job", sched.Every(time.Hour),
					func(context.Context) error { fired.Add(1); return nil }, false)
				require.NoError(t, err)
				require.False(t, next.IsZero())

				for i := 0; i < 2; i++ {
					n, ok := s.NextRun("every-job")
					require.True(t, ok)
					clk.Advance(n.Sub(clk.Now()) + time.Millisecond)
					require.Eventually(t, func() bool { return fired.Load() >= int32(i+1) }, time.Second, 5*time.Millisecond)
				}
			},
		},
		{
			name:  "EveryRandom recurs within bounds",
			start: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var fired atomic.Int32
				next, err := s.ScheduleJob(t.Context(), "rand-job", sched.EveryRandom(time.Minute, 2*time.Minute),
					func(context.Context) error { fired.Add(1); return nil }, false)
				require.NoError(t, err)
				require.False(t, next.IsZero())

				for i := 0; i < 2; i++ {
					n, ok := s.NextRun("rand-job")
					require.True(t, ok)
					clk.Advance(n.Sub(clk.Now()) + time.Millisecond)
					require.Eventually(t, func() bool { return fired.Load() >= int32(i+1) }, time.Second, 5*time.Millisecond)
				}
			},
		},
		{
			name:  "Cron recurs per the expression",
			start: time.Date(2026, 1, 1, 8, 59, 0, 0, time.UTC),
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var fired atomic.Int32
				next, err := s.ScheduleJob(t.Context(), "cron-job", sched.Cron(`0 9 * * *`),
					func(context.Context) error { fired.Add(1); return nil }, false)
				require.NoError(t, err)
				require.False(t, next.IsZero())

				for i := 0; i < 2; i++ {
					n, ok := s.NextRun("cron-job")
					require.True(t, ok)
					clk.Advance(n.Sub(clk.Now()) + time.Millisecond)
					require.Eventually(t, func() bool { return fired.Load() >= int32(i+1) }, time.Second, 5*time.Millisecond)
				}
			},
		},
		{
			name:  "Daily fires at the scheduled time",
			start: time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC),
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var fired atomic.Int32
				next, err := s.ScheduleJob(t.Context(), "daily-job",
					sched.Daily(1, sched.ClockTime{Hour: 9, Minute: 0, Second: 0}),
					func(context.Context) error { fired.Add(1); return nil }, false)
				require.NoError(t, err)
				require.False(t, next.IsZero())

				n, ok := s.NextRun("daily-job")
				require.True(t, ok)
				clk.Advance(n.Sub(clk.Now()) + time.Millisecond)
				require.Eventually(t, func() bool { return fired.Load() >= 1 }, time.Second, 5*time.Millisecond)
			},
		},
		{
			name:  "Weekly fires on both weekdays and recurs",
			start: time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC), // Thursday
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var fired atomic.Int32
				next, err := s.ScheduleJob(t.Context(), "weekly-job",
					sched.Weekly(1, []time.Weekday{time.Monday, time.Friday}, sched.ClockTime{Hour: 9}),
					func(context.Context) error { fired.Add(1); return nil }, false)
				require.NoError(t, err)
				require.False(t, next.IsZero())

				for i := 0; i < 2; i++ {
					n, ok := s.NextRun("weekly-job")
					require.True(t, ok)
					clk.Advance(n.Sub(clk.Now()) + time.Millisecond)
					require.Eventually(t, func() bool { return fired.Load() >= int32(i+1) }, time.Second, 5*time.Millisecond)
				}
			},
		},
		{
			// nil weekdays → jobDefinition's guard supplies time.Sunday.
			name:  "Weekly (empty-days guard) defaults to Sunday",
			start: time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC),
			assert: func(t *testing.T, s *sched.GocronScheduler, _ *clockwork.FakeClock) {
				next, err := s.ScheduleJob(t.Context(), "weekly-guard-job",
					sched.Weekly(1, nil, sched.ClockTime{Hour: 9}),
					func(context.Context) error { return nil }, false)
				require.NoError(t, err)
				require.False(t, next.IsZero(), "guard must supply a default weekday (Sunday) and return a valid NextRun")

				n, ok := s.NextRun("weekly-guard-job")
				require.True(t, ok)
				assert.Equal(t, time.Sunday, n.Weekday(), "empty-days guard must default to Sunday")
			},
		},
		{
			name:  "Monthly fires on specified days and recurs",
			start: time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC),
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
				var fired atomic.Int32
				next, err := s.ScheduleJob(t.Context(), "monthly-job",
					sched.Monthly(1, []int{1, 15}, sched.ClockTime{Hour: 9}),
					func(context.Context) error { fired.Add(1); return nil }, false)
				require.NoError(t, err)
				require.False(t, next.IsZero())

				for i := 0; i < 2; i++ {
					n, ok := s.NextRun("monthly-job")
					require.True(t, ok)
					clk.Advance(n.Sub(clk.Now()) + time.Millisecond)
					require.Eventually(t, func() bool { return fired.Load() >= int32(i+1) }, time.Second, 5*time.Millisecond)
				}
			},
		},
		{
			// nil days → jobDefinition's guard supplies day 1. Start on the
			// 2nd so the next fire lands on the 1st of the following month.
			name:  "Monthly (empty-days guard) defaults to day 1",
			start: time.Date(2026, 1, 2, 8, 0, 0, 0, time.UTC),
			assert: func(t *testing.T, s *sched.GocronScheduler, _ *clockwork.FakeClock) {
				next, err := s.ScheduleJob(t.Context(), "monthly-guard-job",
					sched.Monthly(1, nil, sched.ClockTime{Hour: 9}),
					func(context.Context) error { return nil }, false)
				require.NoError(t, err)
				require.False(t, next.IsZero(), "guard must supply default day 1 and return a valid NextRun")

				n, ok := s.NextRun("monthly-guard-job")
				require.True(t, ok)
				assert.Equal(t, 1, n.Day(), "empty-days guard must default to day 1")
			},
		},
		{
			// The zero TriggerDef (triggerDefUnset) hits jobDefinition's
			// default branch directly, wrapping ErrUnsupportedTrigger.
			name:  "zero TriggerDef is rejected wrapping ErrUnsupportedTrigger",
			start: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
			assert: func(t *testing.T, s *sched.GocronScheduler, _ *clockwork.FakeClock) {
				next, err := s.ScheduleJob(t.Context(), "zero-trigger-job", sched.TriggerDef{},
					func(context.Context) error { return nil }, false)
				require.Error(t, err)
				require.ErrorIs(t, err, sched.ErrUnsupportedTrigger)
				assert.True(t, next.IsZero())
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk := clockwork.NewFakeClockAt(c.start)
			s, err := sched.NewGocronScheduler(sched.WithClock(clk))
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })
			c.assert(t, s, clk)
		})
	}
}
