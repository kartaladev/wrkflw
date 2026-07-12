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

	"github.com/kartaladev/wrkflw/definition"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/eventing"
	"github.com/kartaladev/wrkflw/persistence"
	"github.com/kartaladev/wrkflw/runtime"
)

// demoPublisher stands in for a real broker's watermill message.Publisher. In
// production this is kafka.NewPublisher(...) / nats.NewPublisher(...) /
// redisstream.NewPublisher(...) / sql.NewPublisher(...). It prints each message
// so you can see precisely what the broker receives.
//
// It implements watermill's message.Publisher (Publish + Close) — the SAME
// interface every real watermill broker publisher implements — which is exactly
// why the swap is one line: eventing.NewPublisher accepts any message.Publisher.
// A consumer replaces demoPublisher{} with their broker's publisher and changes
// nothing else.
type demoPublisher struct{}

func (demoPublisher) Publish(topic string, msgs ...*message.Message) error {
	// One watermill message per outbox event. The engine populates the fields a
	// downstream broker/consumer needs: UUID is the outbox row's dedup key (a
	// consumer keys idempotency on it), instance_id is the partition key a Kafka
	// partitioner uses to keep one instance's events in order, and definition_ref
	// identifies the process the event came from. A real broker keys/routes on
	// exactly these; printing them shows the wire shape without a broker.
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
	final, err := driver.Drive(ctx, def, "order-42", map[string]any{"amount": 4200})
	if err != nil {
		return err
	}
	fmt.Printf("instance %q reached %s\n\n", final.InstanceID, final.Status)

	// The broker wiring: wrap the broker's watermill publisher with NewPublisher
	// (adapting message.Publisher → the engine's kernel.OutboxPublisher port) and
	// hand it to the relay. Swap demoPublisher{} for your real broker publisher.
	//
	// The relay is the OUTBOX drainer: it reads rows the engine committed to the
	// outbox table (in the same tx as the state change), publishes each, and marks
	// it done — the transactional-outbox pattern that makes event delivery atomic
	// with state changes and at-least-once. It is deliberately decoupled from the
	// engine: the engine only writes rows, the relay only publishes them, so the
	// broker choice never touches workflow code.
	relay, err := persistence.NewSQLiteRelay(db, eventing.NewPublisher(demoPublisher{}))
	if err != nil {
		return err
	}

	// DrainOnce publishes every currently-pending outbox row and returns — used here
	// so the example is a single synchronous pass with no goroutines. In production
	// the same relay runs continuously in a goroutine via relay.Run(ctx), which loops
	// DrainOnce with backoff and (on Postgres/MySQL) LISTEN/NOTIFY wakeups.
	n, err := relay.DrainOnce(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("\nrelay drained and published %d event(s)\n", n)
	return nil
}
