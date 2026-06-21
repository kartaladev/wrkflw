package watermill

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Publisher adapts a watermill message.Publisher to runtime.Publisher. It maps
// one OutboxEvent to one watermill message: the message UUID is the event's
// DedupKey (or a fresh UUID when empty) so redeliveries are deduplicable, and
// the instance id is set as metadata for per-instance partitioning/ordering.
type Publisher struct {
	pub    message.Publisher
	logger *slog.Logger
}

// Compile-time check.
var _ runtime.Publisher = (*Publisher)(nil)

// NewPublisher wraps a watermill message.Publisher.
func NewPublisher(pub message.Publisher, opts ...Option) *Publisher {
	cfg := config{logger: slog.Default()}
	for _, o := range opts {
		o(&cfg)
	}
	return &Publisher{pub: pub, logger: cfg.logger}
}

// Publish maps ev to a watermill message and publishes it to ev.Topic.
func (p *Publisher) Publish(ctx context.Context, ev runtime.OutboxEvent) error {
	payload, err := json.Marshal(ev.Payload)
	if err != nil {
		p.logger.ErrorContext(ctx, "eventing: marshal payload failed",
			slog.String("topic", ev.Topic), slog.Any("error", err))
		return fmt.Errorf("eventing: marshal payload: %w", err)
	}

	id := ev.DedupKey
	if id == "" {
		id = watermill.NewUUID()
	}
	msg := message.NewMessage(id, payload)
	msg.Metadata.Set("topic", ev.Topic)
	msg.Metadata.Set("instance_id", ev.InstanceID)
	msg.SetContext(ctx)

	if err := p.pub.Publish(ev.Topic, msg); err != nil {
		p.logger.ErrorContext(ctx, "eventing: publish failed",
			slog.String("topic", ev.Topic), slog.String("instance_id", ev.InstanceID),
			slog.Any("error", err))
		return fmt.Errorf("eventing: publish topic=%q: %w", ev.Topic, err)
	}

	p.logger.DebugContext(ctx, "eventing: published",
		slog.String("topic", ev.Topic), slog.String("instance_id", ev.InstanceID),
		slog.String("dedup_key", ev.DedupKey))
	return nil
}
