// Package obs bundles slog and OTel tracing/metrics behind one small helper,
// scoped to the scheduler subsystem so scheduler/internal/gocron does not
// depend on the module-wide internal/observability package (keeping the
// scheduler tree self-contained). It ports the subset of that package's API
// actually used by scheduler/internal/gocron, plus the Int64Counter and
// Float64Histogram instrument helpers needed by later scheduler-owned-jobs
// work.
package obs

import (
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
)

// Telemetry carries a logger plus a scoped tracer and meter. All three fields
// are always non-nil after [New].
type Telemetry struct {
	// Logger is the structured logger for the instrumented component.
	Logger *slog.Logger
	// Tracer is an OTel tracer scoped to the instrumentation name.
	Tracer trace.Tracer
	// Meter is an OTel meter scoped to the instrumentation name.
	Meter metric.Meter

	// name is kept for the noop fallback inside instrument constructors.
	name string
}

type config struct {
	logger *slog.Logger
	tp     trace.TracerProvider
	mp     metric.MeterProvider
}

// Option configures [New].
type Option func(*config)

// WithLogger sets the structured logger. A nil value is ignored and the default
// (slog.Default()) is kept.
func WithLogger(l *slog.Logger) Option {
	return func(c *config) {
		if l != nil {
			c.logger = l
		}
	}
}

// WithTracerProvider sets the OTel tracer provider. A nil value is ignored and
// the OTel global provider is used.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(c *config) {
		if tp != nil {
			c.tp = tp
		}
	}
}

// WithMeterProvider sets the OTel meter provider. A nil value is ignored and
// the OTel global provider is used.
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(c *config) {
		if mp != nil {
			c.mp = mp
		}
	}
}

// New builds a [Telemetry] scoped to instrumentationName (typically the
// importing package path, e.g. "github.com/kartaladev/wrkflw/scheduler").
// Unset providers fall back to the OTel globals; the logger defaults to
// [slog.Default]. All three fields in the returned value are guaranteed
// non-nil.
func New(instrumentationName string, opts ...Option) Telemetry {
	cfg := config{logger: slog.Default()}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.tp == nil {
		cfg.tp = otel.GetTracerProvider()
	}
	if cfg.mp == nil {
		cfg.mp = otel.GetMeterProvider()
	}
	return Telemetry{
		Logger: cfg.logger,
		Tracer: cfg.tp.Tracer(instrumentationName),
		Meter:  cfg.mp.Meter(instrumentationName),
		name:   instrumentationName,
	}
}

// Int64Counter creates a counter instrument scoped to this Telemetry's meter.
// On error it falls back to a noop instrument so callers never receive nil.
func (t Telemetry) Int64Counter(name, desc string) metric.Int64Counter {
	c, err := t.Meter.Int64Counter(name, metric.WithDescription(desc))
	if err != nil {
		c, _ = metricnoop.NewMeterProvider().Meter(t.name).Int64Counter(name)
	}
	return c
}

// Float64Histogram creates a histogram instrument. Falls back to noop on error
// so callers never receive nil.
func (t Telemetry) Float64Histogram(name, desc string) metric.Float64Histogram {
	h, err := t.Meter.Float64Histogram(name, metric.WithDescription(desc))
	if err != nil {
		h, _ = metricnoop.NewMeterProvider().Meter(t.name).Float64Histogram(name)
	}
	return h
}
