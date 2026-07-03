package kernel

import "context"

// Publisher relays one outbox event to the eventing backend. Implementations
// must be idempotent downstream (delivery is at-least-once; the outbox
// dedup_key supports deduplication). The persistence relay calls Publish for
// each claimed unpublished row. No broker is imported here — the Eventing
// sub-project supplies a watermill-backed Publisher.
type Publisher interface {
	Publish(ctx context.Context, ev OutboxEvent) error
}
