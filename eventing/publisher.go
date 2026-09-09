package eventing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/kartaladev/wrkflw/runtime/idgen"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

const instrumentationName = "github.com/kartaladev/wrkflw/eventing"

// isBenignBusShutdown reports whether err is (or wraps) [ErrBusClosed] — an
// in-process bus refusing a publish after Close. A relay draining concurrently
// with a graceful shutdown reaches this by design, so it is logged at DEBUG
// rather than ERROR. It is the outbound twin of isBenignDriverShutdown, which
// demotes the same category on the consuming side; without it a clean shutdown
// emits ERROR alarms for an entirely expected condition.
func isBenignBusShutdown(err error) bool {
	return errors.Is(err, ErrBusClosed)
}

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
// generated id when empty — see [Envelope.ID] for what that is and why the
// relay never needs it) so redeliveries are deduplicable, and the instance id is
// set as metadata for per-instance partitioning/ordering. Each Publish call emits
// one OTel span and increments wrkflw_eventing_published_total.
type publisher struct {
	publish    PublishFunc
	logger     *slog.Logger
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
	published  metric.Int64Counter
	// ids mints an envelope id for an outbox event that carries no dedup key.
	// It is the repo's own generator (idgen.XID), which is what keeps this
	// package's direct imports free of any third-party name — see
	// TestEventingDependencyGraphNamesNoVendorDirectly. Its output is unique and
	// k-sortable, not random; see [Envelope.ID].
	ids idgen.Generator
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
		publish:    publish,
		logger:     o.logger,
		tracer:     o.tp.Tracer(instrumentationName),
		propagator: o.propagator,
		published:  counter,
		ids:        idgen.XID(),
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
		// Unreachable from the relay — wrkflw_outbox.dedup_key is NOT NULL
		// UNIQUE in every dialect — so this serves a caller driving Publish
		// directly. The id is unique and k-sortable, NOT unguessable; see
		// Envelope.ID before keying anything on it.
		generated, err := p.ids.NewID()
		if err != nil {
			return fmt.Errorf("workflow-eventing: mint envelope id: %w", err)
		}
		id = generated
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
	// Trace context travels IN the envelope, not alongside it in a Go context: a
	// real broker hands the consumer bytes and headers, never the publisher's
	// ctx. Metadata is a map[string]string, which is propagation.MapCarrier
	// exactly, so no adapter is needed. ctx here is the publish span's context,
	// so a consumer that extracts these keys parents onto eventing.publish.
	p.propagator.Inject(ctx, propagation.MapCarrier(env.Metadata))

	if err := p.publish(ctx, env); err != nil {
		// Mirror of the inbound discipline in isBenignDriverShutdown: a publish
		// refused because the bus is closing is the expected shape of a graceful
		// shutdown, not an incident. The error is still returned — the relay
		// leaves the outbox row pending and retries — but logging it at ERROR
		// would fill a shutdown with alarms for something working as designed.
		// ErrNoSubscription deliberately does NOT join isBenignBusShutdown's DEBUG
		// path and takes the ERROR branch below. The two look alike — both mean
		// "not delivered" — but they differ in what they say about the system.
		//
		// ErrBusClosed is a graceful shutdown racing a drain: expected, bounded,
		// self-correcting, nobody to wake. ErrNoSubscription is DATA LOSS that was
		// converted into a retry — an outbox row is now pending and will
		// dead-letter unless something starts consuming that topic. It is returned
		// by default, not only when a consumer opted in, precisely because it is
		// the condition nobody would think to ask about; demoting it to DEBUG
		// would restore the silence this whole change exists to break.
		if isBenignBusShutdown(err) {
			p.logger.DebugContext(ctx, "eventing: publish refused; bus is closing",
				slog.String("topic", ev.Topic), slog.String("instance_id", ev.InstanceID))
		} else {
			p.logger.ErrorContext(ctx, "eventing: publish failed",
				slog.String("topic", ev.Topic), slog.String("instance_id", ev.InstanceID),
				slog.Any("error", err))
		}
		return fmt.Errorf("workflow-eventing: publish topic=%q: %w", ev.Topic, err)
	}

	p.logger.DebugContext(ctx, "eventing: published",
		slog.String("topic", ev.Topic), slog.String("instance_id", ev.InstanceID),
		slog.String("dedup_key", ev.DedupKey))
	return nil
}
