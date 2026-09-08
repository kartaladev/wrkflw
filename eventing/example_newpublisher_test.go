package eventing_test

import (
	"context"
	"fmt"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/eventing"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// ExampleNewPublisher shows how a consumer reaches any external broker: write a
// PublishFunc that hands one Envelope to the broker client you already run, wrap
// it with eventing.NewPublisher, and give the result to persistence.NewRelay.
// Every envelope carries the process-instance id as metadata (for
// partition/ordering) and uses the outbox dedup key as its ID (for at-least-once
// dedup on the consumer).
func ExampleNewPublisher() {
	// In production the body of this function is one call into kafka.Writer /
	// nats.Conn / redis.Client. Here it just records what it was handed.
	var seen []eventing.Envelope
	publish := func(_ context.Context, env eventing.Envelope) error {
		seen = append(seen, env)
		return nil
	}

	pub := eventing.NewPublisher(publish)

	// The outbox relay calls Publish for each drained row; here we publish one
	// event directly to show the mapping.
	_ = pub.Publish(context.Background(), kernel.OutboxEvent{
		Topic:         "instance.completed",
		InstanceID:    "order-42",
		DefinitionRef: model.Version("order-flow", 1),
		DedupKey:      "order-42:3:0",
		Payload:       map[string]any{"status": "completed"},
	})

	env := seen[0]
	fmt.Println("topic:", env.Topic)
	fmt.Println("id:", env.ID)
	fmt.Println("instance_id:", env.Metadata[eventing.MetaInstanceID])
	fmt.Println("definition_ref:", env.Metadata[eventing.MetaDefinitionRef])
	fmt.Println("body:", string(env.Body))
	// Output:
	// topic: instance.completed
	// id: order-42:3:0
	// instance_id: order-42
	// definition_ref: order-flow:1
	// body: {"status":"completed"}
}
