package eventing

import "github.com/kartaladev/wrkflw/runtime/kernel"

// The topics the wrkflw runtime writes to the transactional outbox. Naming them
// here means a consumer wiring a broker subscription, a router, or a DLQ rule
// refers to the same identifiers this package's handlers route on, instead of
// re-spelling the strings. The runtime writes the literals in
// runtime/outbox.go's terminalOutboxEvent and outboundMessageEvents;
// TestTopicConstantsMatchWhatTheRuntimeWrites drives real instances and checks
// these constants against what actually lands in the outbox.
const (
	// TopicInstanceCompleted is published when an instance reaches
	// engine.StatusCompleted. Its payload is the terminal variables.
	TopicInstanceCompleted = "instance.completed"
	// TopicInstanceFailed is published when an instance reaches
	// engine.StatusFailed. Its payload is {"error": …}.
	TopicInstanceFailed = "instance.failed"
	// TopicInstanceTerminated is published when an instance reaches
	// engine.StatusTerminated — a cancellation or an admin full rollback. Its
	// payload is {"error": …}.
	TopicInstanceTerminated = "instance.terminated"
	// TopicMessagePrefix prefixes the per-message topic a SendTask emits:
	// TopicMessagePrefix + the message name, e.g. "message.OrderPlaced".
	TopicMessagePrefix = "message."
)

// chainTopics are the three status-accurate terminal topics a chaining consumer
// subscribes. The map also drives topic→Outcome projection.
var chainTopics = map[string]kernel.ChainOutcome{
	TopicInstanceCompleted:  kernel.OutcomeCompleted,
	TopicInstanceFailed:     kernel.OutcomeFailed,
	TopicInstanceTerminated: kernel.OutcomeTerminated,
}

// chainTopicOrder fixes the subscription order of chainTopics. Ranging a map
// would subscribe in a different order on every run, which makes a partial
// failure non-reproducible in both Chainer.Run and Chainer.Start — and for
// Start the order is load-bearing rather than merely convenient, because it
// decides which subscriptions are already live when one fails and therefore
// which its teardown must stop.
var chainTopicOrder = []string{
	TopicInstanceCompleted,
	TopicInstanceFailed,
	TopicInstanceTerminated,
}
