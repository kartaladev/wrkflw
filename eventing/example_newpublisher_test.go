package eventing_test

import (
	"context"
	"fmt"

	"github.com/ThreeDotsLabs/watermill/message"

	"github.com/zakyalvan/krtlwrkflw/eventing"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// capturePublisher is a trivial watermill message.Publisher that records the
// messages it is handed, standing in for a real broker publisher (Kafka, NATS,
// Redis Streams, watermill-SQL, …). It exists only to make this example runnable
// without a broker dependency.
type capturePublisher struct {
	topics []string
	msgs   []*message.Message
}

func (c *capturePublisher) Publish(topic string, msgs ...*message.Message) error {
	for _, m := range msgs {
		c.topics = append(c.topics, topic)
		c.msgs = append(c.msgs, m)
	}
	return nil
}

func (c *capturePublisher) Close() error { return nil }

// ExampleNewPublisher shows how a consumer reaches any external broker: wrap the
// broker's watermill message.Publisher with eventing.NewPublisher and hand the
// result to persistence.NewRelay. Every published message carries the
// process-instance id as metadata (for partition/ordering) and uses the outbox
// dedup key as its UUID (for at-least-once dedup on the consumer).
func ExampleNewPublisher() {
	// In production this is kafka.NewPublisher(...) / nats.NewPublisher(...) / etc.
	broker := &capturePublisher{}

	pub := eventing.NewPublisher(broker)

	// The outbox relay calls Publish for each drained row; here we publish one
	// event directly to show the mapping.
	_ = pub.Publish(context.Background(), kernel.OutboxEvent{
		Topic:         "instance.completed",
		InstanceID:    "order-42",
		DefinitionRef: "order-flow:1",
		DedupKey:      "order-42:3:0",
		Payload:       map[string]any{"status": "completed"},
	})

	m := broker.msgs[0]
	fmt.Println("topic:", broker.topics[0])
	fmt.Println("uuid:", m.UUID)
	fmt.Println("instance_id:", m.Metadata.Get("instance_id"))
	fmt.Println("definition_ref:", m.Metadata.Get("definition_ref"))
	// Output:
	// topic: instance.completed
	// uuid: order-42:3:0
	// instance_id: order-42
	// definition_ref: order-flow:1
}
