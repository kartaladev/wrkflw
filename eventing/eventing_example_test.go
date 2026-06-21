package eventing_test

import (
	"context"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/eventing"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// ExampleNewGoChannelPublisher shows publishing an outbox event in-process and
// receiving it on the subscriber side.
func ExampleNewGoChannelPublisher() {
	pub, sub, closer := eventing.NewGoChannelPublisher()
	defer closer.Close()

	ctx := context.Background()
	msgs, _ := sub.Subscribe(ctx, "instance.completed")

	_ = pub.Publish(ctx, runtime.OutboxEvent{
		Topic:      "instance.completed",
		Payload:    map[string]any{"order": "A-1"},
		DedupKey:   "inst-1:1:0",
		InstanceID: "inst-1",
	})

	msg := <-msgs
	fmt.Println(msg.Metadata.Get("instance_id"), string(msg.Payload))
	msg.Ack()
	// Output: inst-1 {"order":"A-1"}
}
