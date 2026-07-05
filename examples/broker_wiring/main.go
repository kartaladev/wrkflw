// Command broker_wiring is a runnable, dependency-free reference showing how a
// consumer publishes wrkflw domain events to an external message broker.
//
// The engine writes domain events (status-accurate terminal events like
// instance.completed, and SendTask outbound messages) into the transactional
// outbox. A relay drains the outbox and hands each event to a kernel.OutboxPublisher.
// eventing.NewPublisher adapts ANY watermill message.Publisher to that port — so
// reaching Kafka, NATS JetStream, Redis Streams, or watermill-SQL is a one-line
// swap: replace demoPublisher below with your broker's watermill publisher.
//
// This program uses an in-repo demoPublisher (which just prints each message) so
// it runs with no broker and no extra dependency. It prints the EXACT message a
// real broker would receive, including the instance_id metadata a Kafka
// partitioner keys on and the UUID (= outbox dedup key) a consumer dedupes on.
//
// See docs/eventing-brokers.md for copy-paste Kafka / NATS / Redis / SQL wiring
// and the ordering/dedup guidance.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"github.com/ThreeDotsLabs/watermill/message"
	_ "modernc.org/sqlite" // pure-Go SQLite driver (ADR-0082)

	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/eventing"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// demoPublisher stands in for a real broker's watermill message.Publisher. In
// production this is kafka.NewPublisher(...) / nats.NewPublisher(...) /
// redisstream.NewPublisher(...) / sql.NewPublisher(...). It prints each message
// so you can see precisely what the broker receives.
type demoPublisher struct{}

func (demoPublisher) Publish(topic string, msgs ...*message.Message) error {
	for _, m := range msgs {
		fmt.Printf("→ publish  topic=%q  uuid=%q\n", topic, m.UUID)
		fmt.Printf("    metadata: instance_id=%q definition_ref=%q\n",
			m.Metadata.Get("instance_id"), m.Metadata.Get("definition_ref"))
		fmt.Printf("    payload:  %s\n", string(m.Payload))
	}
	return nil
}

func (demoPublisher) Close() error { return nil }

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()

	// A trivial definition that completes immediately, emitting instance.completed.
	def, err := definition.NewBuilder("order-flow", 1).
		Add(event.NewStart("start")).
		Add(event.NewEnd("end")).
		Connect("start", "end").
		Build()
	if err != nil {
		return err
	}

	// SQLite store (in-memory, single connection). The store persists state AND the
	// outbox atomically per committed step.
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1) // SQLite is single-writer (ADR-0082)

	if err := persistence.MigrateSQLite(ctx, db); err != nil {
		return err
	}
	store, err := persistence.OpenSQLite(ctx, db)
	if err != nil {
		return err
	}

	driver, err := runtime.NewProcessDriver(runtime.WithInstanceStore(store))
	if err != nil {
		return err
	}

	// Run one instance to completion — this writes an instance.completed event to
	// the outbox in the same transaction as the terminal state.
	final, err := driver.Run(ctx, def, "order-42", map[string]any{"amount": 4200})
	if err != nil {
		return err
	}
	fmt.Printf("instance %q reached %s\n\n", final.InstanceID, final.Status)

	// The broker wiring: wrap the broker's watermill publisher with NewPublisher
	// and hand it to the relay. Swap demoPublisher{} for your real broker publisher.
	relay, err := persistence.NewSQLiteRelay(db, eventing.NewPublisher(demoPublisher{}))
	if err != nil {
		return err
	}

	// Drain the outbox once, synchronously, publishing each event to the broker.
	// In production the relay runs in a goroutine via relay.Run(ctx).
	n, err := relay.DrainOnce(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("\nrelay drained and published %d event(s)\n", n)
	return nil
}
