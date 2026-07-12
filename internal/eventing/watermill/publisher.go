package watermill

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/kartaladev/wrkflw/eventing"

// Publisher adapts a watermill message.Publisher to kernel.OutboxPublisher. It maps
// one OutboxEvent to one watermill message: the message UUID is the event's
// DedupKey (or a fresh UUID when empty) so redeliveries are deduplicable, and
// the instance id is set as metadata for per-instance partitioning/ordering.
// Each Publish call emits one OTel span and increments wrkflw_eventing_published_total.
type Publisher struct {
	pub       message.Publisher
	logger    *slog.Logger
	tracer    trace.Tracer
	published metric.Int64Counter
}

// Compile-time check.
var _ kernel.OutboxPublisher = (*Publisher)(nil)

// NewPublisher wraps a watermill message.Publisher.
func NewPublisher(pub message.Publisher, opts ...Option) *Publisher {
	cfg := config{logger: slog.Default()}
	for _, o := range opts {
		o(&cfg)
	}
	tp := cfg.tp
	if tp == nil {
		tp = otel.GetTracerProvider()
	}
	mp := cfg.mp
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	counter, err := mp.Meter(instrumentationName).Int64Counter(
		"wrkflw_eventing_published_total",
		metric.WithDescription("Count of outbox events published to the broker."),
	)
	if err != nil {
		// Never fail construction over a metric; fall back to a no-op counter.
		counter, _ = metricnoop.NewMeterProvider().Meter(instrumentationName).Int64Counter("wrkflw_eventing_published_total")
		cfg.logger.Warn("eventing: counter init failed; using no-op", slog.Any("error", err))
	}
	return &Publisher{
		pub:       pub,
		logger:    cfg.logger,
		tracer:    tp.Tracer(instrumentationName),
		published: counter,
	}
}

// Publish maps ev to a watermill message, publishes it to ev.Topic, and emits
// an OTel span and counter increment for each call.
func (p *Publisher) Publish(ctx context.Context, ev kernel.OutboxEvent) error {
	ctx, span := p.tracer.Start(ctx, "eventing.publish", trace.WithAttributes(
		attribute.String("messaging.destination", ev.Topic),
		attribute.String("wrkflw.instance_id", ev.InstanceID),
	))
	defer span.End()

	err := p.publish(ctx, ev)
	status := "ok"
	if err != nil {
		status = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	p.published.Add(ctx, 1, metric.WithAttributes(attribute.String("status", status)))
	return err
}

// publish is the core marshal+publish logic; it is called by Publish after the
// span has been started. The active span context propagates through ctx into
// msg.SetContext so downstream systems can extract it.
func (p *Publisher) publish(ctx context.Context, ev kernel.OutboxEvent) error {
	payload, err := json.Marshal(ev.Payload)
	if err != nil {
		p.logger.ErrorContext(ctx, "eventing: marshal payload failed",
			slog.String("topic", ev.Topic), slog.Any("error", err))
		return fmt.Errorf("workflow-eventing: marshal payload: %w", err)
	}

	id := ev.DedupKey
	if id == "" {
		id = watermill.NewUUID()
	}
	msg := message.NewMessage(id, payload)
	msg.Metadata.Set("topic", ev.Topic)
	msg.Metadata.Set("instance_id", ev.InstanceID)
	msg.Metadata.Set("definition_ref", ev.DefinitionRef.String())
	msg.SetContext(ctx)

	if err := p.pub.Publish(ev.Topic, msg); err != nil {
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
