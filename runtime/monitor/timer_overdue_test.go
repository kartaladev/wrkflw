package monitor_test

// TimerStatsCollector reads kernel.TimerStats every collection cycle, and the
// store genuinely computes NextFireAt in SQL on every one of those calls — then
// the callback observed only Armed and threw NextFireAt away. A scheduler 45
// minutes behind emitted exactly one instrument, wrkflw_timers_armed=7, which is
// the same value a perfectly healthy scheduler emits. The overdue age costs no
// extra query; it was already in hand.
//
// ⚠ Lateness is NOT unmeasured everywhere: the scheduler computes it once, in
// its gocron adapter, for a one-shot that is ALREADY past due at arm time, and
// only as a WARN above the skew tolerance. A recurring timer, and a one-shot
// that goes overdue AFTER being armed — the stalled-scheduler case this gauge
// exists for — never reach that code.
//
// ⚠ clockwork.NewFakeClock() seeds from WALL TIME in the pinned version, so a
// test built on it can pass while production still reads the wall clock. Every
// case here pins an absolute instant with NewFakeClockAt and asserts an exact
// duration.

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/runtime/monitor"
)

// collectorNow is the fixed instant every case in this file measures against.
// It is far from any wall clock on purpose: a fix that reads time.Now() instead
// of the injected clock cannot produce these numbers.
var collectorNow = time.Date(2031, 3, 4, 12, 0, 0, 0, time.UTC)

func timePtr(t time.Time) *time.Time { return &t }

// TestTimerStatsCollectorReportsOverdueAge pins that the age of the earliest
// armed timer's next_run must be observable.
//
// What makes it fail today: NewTimerStatsCollector registers exactly one
// instrument (wrkflw_timers_armed) and its callback observes only stats.Armed,
// so no metric named wrkflw_timers_next_fire_age_seconds exists to be found.
//
// The nil / zero-time / future rows are not padding. NextFireAt is a *time.Time
// that is nil for an empty table and can legitimately be 0001-01-01 in a stored
// row, and a healthy timer is by definition in the FUTURE — a naive
// now.Sub(*NextFireAt) reports a ~2000-year age for the second and a negative
// age for the third, i.e. the gauge would be wrong in the normal case.
func TestTimerStatsCollectorReportsOverdueAge(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		stats  kernel.TimerStats
		assert func(t *testing.T, rm metricdata.ResourceMetrics)
	}

	overdueAge := func(t *testing.T, rm metricdata.ResourceMetrics) int64 {
		t.Helper()
		v, ok := gaugeInt64Value(rm, "wrkflw_timers_next_fire_age_seconds")
		require.True(t, ok, "wrkflw_timers_next_fire_age_seconds must be present")
		return v
	}

	cases := []testCase{
		{
			name: "an overdue next fire reports its exact age in seconds",
			stats: kernel.TimerStats{
				Armed:      7,
				NextFireAt: timePtr(collectorNow.Add(-45 * time.Minute)),
			},
			assert: func(t *testing.T, rm metricdata.ResourceMetrics) {
				t.Helper()
				assert.EqualValues(t, 2700, overdueAge(t, rm),
					"45 minutes overdue must read as 2700 seconds")
				armed, ok := gaugeInt64Value(rm, "wrkflw_timers_armed")
				require.True(t, ok, "the pre-existing gauge must survive")
				assert.EqualValues(t, 7, armed)
			},
		},
		{
			name:  "a nil next fire (empty table) reports zero, not an epoch age",
			stats: kernel.TimerStats{Armed: 0, NextFireAt: nil},
			assert: func(t *testing.T, rm metricdata.ResourceMetrics) {
				t.Helper()
				assert.EqualValues(t, 0, overdueAge(t, rm))
			},
		},
		{
			name: "a zero next fire (stored row) reports zero, not ~2000 years",
			stats: kernel.TimerStats{
				Armed:      3,
				NextFireAt: timePtr(time.Time{}),
			},
			assert: func(t *testing.T, rm metricdata.ResourceMetrics) {
				t.Helper()
				assert.EqualValues(t, 0, overdueAge(t, rm))
			},
		},
		{
			name: "a future next fire is not overdue and must not go negative",
			stats: kernel.TimerStats{
				Armed:      3,
				NextFireAt: timePtr(collectorNow.Add(30 * time.Minute)),
			},
			assert: func(t *testing.T, rm metricdata.ResourceMetrics) {
				t.Helper()
				assert.EqualValues(t, 0, overdueAge(t, rm))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rdr := sdkmetric.NewManualReader()
			mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(rdr))

			_ = monitor.NewTimerStatsCollector(&fakeTimerReader{stats: tc.stats},
				monitor.WithMeterProvider(mp),
				monitor.WithClock(clockwork.NewFakeClockAt(collectorNow)),
			)

			var rm metricdata.ResourceMetrics
			require.NoError(t, rdr.Collect(context.Background(), &rm))

			tc.assert(t, rm)
		})
	}
}

// TestTimerStatsCollectorOverdueAgeReaderError asserts the new gauge follows the
// existing failure convention: a reader error produces no datapoints on either
// instrument rather than a zero that would read as "healthy".
func TestTimerStatsCollectorOverdueAgeReaderError(t *testing.T) {
	t.Parallel()

	rdr := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(rdr))

	_ = monitor.NewTimerStatsCollector(&fakeTimerReader{err: context.DeadlineExceeded},
		monitor.WithMeterProvider(mp),
		monitor.WithClock(clockwork.NewFakeClockAt(collectorNow)),
	)

	var rm metricdata.ResourceMetrics
	require.NoError(t, rdr.Collect(context.Background(), &rm))

	_, ok := gaugeInt64Value(rm, "wrkflw_timers_next_fire_age_seconds")
	assert.False(t, ok, "a failed read must observe nothing, not a zero age that looks healthy")
}
