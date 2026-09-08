// Package eventing is the consumer-facing façade for publishing wrkflw domain
// events to a broker via watermill. Wrap any watermill message.Publisher with
// NewPublisher and hand the result to persistence.NewRelay. watermill is
// confined to this package and internal/eventing/watermill; engine/model/runtime
// never import it.
//
// # Process-instance chaining
//
// The subscriber side of process-instance chaining also lives here so
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
	watermillpub "github.com/kartaladev/wrkflw/internal/eventing/watermill"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"context"
)

// Compile-time guard: the internal adapter satisfies the public port.
var _ kernel.OutboxPublisher = (*watermillpub.Publisher)(nil)

// Option configures a publisher, an in-process bus, or a chaining runner.
type Option func(*options)

type options struct {
	logger     *slog.Logger
	tp         trace.TracerProvider
	mp         metric.MeterProvider
	propagator propagation.TextMapPropagator
}

// newOptions applies opts over the package defaults, so every constructor
// resolves "unset" the same way and no caller has to nil-check.
func newOptions(opts ...Option) options {
	o := options{
		logger: slog.Default(),
		tp:     otel.GetTracerProvider(),
		mp:     otel.GetMeterProvider(),
		// NOT otel.GetTextMapPropagator(). The default global propagator is a
		// no-op: unless the deployment calls otel.SetTextMapPropagator, it has no
		// Fields() and injects nothing, so a publisher reaching for it would write
		// no trace context at all — silently, and only in production, since a test
		// that sets the global would pass. This package states which keys it
		// writes, so it names the propagator that writes them. Override with
		// WithPropagator; an inbound HTTP server is the opposite case and should
		// honour the deployment's global instead.
		propagator: propagation.TraceContext{},
	}
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// WithLogger sets the structured logger (default slog.Default()). A nil logger
// is ignored.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithTracerProvider sets the tracer provider (default: the otel global).
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(o *options) {
		if tp != nil {
			o.tp = tp
		}
	}
}

// WithMeterProvider sets the meter provider (default: the otel global).
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(o *options) {
		if mp != nil {
			o.mp = mp
		}
	}
}

// WithPropagator sets the OpenTelemetry propagator used to write trace context
// into Envelope.Metadata on publish, and to rebuild a handler's context from it
// on delivery. Default: propagation.TraceContext{} — the W3C traceparent /
// tracestate pair — chosen explicitly rather than taken from the otel global,
// which is a no-op until a deployment sets it. Pass this to match a deployment
// that propagates something else (B3, Jaeger, a composite). A nil propagator is
// ignored.
func WithPropagator(p propagation.TextMapPropagator) Option {
	return func(o *options) {
		if p != nil {
			o.propagator = p
		}
	}
}

// NewGoChannelPublisher builds an in-process GoChannel pub/sub and returns a
// kernel.OutboxPublisher over it, the matching Subscriber (for in-process consumers
// or tests), and an io.Closer to release it. No external broker is required.
// GoChannel ships in watermill core, so this adds no broker dependency.
func NewGoChannelPublisher(opts ...Option) (kernel.OutboxPublisher, message.Subscriber, io.Closer) {
	o := newOptions(opts...)
	gc := gochannel.NewGoChannel(gochannel.Config{}, watermillpub.NewWatermillLogger(o.logger))
	publish := func(ctx context.Context, env Envelope) error {
		msg := message.NewMessage(env.ID, env.Body)
		for k, v := range env.Metadata {
			msg.Metadata.Set(k, v)
		}
		msg.SetContext(ctx)
		return gc.Publish(env.Topic, msg)
	}
	return NewPublisher(publish, opts...), gc, gc
}
