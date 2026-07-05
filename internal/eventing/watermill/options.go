// Package watermill adapts a watermill message.Publisher to the
// kernel.OutboxPublisher port. It is the only package besides eventing/ that imports
// watermill; engine/model/runtime never do.
package watermill

import (
	"log/slog"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Option configures a Publisher.
type Option func(*config)

type config struct {
	logger *slog.Logger
	tp     trace.TracerProvider
	mp     metric.MeterProvider
}

// WithLogger sets the structured logger (default slog.Default()). A nil logger
// is ignored.
func WithLogger(l *slog.Logger) Option {
	return func(c *config) {
		if l != nil {
			c.logger = l
		}
	}
}

// WithTracerProvider sets the tracer provider (default: otel global).
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(c *config) {
		if tp != nil {
			c.tp = tp
		}
	}
}

// WithMeterProvider sets the meter provider (default: otel global).
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(c *config) {
		if mp != nil {
			c.mp = mp
		}
	}
}
