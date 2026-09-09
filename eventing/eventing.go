// Package eventing is the consumer-facing façade for publishing wrkflw domain
// events to a message broker, and for consuming them back.
//
// wrkflw does not choose your broker, import its client, or appear in its
// configuration. The whole surface between this package and the outside world is
// two function types and a struct:
//
//   - [Envelope] — one published event as id, topic, string metadata and a JSON
//     body. No messaging library appears in it.
//   - [PublishFunc] — func(context.Context, Envelope) error. Write one over the
//     client you already run, wrap it with [NewPublisher], and hand the result to
//     persistence.NewRelay as a kernel.OutboxPublisher.
//   - [Handler] — the same shape in the other direction, for consuming. Mount
//     [NewChainHandler] or [NewMessageHandler] on your own subscription, or on a
//     [Subscriber].
//
// So reaching Kafka, NATS, Redis Streams or a SQL queue is a function you write,
// not an adapter this package ships and has to keep current.
// TestEventingDependencyGraphNamesNoVendorDirectly holds that line: nothing in
// this package's own imports names a third-party module beyond OpenTelemetry.
//
// # No broker at all
//
// [NewInProcess] is a complete in-memory pub/sub bus — publisher, subscriber and
// closer in one value — for tests, examples and single-process deployments. Read
// its doc comment before relying on it: like every broker-less bus it is
// non-persistent, so an envelope published to a topic nobody has subscribed yet
// is dropped — and Publish still returns nil, which behind an outbox relay marks
// the row published. [WithRequireSubscription] turns that particular drop into
// an error instead; it is opt-in, and the option documents both why and which
// half of the problem it does not reach.
//
// # Trace context
//
// [NewPublisher] injects W3C trace context into Envelope.Metadata, and a
// subscriber rebuilds the handler's context from it, so a span the handler
// starts is a child of the publish span across a process boundary. The
// propagator defaults to propagation.TraceContext{} rather than the
// OpenTelemetry global, which is a no-op until a deployment sets it — see
// [WithPropagator].
//
// # Process-instance chaining
//
// The subscriber side of process-instance chaining lives here so runtime keeps
// no messaging concerns: [NewChainHandler] adapts a runtime.Chainer to a
// [Handler] you mount on your own subscription, and [NewChainerRunner] /
// [Chainer.Run] is a turnkey wrapper that subscribes the three status-accurate
// terminal topics ([TopicInstanceCompleted], [TopicInstanceFailed],
// [TopicInstanceTerminated]) and drives the chaining core.
package eventing

import (
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Option configures a publisher, an in-process bus, or a chaining runner.
type Option func(*options)

type options struct {
	logger            *slog.Logger
	tp                trace.TracerProvider
	mp                metric.MeterProvider
	propagator        propagation.TextMapPropagator
	redeliveryBackoff time.Duration

	// requireSubscription is the opt-in from [WithRequireSubscription];
	// requireSubscriptionTopics narrows it, and nil-with-the-flag-set means
	// every topic. Two fields rather than a nil-vs-empty slice convention,
	// because "strict everywhere" and "strict nowhere" must not be the same
	// zero value.
	requireSubscription       bool
	requireSubscriptionTopics []string
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
		propagator:        propagation.TraceContext{},
		redeliveryBackoff: defaultRedeliveryBackoff,
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
//
// MIND WHAT A COMPOSITE CARRIES. Whatever the propagator writes goes into
// Envelope.Metadata and travels to the broker in cleartext, so composing
// propagation.Baggage{} ships the process's OpenTelemetry baggage with every
// event — commonly tenant ids, user ids and feature flags, to an operator who
// may be a third party and to every consumer of the topic. The default writes
// the W3C traceparent/tracestate pair and nothing else. Add baggage only when
// you know what is in it and where the topic goes.
func WithPropagator(p propagation.TextMapPropagator) Option {
	return func(o *options) {
		if p != nil {
			o.propagator = p
		}
	}
}

// WithRedeliveryBackoff sets how long [NewInProcess]'s bus waits before handing a
// nacked envelope back to the same handler (default 10ms). It is paid on every
// retry, so it wants to stay short; raise it when a handler's failures are worth
// pacing. A non-positive duration is ignored.
func WithRedeliveryBackoff(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.redeliveryBackoff = d
		}
	}
}

// WithRequireSubscription escalates the named topics to strict: a publish to one
// of them is refused with [ErrNoSubscription] whenever it has no live
// subscription, INCLUDING before anything has ever subscribed it. With no
// arguments it applies to every topic.
//
// This is not the switch that enables [ErrNoSubscription] — that is on by
// default. [NewInProcess] already refuses a publish to a topic that HAS been
// subscribed and currently is not, which is the silent-loss defect: the consumer
// was listening, stopped, and the relay went on marking outbox rows published.
// What this option adds is the first-publish window, for a topic that must
// always have a live subscriber even before one has registered — a startup race
// where the relay drains before the consumer subscribes would otherwise pass
// unreported.
//
// Reach for it when a topic is load-bearing and you would rather have a retrying
// outbox row than a silent success. Leave it alone for topics that legitimately
// have no in-process consumer: naming those makes every publish to them an
// error, which is how a deployment ends up dead-lettering rows nobody wanted.
func WithRequireSubscription(topics ...string) Option {
	return func(o *options) {
		o.requireSubscription = true
		o.requireSubscriptionTopics = append(o.requireSubscriptionTopics, topics...)
	}
}
