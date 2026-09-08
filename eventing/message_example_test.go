package eventing_test

import (
	"github.com/kartaladev/wrkflw/eventing"
	"github.com/kartaladev/wrkflw/runtime"
)

// ExampleNewMessageHandler shows wiring a message.* subscription to intra-engine
// delivery. See ExampleNewInProcess for a runnable version with a live subscription.
func ExampleNewMessageHandler() {
	// Given a driver — the receiver definition is resolved by the driver itself
	// (correlate to a running instance, or start from a message-start event), so
	// DeliverMessage's signature matches MessageDeliverFunc and can be passed as a
	// method value directly:
	var driver *runtime.ProcessDriver

	handler := eventing.NewMessageHandler(driver.DeliverMessage)

	// handler is an eventing.Handler: func(context.Context, eventing.Envelope) error.
	// Mount it on your own broker subscription for the TopicMessagePrefix + "<Name>"
	// topics you consume — the same broker persistence.Relay publishes to — or on an
	// eventing.Subscriber such as eventing.NewInProcess().
	_ = handler
}
