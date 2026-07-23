package obs_test

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"

	"github.com/kartaladev/wrkflw/scheduler/internal/obs"
)

// errMeter is a metric.Meter that always returns an error from every instrument
// constructor, letting us exercise the noop-fallback branches in Telemetry.
type errMeter struct{ metricnoop.Meter }

func (errMeter) Int64Counter(string, ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	return nil, errors.New("injected error")
}
func (errMeter) Float64Histogram(string, ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	return nil, errors.New("injected error")
}

// errMeterProvider wraps errMeter so it satisfies metric.MeterProvider.
type errMeterProvider struct{ metricnoop.MeterProvider }

func (errMeterProvider) Meter(_ string, _ ...metric.MeterOption) metric.Meter {
	return errMeter{}
}

func TestNew_Defaults(t *testing.T) {
	t.Parallel()

	tel := obs.New("test/scope")

	require.NotNil(t, tel.Logger, "Logger must be populated")
	require.NotNil(t, tel.Tracer, "Tracer must be populated")
	require.NotNil(t, tel.Meter, "Meter must be populated")
}

func TestNew_WithLogger(t *testing.T) {
	t.Parallel()

	custom := slog.New(slog.NewTextHandler(io.Discard, nil))
	tel := obs.New("test/scope", obs.WithLogger(custom))

	assert.Same(t, custom, tel.Logger, "WithLogger must replace the default logger")
}

func TestNew_WithLogger_NilIgnored(t *testing.T) {
	t.Parallel()

	tel := obs.New("test/scope", obs.WithLogger(nil))

	assert.NotNil(t, tel.Logger, "nil logger option must be ignored; default must survive")
}

func TestNew_WithMeterProvider(t *testing.T) {
	t.Parallel()

	tel := obs.New("test/scope", obs.WithMeterProvider(metricnoop.NewMeterProvider()))

	// The meter must still be non-nil when a custom provider is supplied.
	assert.NotNil(t, tel.Meter, "WithMeterProvider must produce a non-nil Meter")
}

func TestNew_WithMeterProvider_NilIgnored(t *testing.T) {
	t.Parallel()

	tel := obs.New("test/scope", obs.WithMeterProvider(nil))

	assert.NotNil(t, tel.Meter, "nil meter-provider option must be ignored; default must survive")
}

// TestInstruments_NeverFail verifies that the never-fail instrument constructors
// always return a non-nil instrument, even when the underlying meter returns an
// error.
func TestInstruments_NeverFail(t *testing.T) {
	t.Parallel()

	tel := obs.New("test/scope")

	assert.NotNil(t, tel.Int64Counter("req.count", "total requests"), "Int64Counter must never return nil")
	assert.NotNil(t, tel.Float64Histogram("req.duration", "request latency"), "Float64Histogram must never return nil")
}

// TestInstruments_NoopFallback exercises the error-fallback branches by
// supplying a meter provider whose meter always returns errors.
func TestInstruments_NoopFallback(t *testing.T) {
	t.Parallel()

	tel := obs.New("test/scope", obs.WithMeterProvider(errMeterProvider{}))

	assert.NotNil(t, tel.Int64Counter("c", "d"), "Int64Counter noop fallback must not return nil")
	assert.NotNil(t, tel.Float64Histogram("h", "d"), "Float64Histogram noop fallback must not return nil")
}
