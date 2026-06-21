// Package eventing is the consumer-facing façade for publishing wrkflw domain
// events to a broker via watermill. Wrap any watermill message.Publisher with
// NewPublisher and hand the result to persistence.NewRelay. watermill is
// confined to this package and internal/eventing/watermill; engine/model/runtime
// never import it.
package eventing

import (
	"io"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	watermillpub "github.com/zakyalvan/krtlwrkflw/internal/eventing/watermill"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Compile-time guard: the internal adapter satisfies the public port.
var _ runtime.Publisher = (*watermillpub.Publisher)(nil)

// Option configures a publisher.
type Option func(*options)

type options struct {
	logger *slog.Logger
	tp     trace.TracerProvider
	mp     metric.MeterProvider
}

// WithLogger sets the structured logger (default slog.Default()).
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.logger = l } }

// WithTracerProvider sets the tracer provider (default: otel global).
func WithTracerProvider(tp trace.TracerProvider) Option { return func(o *options) { o.tp = tp } }

// WithMeterProvider sets the meter provider (default: otel global).
func WithMeterProvider(mp metric.MeterProvider) Option { return func(o *options) { o.mp = mp } }

// NewPublisher wraps a watermill message.Publisher as a runtime.Publisher,
// mapping each OutboxEvent to a watermill message.
func NewPublisher(pub message.Publisher, opts ...Option) runtime.Publisher {
	var o options
	for _, fn := range opts {
		fn(&o)
	}
	return watermillpub.NewPublisher(pub, toInternal(o)...)
}

// NewGoChannelPublisher builds an in-process GoChannel pub/sub and returns a
// runtime.Publisher over it, the matching Subscriber (for in-process consumers
// or tests), and an io.Closer to release it. No external broker is required.
// GoChannel ships in watermill core, so this adds no broker dependency.
func NewGoChannelPublisher(opts ...Option) (runtime.Publisher, message.Subscriber, io.Closer) {
	var o options
	for _, fn := range opts {
		fn(&o)
	}
	logger := o.logger
	if logger == nil {
		logger = slog.Default()
	}
	gc := gochannel.NewGoChannel(gochannel.Config{}, watermillpub.NewWatermillLogger(logger))
	return NewPublisher(gc, opts...), gc, gc
}

func toInternal(o options) []watermillpub.Option {
	var out []watermillpub.Option
	if o.logger != nil {
		out = append(out, watermillpub.WithLogger(o.logger))
	}
	if o.tp != nil {
		out = append(out, watermillpub.WithTracerProvider(o.tp))
	}
	if o.mp != nil {
		out = append(out, watermillpub.WithMeterProvider(o.mp))
	}
	return out
}
