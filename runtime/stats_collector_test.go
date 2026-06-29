package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/zakyalvan/krtlwrkflw/internal/observability"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// fakeOutboxReader is an in-test implementation of runtime.OutboxStatsReader.
type fakeOutboxReader struct {
	stats runtime.OutboxStats
	err   error
}

func (f *fakeOutboxReader) OutboxStats(_ context.Context) (runtime.OutboxStats, error) {
	return f.stats, f.err
}

// fakeTimerReader is an in-test implementation of runtime.TimerStatsReader.
type fakeTimerReader struct {
	stats runtime.TimerStats
	err   error
}

func (f *fakeTimerReader) Stats(_ context.Context) (runtime.TimerStats, error) {
	return f.stats, f.err
}

// gaugeInt64Value returns the int64 gauge value for the named metric from the
// ResourceMetrics snapshot, or -1 if not found.
func gaugeInt64Value(rm metricdata.ResourceMetrics, name string) (int64, bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			g, ok := m.Data.(metricdata.Gauge[int64])
			if !ok || len(g.DataPoints) == 0 {
				return 0, false
			}
			return g.DataPoints[0].Value, true
		}
	}
	return 0, false
}

// hasMetric reports whether any metric with the given name is present in rm.
func hasMetric(rm metricdata.ResourceMetrics, name string) bool {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return true
			}
		}
	}
	return false
}

// TestOutboxStatsCollector verifies that NewOutboxStatsCollector registers three
// gauges and that a Collect produces the expected datapoint values.
func TestOutboxStatsCollector(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name          string
		reader        *fakeOutboxReader
		assertMetrics func(t *testing.T, rm metricdata.ResourceMetrics)
	}

	cases := []testCase{
		{
			name: "known stats produce expected gauge values",
			reader: &fakeOutboxReader{
				stats: runtime.OutboxStats{
					Pending:          3,
					Dead:             1,
					OldestPendingAge: 2 * time.Second,
				},
			},
			assertMetrics: func(t *testing.T, rm metricdata.ResourceMetrics) {
				t.Helper()

				v, ok := gaugeInt64Value(rm, "wrkflw_outbox_pending")
				require.True(t, ok, "wrkflw_outbox_pending must be present")
				assert.EqualValues(t, 3, v, "wrkflw_outbox_pending must equal 3")

				v, ok = gaugeInt64Value(rm, "wrkflw_outbox_dead")
				require.True(t, ok, "wrkflw_outbox_dead must be present")
				assert.EqualValues(t, 1, v, "wrkflw_outbox_dead must equal 1")

				v, ok = gaugeInt64Value(rm, "wrkflw_outbox_oldest_pending_age_seconds")
				require.True(t, ok, "wrkflw_outbox_oldest_pending_age_seconds must be present")
				assert.EqualValues(t, 2, v, "wrkflw_outbox_oldest_pending_age_seconds must equal 2 (seconds)")
			},
		},
		{
			name: "reader error produces no datapoints and no panic",
			reader: &fakeOutboxReader{
				err: errors.New("db unavailable"),
			},
			assertMetrics: func(t *testing.T, rm metricdata.ResourceMetrics) {
				t.Helper()

				// The metric names may be present (registered) but must have no datapoints.
				for _, name := range []string{
					"wrkflw_outbox_pending",
					"wrkflw_outbox_dead",
					"wrkflw_outbox_oldest_pending_age_seconds",
				} {
					for _, sm := range rm.ScopeMetrics {
						for _, m := range sm.Metrics {
							if m.Name != name {
								continue
							}
							g, ok := m.Data.(metricdata.Gauge[int64])
							if ok {
								assert.Empty(t, g.DataPoints,
									"error path must produce no datapoints for %s", name)
							}
						}
					}
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rdr := sdkmetric.NewManualReader()
			mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(rdr))

			_ = runtime.NewOutboxStatsCollector(tc.reader, observability.WithMeterProvider(mp))

			var rm metricdata.ResourceMetrics
			require.NoError(t, rdr.Collect(context.Background(), &rm))

			tc.assertMetrics(t, rm)
		})
	}
}

// TestTimerStatsCollector verifies that NewTimerStatsCollector registers the
// wrkflw_timers_armed gauge and produces the expected datapoint value.
func TestTimerStatsCollector(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name          string
		reader        *fakeTimerReader
		assertMetrics func(t *testing.T, rm metricdata.ResourceMetrics)
	}

	cases := []testCase{
		{
			name: "known stats produce expected gauge value",
			reader: &fakeTimerReader{
				stats: runtime.TimerStats{Armed: 7},
			},
			assertMetrics: func(t *testing.T, rm metricdata.ResourceMetrics) {
				t.Helper()

				v, ok := gaugeInt64Value(rm, "wrkflw_timers_armed")
				require.True(t, ok, "wrkflw_timers_armed must be present")
				assert.EqualValues(t, 7, v, "wrkflw_timers_armed must equal 7")
			},
		},
		{
			name: "reader error produces no datapoints and no panic",
			reader: &fakeTimerReader{
				err: errors.New("db unavailable"),
			},
			assertMetrics: func(t *testing.T, rm metricdata.ResourceMetrics) {
				t.Helper()

				// Must not panic; any metric named wrkflw_timers_armed must have no datapoints.
				if hasMetric(rm, "wrkflw_timers_armed") {
					for _, sm := range rm.ScopeMetrics {
						for _, m := range sm.Metrics {
							if m.Name != "wrkflw_timers_armed" {
								continue
							}
							g, ok := m.Data.(metricdata.Gauge[int64])
							if ok {
								assert.Empty(t, g.DataPoints, "error path must produce no datapoints for wrkflw_timers_armed")
							}
						}
					}
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rdr := sdkmetric.NewManualReader()
			mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(rdr))

			_ = runtime.NewTimerStatsCollector(tc.reader, observability.WithMeterProvider(mp))

			var rm metricdata.ResourceMetrics
			require.NoError(t, rdr.Collect(context.Background(), &rm))

			tc.assertMetrics(t, rm)
		})
	}
}
