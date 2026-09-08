package kernel

import "context"

// OutboxPublisher relays one outbox event to the eventing backend. Implementations
// must be idempotent downstream (delivery is at-least-once; the outbox
// dedup_key supports deduplication). The persistence relay calls Publish for
// each claimed unpublished row. No broker is imported here, or anywhere else in
// the tree: eventing.NewPublisher satisfies this port over an
// eventing.PublishFunc the consumer writes against their own client.
type OutboxPublisher interface {
	Publish(ctx context.Context, ev OutboxEvent) error
}
