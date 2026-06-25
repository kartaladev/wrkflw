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
// command — but, crucially, AFTER the (often already terminal/Completed)
// auto-advanced instance state has been committed. The order is
// Step -> Commit(state) -> perform(Send), the same commit-before-perform shape
// as ThrowSignal. A non-nil error is surfaced to the caller of [Runner.Run] /
// [Runner.Deliver], yet by then the instance has already durably advanced, so
// the failed message is NOT re-delivered: SendTask is best-effort and a sink
// error strands the message (it is silently never sent), it does not cause a
// double-send.
//
// If the consumer needs atomic / at-least-once delivery, wire this sink to the
// transactional outbox — write the outbound message in the SAME transaction as
// the state commit — so the relayer guarantees the message is eventually sent
// instead of being lost on a transient sink failure.
type MessageSink interface {
	Send(ctx context.Context, msg OutboundMessage) error
}
