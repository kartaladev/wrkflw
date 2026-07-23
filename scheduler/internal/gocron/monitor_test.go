package gocron_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	sched "github.com/kartaladev/wrkflw/scheduler/internal/gocron"
)

// jobRunsTotalMetric/jobDurationSecondsMetric are the metric names monitor.go
// registers (ADR-0134 production item ①). Mirrored here as string literals
// rather than importing an unexported constant — this file lives in the
// black-box gocron_test package.
const (
	jobRunsTotalMetric       = "wrkflw_scheduler_job_runs_total"
	jobDurationSecondsMetric = "wrkflw_scheduler_job_duration_seconds"
)

// sumFor returns the summed value of the int64 Sum metric named metricName
// across data points whose Attributes contain attrKey=attrVal.
func sumFor(t *testing.T, reader *sdkmetric.ManualReader, metricName, attrKey, attrVal string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != metricName {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, dp := range sum.DataPoints {
				if attrMatches(dp.Attributes, attrKey, attrVal) {
					total += dp.Value
				}
			}
		}
	}
	return total
}

// histogramCountFor returns the summed observation Count of the float64
// Histogram metric named metricName across data points whose Attributes
// contain attrKey=attrVal.
func histogramCountFor(t *testing.T, reader *sdkmetric.ManualReader, metricName, attrKey, attrVal string) uint64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))
	var total uint64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != metricName {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				continue
			}
			for _, dp := range hist.DataPoints {
				if attrMatches(dp.Attributes, attrKey, attrVal) {
					total += dp.Count
				}
			}
		}
	}
	return total
}

func attrMatches(set attribute.Set, key, val string) bool {
	v, ok := set.Value(attribute.Key(key))
	return ok && v.AsString() == val
}

// recordsWithLevelAndKey reports whether h captured a record at level lvl
// carrying an attribute keyed key (any value).
func recordsWithLevelAndKey(h *captureHandler, lvl slog.Level, key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Level != lvl {
			continue
		}
		found := false
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == key {
				found = true
				return false
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

// TestGocronScheduler_MonitorStatus verifies the gocron-native Monitor +
// EventListener wiring (ADR-0134 production item ①): job outcomes flow into
// the wrkflw_scheduler_job_runs_total counter and
// wrkflw_scheduler_job_duration_seconds histogram (both attributed by
// status), and a panicking task is recovered by gocron (the scheduler stays
// usable) while the panic is logged via the AfterJobRunsWithPanic listener.
func TestGocronScheduler_MonitorStatus(t *testing.T) {
	type tc struct {
		name   string
		assert func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock, reader *sdkmetric.ManualReader, h *captureHandler)
	}

	cases := []tc{
		{
			name: "erroring task records status=fail on both instruments",
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock, reader *sdkmetric.ManualReader, _ *captureHandler) {
				boom := errors.New("boom")
				_, err := s.ScheduleJob(t.Context(), "job-fail", sched.After(time.Second),
					func(context.Context) error { return boom }, false)
				require.NoError(t, err)

				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				clk.Advance(2 * time.Second)

				require.Eventually(t, func() bool {
					return sumFor(t, reader, jobRunsTotalMetric, "status", "fail") >= 1
				}, time.Second, 5*time.Millisecond, "job_runs_total{status=fail} must reach 1")

				assert.GreaterOrEqual(t, histogramCountFor(t, reader, jobDurationSecondsMetric, "status", "fail"), uint64(1),
					"a duration point must be recorded for the failed run")
			},
		},
		{
			name: "succeeding task records status=success on both instruments",
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock, reader *sdkmetric.ManualReader, _ *captureHandler) {
				_, err := s.ScheduleJob(t.Context(), "job-ok", sched.After(time.Second),
					func(context.Context) error { return nil }, false)
				require.NoError(t, err)

				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				clk.Advance(2 * time.Second)

				require.Eventually(t, func() bool {
					return sumFor(t, reader, jobRunsTotalMetric, "status", "success") >= 1
				}, time.Second, 5*time.Millisecond, "job_runs_total{status=success} must reach 1")

				assert.GreaterOrEqual(t, histogramCountFor(t, reader, jobDurationSecondsMetric, "status", "success"), uint64(1),
					"a duration point must be recorded for the successful run")
			},
		},
		{
			name: "panicking task is recovered: scheduler survives and the panic listener logs ERROR",
			assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock, _ *sdkmetric.ManualReader, h *captureHandler) {
				_, err := s.ScheduleJob(t.Context(), "job-panic", sched.After(time.Second),
					func(context.Context) error { panic("kaboom") }, false)
				require.NoError(t, err)

				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				clk.Advance(2 * time.Second)

				require.Eventually(t, func() bool {
					return recordsWithLevelAndKey(h, slog.LevelError, "panic")
				}, time.Second, 5*time.Millisecond, "AfterJobRunsWithPanic must log an ERROR record carrying the panic payload")

				// The scheduler must remain usable after a task panic: schedule and
				// fire another job successfully.
				var fired atomic.Bool
				_, err = s.ScheduleJob(t.Context(), "job-after-panic", sched.After(time.Second),
					func(context.Context) error { fired.Store(true); return nil }, false)
				require.NoError(t, err)

				require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
				clk.Advance(2 * time.Second)

				require.Eventually(t, func() bool { return fired.Load() }, time.Second, 5*time.Millisecond,
					"scheduler must still fire jobs after recovering from a prior panic")
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk := clockwork.NewFakeClock()
			reader := sdkmetric.NewManualReader()
			mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })

			h := &captureHandler{}
			logger := slog.New(h)

			s, err := sched.NewGocronScheduler(
				sched.WithClock(clk),
				sched.WithMeterProvider(mp),
				sched.WithLogger(logger),
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })

			c.assert(t, s, clk, reader, h)
		})
	}
}
