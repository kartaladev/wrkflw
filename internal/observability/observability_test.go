package observability_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/zakyalvan/krtlwrkflw/internal/observability"
)

// errMeter is a metric.Meter that always returns an error from every instrument
// constructor, letting us exercise the noop-fallback branches in Telemetry.
type errMeter struct{ metricnoop.Meter }

func (errMeter) Int64Counter(string, ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	return nil, errors.New("injected error")
}
func (errMeter) Int64UpDownCounter(string, ...metric.Int64UpDownCounterOption) (metric.Int64UpDownCounter, error) {
	return nil, errors.New("injected error")
}
func (errMeter) Float64Histogram(string, ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	return nil, errors.New("injected error")
}
func (errMeter) Int64ObservableGauge(string, ...metric.Int64ObservableGaugeOption) (metric.Int64ObservableGauge, error) {
	return nil, errors.New("injected error")
}

// errMeterProvider wraps errMeter so it satisfies metric.MeterProvider.
type errMeterProvider struct{ metricnoop.MeterProvider }

func (errMeterProvider) Meter(_ string, _ ...metric.MeterOption) metric.Meter {
	return errMeter{}
}

func TestNew_Defaults(t *testing.T) {
	t.Parallel()

	tel := observability.New("test/scope")

	require.NotNil(t, tel.Logger, "Logger must be populated")
	require.NotNil(t, tel.Tracer, "Tracer must be populated")
	require.NotNil(t, tel.Meter, "Meter must be populated")
}

func TestNew_WithLogger(t *testing.T) {
	t.Parallel()

	custom := slog.New(slog.NewTextHandler(io.Discard, nil))
	tel := observability.New("test/scope", observability.WithLogger(custom))

	assert.Same(t, custom, tel.Logger, "WithLogger must replace the default logger")
}

func TestNew_WithLogger_NilIgnored(t *testing.T) {
	t.Parallel()

	tel := observability.New("test/scope", observability.WithLogger(nil))

	assert.NotNil(t, tel.Logger, "nil logger option must be ignored; default must survive")
}

func TestNew_WithMeterProvider(t *testing.T) {
	t.Parallel()

	tel := observability.New("test/scope", observability.WithMeterProvider(metricnoop.NewMeterProvider()))

	// The meter must still be non-nil when a custom provider is supplied.
	assert.NotNil(t, tel.Meter, "WithMeterProvider must produce a non-nil Meter")
}

func TestNew_WithMeterProvider_NilIgnored(t *testing.T) {
	t.Parallel()

	tel := observability.New("test/scope", observability.WithMeterProvider(nil))

	assert.NotNil(t, tel.Meter, "nil meter-provider option must be ignored; default must survive")
}

// TestLogAttrs verifies span-correlation attrs are attached only when a real
// (recording) span is active in the context.
func TestLogAttrs(t *testing.T) {
	t.Parallel()

	// A real SDK tracer provider so spans are recording and carry valid IDs.
	tp := sdktrace.NewTracerProvider()
	tel := observability.New("test/scope", observability.WithTracerProvider(tp))

	type testCase struct {
		name   string
		ctx    func(base context.Context) context.Context
		assert func(t *testing.T, attrs []slog.Attr)
	}

	cases := []testCase{
		{
			name: "no active span returns no attrs",
			ctx:  nil, // identity — plain t.Context(), no span
			assert: func(t *testing.T, attrs []slog.Attr) {
				t.Helper()
				assert.Empty(t, attrs, "no active span must produce no log attrs")
			},
		},
		{
			name: "active span injects trace_id and span_id",
			ctx: func(base context.Context) context.Context {
				ctx, span := tel.Tracer.Start(base, "op")
				t.Cleanup(func() { span.End() })
				return ctx
			},
			assert: func(t *testing.T, attrs []slog.Attr) {
				t.Helper()
				keys := make(map[string]bool, len(attrs))
				for _, a := range attrs {
					keys[a.Key] = true
				}
				assert.True(t, keys["trace_id"], "trace_id attr must be present")
				assert.True(t, keys["span_id"], "span_id attr must be present")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tc.ctx != nil {
				ctx = tc.ctx(ctx)
			}

			attrs := tel.LogAttrs(ctx)
			tc.assert(t, attrs)
		})
	}
}

// TestInstruments_NeverFail verifies that the never-fail instrument constructors
// always return a non-nil instrument, even when the underlying meter returns an
// error.
func TestInstruments_NeverFail(t *testing.T) {
	t.Parallel()

	tel := observability.New("test/scope")

	assert.NotNil(t, tel.Int64Counter("req.count", "total requests"), "Int64Counter must never return nil")
	assert.NotNil(t, tel.Int64UpDownCounter("queue.depth", "current queue depth"), "Int64UpDownCounter must never return nil")
	assert.NotNil(t, tel.Float64Histogram("req.duration", "request latency"), "Float64Histogram must never return nil")
	assert.NotNil(t, tel.Int64ObservableGauge("active.sessions", "active sessions", nil), "Int64ObservableGauge must never return nil")
}

// TestInstruments_NoopFallback exercises the error-fallback branches by
// supplying a meter provider whose meter always returns errors.
func TestInstruments_NoopFallback(t *testing.T) {
	t.Parallel()

	tel := observability.New("test/scope", observability.WithMeterProvider(errMeterProvider{}))

	assert.NotNil(t, tel.Int64Counter("c", "d"), "Int64Counter noop fallback must not return nil")
	assert.NotNil(t, tel.Int64UpDownCounter("g", "d"), "Int64UpDownCounter noop fallback must not return nil")
	assert.NotNil(t, tel.Float64Histogram("h", "d"), "Float64Histogram noop fallback must not return nil")
	assert.NotNil(t, tel.Int64ObservableGauge("g2", "gauge", nil), "Int64ObservableGauge noop fallback must not return nil")
}

// TestInt64ObservableGauge_CollectsValue verifies that a gauge registered via
// Int64ObservableGauge produces datapoints with the expected value when the
// manual reader is collected.
func TestInt64ObservableGauge_CollectsValue(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		observed  int64
		wantValue int64
	}

	cases := []testCase{
		{name: "positive value", observed: 42, wantValue: 42},
		{name: "zero value", observed: 0, wantValue: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rdr := sdkmetric.NewManualReader()
			mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(rdr))

			cb := func(_ context.Context, o metric.Int64Observer) error {
				o.Observe(tc.observed)
				return nil
			}

			tel := observability.New("test/scope", observability.WithMeterProvider(mp))
			g := tel.Int64ObservableGauge("test.gauge", "a test gauge", cb)
			require.NotNil(t, g, "Int64ObservableGauge must not return nil")

			var rm metricdata.ResourceMetrics
			require.NoError(t, rdr.Collect(context.Background(), &rm))

			var found bool
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					if m.Name != "test.gauge" {
						continue
					}
					found = true
					gauge, ok := m.Data.(metricdata.Gauge[int64])
					require.True(t, ok, "metric data must be a Gauge[int64]")
					require.Len(t, gauge.DataPoints, 1, "expected exactly one datapoint")
					assert.Equal(t, tc.wantValue, gauge.DataPoints[0].Value,
						"gauge datapoint value must match observed value")
				}
			}
			assert.True(t, found, "gauge metric 'test.gauge' must be present in collected metrics")
		})
	}
}
