package eventing

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
)

// MessageDeliverFunc routes a decoded outbound SendTask message to its receiver.
// Its signature matches [runtime.ProcessDriver.DeliverMessage] exactly, so a
// consumer can pass that method value directly — the driver resolves the target
// definition itself (correlate to a running instance or start from a
// message-start event), so no receiver definition needs pre-capturing (ADR-0121).
type MessageDeliverFunc func(ctx context.Context, name, correlationKey string, payload map[string]any) error

// messageBody is the wire shape of a message.<Name> outbox event payload (ADR-0067).
type messageBody struct {
	MessageName    string         `json:"messageName"`
	CorrelationKey string         `json:"correlationKey"`
	Variables      map[string]any `json:"variables"`
}

// NewMessageHandler adapts a message.* outbox subscription to a MessageDeliverFunc. A
// consumer mounts it on their own message.Router for the message topics they care about
// (their retry/poison/DLQ middleware wraps it). It decodes the payload and routes the
// message to deliver.
//
// Ack/Nack discipline (a returned error nacks for re-delivery):
//   - delivered (or no-op: no waiter) → nil (ack)
//   - malformed JSON / empty message name → nil (ack + log; never loop on poison)
//   - transient deliver failure → error (nack → re-delivered)
func NewMessageHandler(deliver MessageDeliverFunc) message.NoPublishHandlerFunc {
	logger := slog.Default()
	return func(msg *message.Message) error {
		var body messageBody
		if len(msg.Payload) > 0 {
			if err := json.Unmarshal(msg.Payload, &body); err != nil {
				logger.WarnContext(msg.Context(), "message: malformed payload; acking",
					slog.String("topic", msg.Metadata.Get("topic")),
					slog.String("instance_id", msg.Metadata.Get("instance_id")),
					slog.Any("error", err))
				return nil
			}
		}
		if body.MessageName == "" {
			return nil // not a decodable message event; ack and ignore
		}
		return deliver(msg.Context(), body.MessageName, body.CorrelationKey, body.Variables)
	}
}
