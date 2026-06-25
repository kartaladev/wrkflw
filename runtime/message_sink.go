package runtime

import "context"

// OutboundMessage is a message emitted by a KindSendTask node. It carries the
// sending instance's ID, the message name, the resolved correlation key (empty
// when the SendTask declared no correlation-key expression), and a copy of the
// instance variables at the time of the send.
type OutboundMessage struct {
	// InstanceID identifies the sending process instance.
	InstanceID string
	// Name is the message reference (the SendTask's MessageName).
	Name string
	// CorrelationKey is the resolved correlation key, or "" when none was set.
	CorrelationKey string
	// Payload is a copy of the sending instance's variables. May be nil.
	Payload map[string]any
}

// MessageSink is the consumer-wired port through which a SendTask emits an
// outbound message. It is pluggable so a consumer can choose how messages are
// routed: intra-engine delivery to a parked ReceiveTask (e.g. via
// [Runner.DeliverMessage]), publication to an external broker or the eventing
// outbox, or both (ADR-0060).
//
// Send is invoked synchronously while the runner performs the SendMessage
// command. A non-nil error is surfaced to the caller of [Runner.Run] /
// [Runner.Deliver], so the sink should be idempotent: the same OutboundMessage
// may be re-delivered if the step is retried after a transient failure.
type MessageSink interface {
	Send(ctx context.Context, msg OutboundMessage) error
}
