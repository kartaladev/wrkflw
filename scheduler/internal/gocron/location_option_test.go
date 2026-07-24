package gocron_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sched "github.com/kartaladev/wrkflw/scheduler/internal/gocron"
)

// A Daily job at 09:00 must resolve its at-time in the scheduler's configured
// location. Default (no WithLocation) == UTC; WithLocation(loc) == loc;
// WithLocation(nil) falls back to UTC (never gocron's time.Local default).
func TestGocronScheduler_WithLocation(t *testing.T) {
	// Fake "now" is 2026-01-01 00:00:00 UTC. The next 09:00 in a given zone,
	// expressed as an absolute instant, differs by that zone's offset.
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	plusThree := time.FixedZone("plusThree", 3*60*60)

	cases := []struct {
		name   string
		opts   []sched.Option
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
			name: "WithLocation(nil) falls back to UTC",
			opts: []sched.Option{sched.WithLocation(nil)},
			assert: func(t *testing.T, got time.Time) {
				assert.True(t, got.Equal(time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)),
					"want 09:00 UTC, got %s", got)
			},
		},
		{
			name: "WithLocation(+3) resolves at-time in +3",
			opts: []sched.Option{sched.WithLocation(plusThree)},
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
			opts := append([]sched.Option{sched.WithClock(clk)}, c.opts...)
			s, err := sched.NewGocronScheduler(opts...)
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })

			_, err = s.ScheduleJob(t.Context(), "daily-9am",
				sched.Daily(1, sched.ClockTime{Hour: 9}),
				func(context.Context) error { return nil }, false)
			require.NoError(t, err)

			got, ok := s.NextRun("daily-9am")
			require.True(t, ok)
			c.assert(t, got.UTC())
		})
	}
}

// A Cron job ("0 9 * * *", i.e. daily at 09:00) must also resolve in the
// scheduler's configured location — not only the calendar (Daily/Weekly/
// Monthly) triggers. gocron applies the scheduler location to cron
// expressions via an implicit "CRON_TZ=<location.String()>" prefix, which its
// underlying cron parser resolves with time.LoadLocation — so (unlike the
// calendar-trigger cases above) this requires a real IANA zone name rather
// than an anonymous time.FixedZone. Europe/Moscow is a fixed UTC+3 zone (no
// DST since 2014), giving the same +3 offset as the calendar-trigger cases'
// "plusThree" fixture without conflating this with the DST case below.
func TestGocronScheduler_WithLocation_Cron(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	plusThree, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}

	cases := []struct {
		name   string
		opts   []sched.Option
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
			name: "WithLocation(+3) resolves cron at-time in +3",
			opts: []sched.Option{sched.WithLocation(plusThree)},
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
			opts := append([]sched.Option{sched.WithClock(clk)}, c.opts...)
			s, err := sched.NewGocronScheduler(opts...)
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })

			_, err = s.ScheduleJob(t.Context(), "cron-daily-9am",
				sched.Cron("0 9 * * *"),
				func(context.Context) error { return nil }, false)
			require.NoError(t, err)

			got, ok := s.NextRun("cron-daily-9am")
			require.True(t, ok)
			c.assert(t, got.UTC())
		})
	}
}

// A Daily at-time resolved in a named IANA zone must honor that zone's live
// DST transitions, not a fixed offset. 2026-03-08 is the US spring-forward
// date (clocks jump 02:00 -> 03:00 local); by 09:00 the zone is already in
// EDT (UTC-4), so 09:00 America/New_York on that date is 13:00 UTC — not
// 14:00 UTC (which a naive fixed UTC-5 offset would produce).
func TestGocronScheduler_WithLocation_DST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}

	start := time.Date(2026, 3, 8, 0, 0, 0, 0, loc)
	clk := clockwork.NewFakeClockAt(start)

	s, err := sched.NewGocronScheduler(sched.WithClock(clk), sched.WithLocation(loc))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	_, err = s.ScheduleJob(t.Context(), "daily-9am-dst",
		sched.Daily(1, sched.ClockTime{Hour: 9}),
		func(context.Context) error { return nil }, false)
	require.NoError(t, err)

	got, ok := s.NextRun("daily-9am-dst")
	require.True(t, ok)
	assert.True(t, got.UTC().Equal(time.Date(2026, 3, 8, 13, 0, 0, 0, time.UTC)),
		"want 2026-03-08 09:00 America/New_York (EDT, UTC-4) == 13:00 UTC, got %s", got.UTC())
}
