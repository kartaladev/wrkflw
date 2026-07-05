// Package eventing is the consumer-facing façade for publishing wrkflw domain
// events to a broker via watermill. Wrap any watermill message.Publisher with
// NewPublisher and hand the result to persistence.NewRelay. watermill is
// confined to this package and internal/eventing/watermill; engine/model/runtime
// never import it.
//
// # Process-instance chaining
//
// The subscriber side of process-instance chaining (ADR-0045) also lives here so
// runtime stays watermill-free: NewChainHandler adapts a runtime.Chainer to a
// watermill no-publish handler you mount on your own message.Router, and
// NewChainerRunner / Chainer.Run is a turnkey wrapper that subscribes the three
// status-accurate terminal topics (instance.completed / instance.failed /
// instance.terminated) and drives the chaining core.
package eventing

import (
	"io"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	watermillpub "github.com/zakyalvan/krtlwrkflw/internal/eventing/watermill"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Compile-time guard: the internal adapter satisfies the public port.
var _ kernel.OutboxPublisher = (*watermillpub.Publisher)(nil)

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

// NewPublisher wraps a watermill message.Publisher as a kernel.OutboxPublisher,
// mapping each OutboxEvent to a watermill message.
func NewPublisher(pub message.Publisher, opts ...Option) kernel.OutboxPublisher {
	var o options
	for _, fn := range opts {
		fn(&o)
	}
	return watermillpub.NewPublisher(pub, toInternal(o)...)
}

// NewGoChannelPublisher builds an in-process GoChannel pub/sub and returns a
// kernel.OutboxPublisher over it, the matching Subscriber (for in-process consumers
// or tests), and an io.Closer to release it. No external broker is required.
// GoChannel ships in watermill core, so this adds no broker dependency.
func NewGoChannelPublisher(opts ...Option) (kernel.OutboxPublisher, message.Subscriber, io.Closer) {
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
