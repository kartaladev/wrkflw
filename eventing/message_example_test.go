package eventing_test

import (
	"context"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/eventing"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// ExampleNewMessageHandler shows wiring a message.* subscription to intra-engine delivery.
func ExampleNewMessageHandler() {
	// Given a runner and the receiver definition (the process that has a ReceiveTask):
	var runner *runtime.ProcessDriver
	var receiverDef *model.ProcessDefinition

	handler := eventing.NewMessageHandler(func(ctx context.Context, name, key string, payload map[string]any) error {
		// Route intra-engine: wake the instance parked on (name, key) in receiverDef.
		return runner.DeliverMessage(ctx, receiverDef, name, key, payload)
	})

	// Mount handler on your message.Router for the "message.<Name>" topics you consume,
	// subscribing the same broker the persistence.Relay publishes to.
	_ = handler
}
