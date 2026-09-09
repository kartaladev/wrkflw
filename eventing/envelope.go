package eventing

import "context"

// Metadata keys carried by every Envelope that [NewPublisher] produces. They are
// the routing contract between the publish side and the handlers in this package
// ([NewChainHandler], [NewMessageHandler]); a consumer wiring its own broker
// subscriber reads the same keys.
const (
	// MetaTopic repeats Envelope.Topic inside the metadata, so a handler mounted
	// on a fan-in subscription can still tell which topic an envelope arrived on.
	MetaTopic = "topic"
	// MetaInstanceID is the process-instance id: a partition/ordering key for
	// brokers that key on metadata (Kafka, NATS).
	MetaInstanceID = "instance_id"
	// MetaDefinitionRef is the source instance's "id:version" definition
	// reference. It is routing context, not authoritative — see [NewChainHandler].
	MetaDefinitionRef = "definition_ref"
)

// Envelope is the broker-agnostic wire shape of one published domain event: the
// single value this package hands to a [PublishFunc] and delivers to a [Handler].
// It is a plain struct on purpose — no broker library appears in its type, so a
// consumer maps it onto whatever client they already run.
//
// Metadata is a map[string]string precisely so it doubles as an OpenTelemetry
// text-map carrier: [NewPublisher] injects the W3C trace context into it, and a
// subscriber rebuilds the handler's context from it. See [WithPropagator].
type Envelope struct {
	// ID is the outbox row's dedup key when the event carries one, and a
	// generated id otherwise. Consumers key idempotency on it.
	//
	// The generated form is idgen.XID — 20 characters of base32hex built from a
	// timestamp, machine id, process id and counter. It is unique and
	// k-sortable, NOT random and NOT unguessable. Never treat an Envelope.ID as
	// a token, a capability, or anything an authorization decision reads.
	//
	// That branch is in practice unreachable from the relay: wrkflw_outbox's
	// dedup_key column is NOT NULL UNIQUE in every dialect, so an event the
	// relay drains always arrives with one. It exists for a caller driving
	// Publish directly.
	ID string
	// Topic is the destination topic, e.g. "instance.completed" or
	// "message.OrderPlaced".
	Topic string
	// Metadata carries the routing keys above plus any trace-context keys the
	// configured propagator writes. Never nil for an envelope this package built.
	Metadata map[string]string
	// Body is the JSON encoding of the outbox event payload.
	Body []byte
}

// Handler processes one delivered [Envelope]. It is the shape both
// [NewChainHandler] and [NewMessageHandler] return, and the shape a
// [Subscriber] calls.
//
// The ctx a Handler receives is NOT the publisher's context — a real broker
// hands a consumer bytes and headers, never a Go value. It is rebuilt on the
// delivery side from Envelope.Metadata (see [WithPropagator]), so a span the
// handler starts parents onto the publish span across a process boundary.
//
// The returned error is the ack/nack decision, and the whole of it: nil acks
// (the envelope is done), non-nil nacks (the envelope is re-delivered). Ack a
// poison payload — a body that will never decode — or the broker loops on it
// forever; nack only what a retry could fix.
type Handler func(ctx context.Context, env Envelope) error

// Subscriber delivers the envelopes published to one topic to a [Handler].
//
// Subscribe BLOCKS: it owns the delivery loop and returns only when ctx is done
// or the subscription is closed, so the caller decides which goroutine the loop
// runs on. Implement it over your own broker's consumer to reuse this package's
// handlers; [NewInProcess] is the built-in implementation.
type Subscriber interface {
	Subscribe(ctx context.Context, topic string, h Handler) error
}

// Starter is the READINESS half of a [Subscriber]: it registers a subscription
// and returns only once that subscription is LIVE, so a publish issued after it
// returns cannot be dropped for want of a subscriber. The returned stop function
// ends the subscription and waits for its delivery loop to finish.
//
// This is a separate interface rather than a method on [Subscriber] because the
// two express different things and not every broker can offer both. Subscribe
// BLOCKS and registers somewhere inside itself, so a caller has no edge to
// sequence against — which is precisely why [Chainer.Run] cannot promise
// readiness and [Chainer.Start] can. A broker whose consumer registration is
// synchronous can implement this; one whose registration completes
// asynchronously should not pretend to, because a Starter that returns before
// the subscription is live re-creates the very false guarantee it exists to
// remove.
//
// [NewInProcess] implements it via [InProcess.Start].
type Starter interface {
	Start(ctx context.Context, topic string, h Handler) (stop func(), err error)
}
