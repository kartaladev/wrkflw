package eventing_test

import (
	"github.com/kartaladev/wrkflw/eventing"
	"github.com/kartaladev/wrkflw/runtime"
)

// ExampleNewMessageHandler shows wiring a message.* subscription to intra-engine delivery.
func ExampleNewMessageHandler() {
	// Given a driver — the receiver definition is resolved by the driver itself
	// (correlate to a running instance, or start from a message-start event), so
	// DeliverMessage's signature matches MessageDeliverFunc and can be passed as a
	// method value directly (ADR-0121):
	var driver *runtime.ProcessDriver

	handler := eventing.NewMessageHandler(driver.DeliverMessage)

	// Mount handler on your message.Router for the "message.<Name>" topics you consume,
	// subscribing the same broker the persistence.Relay publishes to.
	_ = handler
}
