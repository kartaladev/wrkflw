package grpctransport

import (
	"log/slog"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/internal/observability"
)

// serverConfig holds the resolved gRPC server configuration for telemetry.
type serverConfig struct {
	logOpt observability.Option
	tpOpt  observability.Option
	mpOpt  observability.Option
}

// Option is a functional option for [RegisterWorkflowServiceServer].
type Option func(*serverConfig)

// WithLogger sets the structured logger used by the gRPC service handlers.
// A nil value is ignored and slog.Default() is kept.
func WithLogger(l *slog.Logger) Option {
	return func(c *serverConfig) { c.logOpt = observability.WithLogger(l) }
}

// WithTracerProvider sets the OTel tracer provider used by the gRPC service
// handlers for per-RPC spans. A nil value is ignored and the OTel global
// provider is used.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(c *serverConfig) { c.tpOpt = observability.WithTracerProvider(tp) }
}

// WithMeterProvider sets the OTel meter provider used by the gRPC service
// handlers. A nil value is ignored and the OTel global provider is used.
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(c *serverConfig) { c.mpOpt = observability.WithMeterProvider(mp) }
}

// nonNilOpts returns only the non-nil observability.Option values from opts.
func nonNilOpts(opts ...observability.Option) []observability.Option {
	out := make([]observability.Option, 0, len(opts))
	for _, o := range opts {
		if o != nil {
			out = append(out, o)
		}
	}
	return out
}
