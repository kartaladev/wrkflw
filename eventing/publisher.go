package eventing

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"

	"github.com/kartaladev/wrkflw/runtime/kernel"
)

const instrumentationName = "github.com/kartaladev/wrkflw/eventing"

// PublishFunc delivers one [Envelope] to a broker. It is the single seam between
// wrkflw and whatever messaging client a consumer already runs: implement it over
// a Kafka writer, a NATS connection, a Redis Streams client, an HTTP call — the
// function receives a plain struct and this package imports nothing of yours.
//
// Contract: return nil only once the broker has accepted the envelope. A returned
// error is surfaced to the outbox relay, which leaves the row pending and retries
// it with backoff, so delivery stays at-least-once.
type PublishFunc func(ctx context.Context, env Envelope) error

// publisher adapts a [PublishFunc] to [kernel.OutboxPublisher]. It maps one
// OutboxEvent to one Envelope: the envelope id is the event's DedupKey (or a
// fresh UUID when empty) so redeliveries are deduplicable, and the instance id is
// set as metadata for per-instance partitioning/ordering. Each Publish call emits
// one OTel span and increments wrkflw_eventing_published_total.
type publisher struct {
	publish    PublishFunc
	logger    *slog.Logger
	tracer    trace.Tracer
	published metric.Int64Counter
}

// Compile-time check: the façade satisfies the engine-side port.
var _ kernel.OutboxPublisher = (*publisher)(nil)

// NewPublisher adapts publish to a [kernel.OutboxPublisher] that the persistence
// relay drives. Hand the result to persistence.NewRelay.
//
// For an in-process bus with no broker at all, use [NewInProcess] instead.
func NewPublisher(publish PublishFunc, opts ...Option) kernel.OutboxPublisher {
	o := newOptions(opts...)
	counter, err := o.mp.Meter(instrumentationName).Int64Counter(
		"wrkflw_eventing_published_total",
		metric.WithDescription("Count of outbox events published to the broker."),
	)
	if err != nil {
		// Never fail construction over a metric; fall back to a no-op counter.
		counter, _ = metricnoop.NewMeterProvider().Meter(instrumentationName).Int64Counter("wrkflw_eventing_published_total")
		o.logger.Warn("eventing: counter init failed; using no-op", slog.Any("error", err))
	}
	return &publisher{
		publish:   publish,
		logger:    o.logger,
		tracer:    o.tp.Tracer(instrumentationName),
		published: counter,
	}
}

// Publish maps ev to an [Envelope], hands it to the PublishFunc, and emits an
// OTel span and counter increment for each call.
func (p *publisher) Publish(ctx context.Context, ev kernel.OutboxEvent) error {
	ctx, span := p.tracer.Start(ctx, "eventing.publish", trace.WithAttributes(
		attribute.String("messaging.destination", ev.Topic),
		attribute.String("wrkflw.instance_id", ev.InstanceID),
	))
	defer span.End()

	err := p.publishOne(ctx, ev)
	status := "ok"
	if err != nil {
		status = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	p.published.Add(ctx, 1, metric.WithAttributes(attribute.String("status", status)))
	return err
}

// publishOne is the core marshal+publish logic, called by Publish after the span
// has been started so the envelope carries the publish span as its trace parent.
func (p *publisher) publishOne(ctx context.Context, ev kernel.OutboxEvent) error {
	payload, err := json.Marshal(ev.Payload)
	if err != nil {
		p.logger.ErrorContext(ctx, "eventing: marshal payload failed",
			slog.String("topic", ev.Topic), slog.Any("error", err))
		return fmt.Errorf("workflow-eventing: marshal payload: %w", err)
	}

	id := ev.DedupKey
	if id == "" {
		id = uuid.NewString()
	}
	env := Envelope{
		ID:    id,
		Topic: ev.Topic,
		Metadata: map[string]string{
			MetaTopic:         ev.Topic,
			MetaInstanceID:    ev.InstanceID,
			MetaDefinitionRef: ev.DefinitionRef.String(),
		},
		Body: payload,
	}

	if err := p.publish(ctx, env); err != nil {
		p.logger.ErrorContext(ctx, "eventing: publish failed",
			slog.String("topic", ev.Topic), slog.String("instance_id", ev.InstanceID),
			slog.Any("error", err))
		return fmt.Errorf("workflow-eventing: publish topic=%q: %w", ev.Topic, err)
	}

	p.logger.DebugContext(ctx, "eventing: published",
		slog.String("topic", ev.Topic), slog.String("instance_id", ev.InstanceID),
		slog.String("dedup_key", ev.DedupKey))
	return nil
}
