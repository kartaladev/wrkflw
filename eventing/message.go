package eventing

import (
	"context"
	"encoding/json"
	"log/slog"
)

// MessageDeliverFunc routes a decoded outbound SendTask message to its receiver.
// Its signature matches [runtime.ProcessDriver.DeliverMessage] exactly, so a
// consumer can pass that method value directly — the driver resolves the target
// definition itself (correlate to a running instance or start from a
// message-start event), so no receiver definition needs pre-capturing.
type MessageDeliverFunc func(ctx context.Context, name, correlationKey string, payload map[string]any) error

// messageBody is the wire shape of a message.<Name> outbox event payload.
type messageBody struct {
	MessageName    string         `json:"messageName"`
	CorrelationKey string         `json:"correlationKey"`
	Variables      map[string]any `json:"variables"`
}

// NewMessageHandler adapts a message.* subscription to a MessageDeliverFunc. A
// consumer mounts it on their own broker subscription for the [TopicMessagePrefix]
// topics they care about (their retry/poison/DLQ middleware wraps it). It decodes
// the envelope body and routes the message to deliver.
//
// Ack/Nack discipline (a returned error nacks for re-delivery):
//   - delivered (or no-op: no waiter) → nil (ack)
//   - malformed JSON / empty message name → nil (ack + log; never loop on poison)
//   - transient deliver failure → error (nack → re-delivered)
func NewMessageHandler(deliver MessageDeliverFunc, opts ...Option) Handler {
	logger := newOptions(opts...).logger
	return func(ctx context.Context, env Envelope) error {
		var body messageBody
		if len(env.Body) > 0 {
			if err := json.Unmarshal(env.Body, &body); err != nil {
				logger.WarnContext(ctx, "message: malformed payload; acking",
					slog.String("topic", envelopeTopic(env)),
					slog.String("instance_id", env.Metadata[MetaInstanceID]),
					slog.Any("error", err))
				return nil
			}
		}
		if body.MessageName == "" {
			return nil // not a decodable message event; ack and ignore
		}
		return deliver(ctx, body.MessageName, body.CorrelationKey, body.Variables)
	}
}
