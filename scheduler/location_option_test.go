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
// NextRun, as surfaced by Scheduled (ADR-0136). It mirrors
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
			// (correct) instant, i.e. 06:00 UTC. (Schedule()'s own return
			// value, by contrast, is the Trigger.Next UTC reference.)
			sj, err := s.Scheduled(t.Context(), "daily-9am")
			require.NoError(t, err)
			c.assert(t, sj.NextRun().UTC())
		})
	}
}
