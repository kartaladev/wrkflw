package eventing

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
	// ID is the outbox row's dedup key when the event carries one, and a fresh
	// UUID otherwise. Consumers key idempotency on it.
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
