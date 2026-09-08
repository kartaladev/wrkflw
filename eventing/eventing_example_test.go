package eventing_test

import (
	"context"
	"fmt"

	"github.com/kartaladev/wrkflw/eventing"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// ExampleNewInProcess shows publishing an outbox event with no broker at all and
// receiving it on a subscription. Start returns once the subscription is live,
// which is what makes the publish below safe: the bus is non-persistent, so an
// envelope published to a topic nobody has subscribed yet is dropped.
func ExampleNewInProcess() {
	bus := eventing.NewInProcess()
	defer func() { _ = bus.Close() }()

	ctx := context.Background()
	received := make(chan eventing.Envelope, 1)
	stop, err := bus.Start(ctx, eventing.TopicInstanceCompleted,
		func(_ context.Context, env eventing.Envelope) error {
			received <- env
			return nil
		})
	if err != nil {
		panic(err)
	}
	defer stop()

	_ = bus.Publish(ctx, kernel.OutboxEvent{
		Topic:      eventing.TopicInstanceCompleted,
		Payload:    map[string]any{"order": "A-1"},
		DedupKey:   "inst-1:1:0",
		InstanceID: "inst-1",
	})

	env := <-received
	fmt.Println(env.Metadata[eventing.MetaInstanceID], string(env.Body))
	// Output: inst-1 {"order":"A-1"}
}
