// Package main demonstrates NODE-DRIVEN message routing: a SendTask node in one
// process emits a correlated message that resumes a ReceiveTask node in ANOTHER
// process — with NO consumer code calling driver.DeliverMessage at the publishing
// site. The only DeliverMessage call lives inside a message.* subscriber that is
// wired ONCE at startup (eventing.NewMessageHandler), not per publish.
//
// Contrast with the message_correlation scenario, where an OUTSIDE actor injects the
// message by calling driver.DeliverMessage directly. Here the trigger is a node in
// the sender's graph. Per ADR-0067 a SendTask does NOT call a receiver synchronously;
// it commits a message.<Name> row into the transactional outbox in the same tx as its
// state change. The delivery path is decoupled and durable:
//
//	SendTask node            → engine.SendMessage command
//	deliverLoop edge         → message.OrderPlaced OutboxEvent (committed with state)
//	relay.DrainOnce          → publishes to the broker (here an in-process GoChannel)
//	eventing.NewMessageHandler(driver.DeliverMessage)
//	                         → decodes + routes to the correlated ReceiveTask
//
// Two definitions correlated by orderId:
//
//	receiver: start → await[ReceiveTask "OrderPlaced", key = orderId] → fulfil[Service] → end
//	sender:   start → place[SendTask "OrderPlaced", key = orderId]                       → end
//
// The receiver parks; the sender runs its SendTask node; draining the outbox through
// the message handler resumes the receiver. driver.DeliverMessage is called only from
// inside the handler.
//
// The store is in-memory SQLite (ADR-0082) because a SendTask's outbound message flows
// through the transactional outbox + relay, which the in-memory instance store lacks.
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (ADR-0082)

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/eventing"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()

	// receiver parks on "OrderPlaced" keyed by its own orderId, then fulfils.
	recvDef, err := definition.NewBuilder("order-fulfilment", 1).
		Add(event.NewStart("start")).
		Add(activity.NewReceiveTask("await", "OrderPlaced", activity.WithCorrelationKey("orderId"))).
		Add(activity.NewServiceTask("fulfil", activity.WithTaskAction("fulfil-order"))).
		Add(event.NewEnd("end")).
		Connect("start", "await").
		Connect("await", "fulfil").
		Connect("fulfil", "end").
		Build()
	if err != nil {
		return fmt.Errorf("build receiver def: %w", err)
	}

	// sender emits "OrderPlaced" from a SendTask node, keyed by orderId.
	sendDef, err := definition.NewBuilder("order-intake", 1).
		Add(event.NewStart("start")).
		Add(activity.NewSendTask("place", "OrderPlaced", activity.WithCorrelationKey("orderId"))).
		Add(event.NewEnd("end")).
		Connect("start", "place").
		Connect("place", "end").
		Build()
	if err != nil {
		return fmt.Errorf("build sender def: %w", err)
	}

	fulfilled := false
	cat := action.NewCatalog(map[string]action.Action{
		"fulfil-order": action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
			fulfilled = true
			fmt.Printf("  [fulfil-order] fulfilling order %v\n", in["orderId"])
			return map[string]any{"fulfilled": true}, nil
		}),
	})

	// SQLite store persists instance state AND the outbox atomically per step.
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

	// DeliverMessage resolves the correlated instance's definition from its own
	// snapshot via the registry (ADR-0121), so the receiver def must be registered.
	reg := kernel.NewMemDefinitionRegistry()
	if err := reg.Register(recvDef); err != nil {
		return err
	}
	if err := reg.Register(sendDef); err != nil {
		return err
	}

	driver, err := runtime.NewProcessDriver(
		runtime.WithActionCatalog(cat),
		runtime.WithInstanceStore(store),
		runtime.WithDefinitions(reg),
	)
	if err != nil {
		return err
	}

	fmt.Println("--- Order Intake → Fulfilment: Node-Driven Message Routing ---")

	// The message.* subscriber is the ONLY place DeliverMessage is called, and it is
	// wired once here — not at the SendTask call site. NewGoChannelPublisher gives an
	// in-process broker; a real deployment swaps in Kafka/NATS/etc. (see broker_wiring).
	pub, sub, closer := eventing.NewGoChannelPublisher()
	defer func() { _ = closer.Close() }()

	// Subscribe BEFORE the relay publishes: GoChannel is non-persistent, so a publish
	// before Subscribe is dropped.
	msgs, err := sub.Subscribe(ctx, "message.OrderPlaced")
	if err != nil {
		return err
	}
	deliver := eventing.NewMessageHandler(driver.DeliverMessage)

	// Park the receiver on its ReceiveTask.
	recvSt, err := driver.Drive(ctx, recvDef, "recv-o-1", map[string]any{"orderId": "o-1"})
	if err != nil {
		return fmt.Errorf("start receiver: %w", err)
	}
	fmt.Printf("receiver parked at %q awaiting message %q (status=%s)\n",
		recvSt.Tokens[0].NodeID, recvSt.Tokens[0].AwaitMessage, view.StatusString(recvSt.Status))

	// Run the sender: its SendTask node commits a message.OrderPlaced row into the
	// outbox and the instance completes (fire-and-forget). No DeliverMessage here.
	sendSt, err := driver.Drive(ctx, sendDef, "send-o-1", map[string]any{"orderId": "o-1"})
	if err != nil {
		return fmt.Errorf("run sender: %w", err)
	}
	fmt.Printf("sender status=%s (SendTask committed message to outbox)\n", view.StatusString(sendSt.Status))

	// Relay drains the outbox → publishes to the GoChannel broker. In production the
	// relay runs continuously via relay.Run(ctx); DrainOnce keeps this example a single
	// synchronous pass.
	relay, err := persistence.NewSQLiteRelay(db, pub)
	if err != nil {
		return err
	}
	if _, err := relay.DrainOnce(ctx); err != nil {
		return err
	}

	// Read the message off the broker and route it through the handler, which calls
	// DeliverMessage to resume the parked receiver.
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case msg := <-msgs:
		if err := deliver(msg); err != nil {
			return fmt.Errorf("handler deliver: %w", err)
		}
		msg.Ack()
		fmt.Println("message.OrderPlaced routed through NewMessageHandler → DeliverMessage")
	case <-waitCtx.Done():
		return fmt.Errorf("timed out waiting for message.OrderPlaced on the broker")
	}

	// The receiver has advanced past the ReceiveTask, through fulfil → end.
	final, _, err := store.Load(ctx, "recv-o-1")
	if err != nil {
		return err
	}
	fmt.Printf("receiver status=%s (fulfilled=%v)\n", view.StatusString(final.Status), fulfilled)

	if final.Status == engine.StatusCompleted && fulfilled && sendSt.Status == engine.StatusCompleted {
		fmt.Println("OK: a SendTask node resumed a ReceiveTask node — no DeliverMessage at the call site")
	} else {
		fmt.Printf("unexpected outcome: receiver=%s fulfilled=%v\n", view.StatusString(final.Status), fulfilled)
	}
	return nil
}
