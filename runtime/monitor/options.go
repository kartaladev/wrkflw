package monitor

import (
	"log/slog"

	"github.com/jonboulle/clockwork"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/kartaladev/wrkflw/internal/observability"
)

// Option configures a monitor stats collector ([NewOutboxStatsCollector],
// [NewTimerStatsCollector]).
//
// The collectors previously took the module-internal option type directly,
// which made every one of these settings unreachable from outside the module —
// a consumer naming it got "use of internal package ... not allowed" and was
// left pinned to the OTel global providers with no compile error at the call
// they could write. This is the same shape the rest of the repo already uses
// (see runtime.Option, calllink.CallNotifierOption): a package-owned option
// type, with the internal one held in an unexported field.
type Option func(*collectorConfig)

// collectorConfig stages the telemetry options a collector was built with,
// plus the time source it derives ages from.
type collectorConfig struct {
	obsOpts []observability.Option
	clk     clockwork.Clock
}

// telemetry applies opts and builds the collector's [observability.Telemetry].
// A nil Option is silently ignored, as are nil values inside an option.
func (c *collectorConfig) telemetry(name string, opts []Option) observability.Telemetry {
	for _, o := range opts {
		if o != nil {
			o(c)
		}
	}
	if c.clk == nil {
		c.clk = clockwork.NewRealClock()
	}
	return observability.New(name, c.obsOpts...)
}

// WithClock sets the time source a collector derives wall-clock ages from —
// currently the overdue age of the earliest armed timer. Default:
// [clockwork.NewRealClock]. A nil value is ignored.
//
// ⚠ In tests use [clockwork.NewFakeClockAt] with an explicit instant, not
// clockwork.NewFakeClock(): the latter seeds itself from the wall clock, so an
// age assertion written against it can pass even against a collector that never
// consults the injected clock at all.
func WithClock(clk clockwork.Clock) Option {
	return func(c *collectorConfig) {
		if clk != nil {
			c.clk = clk
		}
	}
}

// WithLogger sets the structured logger a collector reports read failures and
// instrument-registration failures on. Default: [slog.Default]. A nil value is
// ignored.
func WithLogger(l *slog.Logger) Option {
	return func(c *collectorConfig) { c.obsOpts = append(c.obsOpts, observability.WithLogger(l)) }
}

// WithTracerProvider sets the OTel TracerProvider. Default: the OTel global
// provider. A nil value is ignored.
//
// The collectors emit no spans today — they read inside the OTel SDK's own
// collection callback — so this exists for parity with the other packages'
// option sets and for instrumentation added later.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(c *collectorConfig) { c.obsOpts = append(c.obsOpts, observability.WithTracerProvider(tp)) }
}

// WithMeterProvider sets the OTel MeterProvider the collector registers its
// observable gauges on. Default: the OTel global provider.
//
// This is the setting the leak actually cost consumers: without it, a consumer
// running a non-global MeterProvider gets no gauges at all. A nil value is
// ignored.
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(c *collectorConfig) { c.obsOpts = append(c.obsOpts, observability.WithMeterProvider(mp)) }
}
