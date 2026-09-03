package scheduler_test

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/scheduler"
)

// TestNativeScheduler_WithLocation proves that the façade's WithLocation
// option threads through to the internal gocron engine's location-resolved
// NextRun, as surfaced by Scheduled. It mirrors
// TestNativeSchedulerCalendarTriggers's setup (no explicit Start — the first
// Schedule auto-starts) but goes through scheduler.NewScheduler +
// scheduler.WithLocation instead of exercising the internal package directly.
func TestNativeScheduler_WithLocation(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	plusThree := time.FixedZone("plusThree", 3*60*60)

	cases := []struct {
		name   string
		opts   []scheduler.Option
		assert func(t *testing.T, got time.Time)
	}{
		{
			name: "default pins UTC",
			opts: nil,
			assert: func(t *testing.T, got time.Time) {
				assert.True(t, got.Equal(time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)),
					"want 09:00 UTC, got %s", got)
			},
		},
		{
			name: "WithLocation(+3) resolves at-time in +3",
			opts: []scheduler.Option{scheduler.WithLocation(plusThree)},
			assert: func(t *testing.T, got time.Time) {
				// 09:00 at UTC+3 == 06:00 UTC.
				assert.True(t, got.Equal(time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC)),
					"want 06:00 UTC, got %s", got)
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk := clockwork.NewFakeClockAt(start)
			opts := append([]scheduler.Option{scheduler.WithClock(clk)}, c.opts...)
			s, err := scheduler.NewScheduler(opts...)
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })

			job := mustJob(t, "daily-9am", surfaceKind,
				scheduler.Daily(1, scheduler.ClockTime{Hour: 9}), nil)
			_, err = s.Schedule(t.Context(), job)
			require.NoError(t, err)

			// Scheduled(ctx, id) re-fetches gocron's LIVE NextRun, which
			// respects WithLocation — so the +3 case reads the loc-resolved
			// (correct) instant, i.e. 06:00 UTC. Schedule()'s own return value
			// now resolves in the same location too — see
			// TestNativeScheduler_ScheduleReturnMatchesLocation.
			sj, err := s.Scheduled(t.Context(), "daily-9am")
			require.NoError(t, err)
			c.assert(t, sj.NextRun().UTC())
		})
	}
}

// TestNativeScheduler_LocationMethod proves that Location() reports the
// resolved effective timezone: time.UTC by default, or the WithLocation value
// when one is configured.
func TestNativeScheduler_LocationMethod(t *testing.T) {
	plusThree := time.FixedZone("plusThree", 3*60*60)

	cases := []struct {
		name   string
		opts   []scheduler.Option
		assert func(t *testing.T, got *time.Location)
	}{
		{name: "default is UTC", opts: nil, assert: func(t *testing.T, got *time.Location) {
			assert.Equal(t, time.UTC, got)
		}},
		{name: "WithLocation reflected", opts: []scheduler.Option{scheduler.WithLocation(plusThree)}, assert: func(t *testing.T, got *time.Location) {
			assert.Equal(t, plusThree, got)
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := scheduler.NewScheduler(c.opts...)
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })

			c.assert(t, s.Location())
		})
	}
}

// TestNativeScheduler_ScheduleReturnMatchesLocation proves the core
// convergence property: Schedule()'s returned NextRun is computed against the
// same location the live gocron engine resolves at-times in, so the
// Schedule()-return value equals what Scheduled() (the live surface) reports.
// Schedule() once always resolved against UTC, so the two
// diverged whenever WithLocation set a non-UTC zone. It is also the
// correctness oracle for the interval-aware calendarNext first fire:
// the interval>1 and large-interval rows prove the pure Schedule()-return
// value matches the live gocron NextRun, not just the interval==1 case.
func TestNativeScheduler_ScheduleReturnMatchesLocation(t *testing.T) {
	plusThree := time.FixedZone("plusThree", 3*60*60)

	type testCase struct {
		name   string
		start  time.Time
		opts   []scheduler.Option
		trig   scheduler.Trigger
		assert func(t *testing.T, sj scheduler.ScheduledJob) // extra, case-specific checks beyond convergence
	}

	cases := []testCase{
		{
			name:  "interval=1 daily resolves in non-UTC location (baseline)",
			start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			opts:  []scheduler.Option{scheduler.WithLocation(plusThree)},
			trig:  scheduler.Daily(1, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, sj scheduler.ScheduledJob) {
				// 09:00 at UTC+3 == 06:00 UTC — the Schedule()-return now
				// matches the live fire, not the (previously UTC-pinned)
				// trigger reference.
				assert.True(t, sj.NextRun().UTC().Equal(time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC)),
					"want 06:00 UTC, got %s", sj.NextRun().UTC())
			},
		},
		{
			name:  "daily interval=2 past current day converges with live gocron",
			start: time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC), // past the day's 09:00
			trig:  scheduler.Daily(2, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, sj scheduler.ScheduledJob) {
				assert.True(t, sj.NextRun().Equal(time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)),
					"want 2026-07-12 09:00 UTC, got %s", sj.NextRun())
			},
		},
		{
			name:  "weekly interval=2 multi-weekday past current week converges with live gocron",
			start: time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC), // Wednesday, past this week's 09:00
			trig:  scheduler.Weekly(2, []time.Weekday{time.Monday, time.Wednesday}, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, sj scheduler.ScheduledJob) {
				assert.True(t, sj.NextRun().Equal(time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)),
					"want 2026-08-03 09:00 UTC, got %s", sj.NextRun())
			},
		},
		{
			name:  "monthly interval=3 day-31 skip-month past current day converges with live gocron",
			start: time.Date(2026, 1, 31, 10, 0, 0, 0, time.UTC), // past the day's 09:00
			trig:  scheduler.Monthly(3, []int{31}, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, sj scheduler.ScheduledJob) {
				assert.True(t, sj.NextRun().Equal(time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)),
					"want 2026-07-31 09:00 UTC, got %s", sj.NextRun())
			},
		},
		{
			name:  "monthly large interval exercises the scaled scan bound",
			start: time.Date(2026, 1, 28, 10, 0, 0, 0, time.UTC), // past the day's 09:00
			trig:  scheduler.Monthly(100, []int{28}, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, sj scheduler.ScheduledJob) {
				// interval=100 months is ~8.3 years, past the unscaled 5-year
				// scan bound (maxCalendarScanDays); without the interval scaling
				// the pure path would report ok=false while gocron still fires.
				assert.True(t, sj.NextRun().Equal(time.Date(2034, 5, 28, 9, 0, 0, 0, time.UTC)),
					"want 2034-05-28 09:00 UTC, got %s", sj.NextRun())
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk := clockwork.NewFakeClockAt(c.start)
			opts := append([]scheduler.Option{scheduler.WithClock(clk)}, c.opts...)
			s, err := scheduler.NewScheduler(opts...)
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })

			sj, err := s.Schedule(t.Context(), mustJob(t, "job", surfaceKind, c.trig, func() {}))
			require.NoError(t, err)

			// Prove CONVERGENCE with the live surface — the whole point of
			// the change: mustJob defaults to ActivationAuto, so Schedule
			// already armed the job against the live gocron engine. Read the
			// live next-run straight back via Scheduled and confirm it
			// equals the Schedule()-return value (both loc-resolved), not
			// just the computed number.
			live, err := s.Scheduled(t.Context(), "job")
			require.NoError(t, err)
			assert.True(t, live.NextRun().Equal(sj.NextRun()),
				"Schedule()-return %s must equal live Scheduled() %s", sj.NextRun(), live.NextRun())

			c.assert(t, sj)
		})
	}
}
