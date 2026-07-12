// Package main demonstrates event-based start events (ADR-0121): a definition's
// start event can itself listen for a signal or a correlated message, so an
// EXTERNAL trigger — not a caller's Drive call — creates the instance.
//
// Positioning: event-start is the preferred, BPMN-native path for
// process-to-process CHOREOGRAPHY — declarative (the definition declares its
// own start trigger), publisher-decoupled (the publisher never names a target
// definition), and fan-out (one signal can start many definitions). Reach for
// [runtime/chain.Chainer] instead when what you need is predecessor→successor
// LINEAGE, OUTCOME-based routing driven from consumer code, or EXACTLY-ONCE
// DURABLE chaining over the outbox — none of which event-start provides.
//
// Flow (two definitions, one signal, one message):
//
//	payment:  signal-start["order.received"] → process-payment[Service] → end
//	shipment: signal-start["order.received"] → await-payment[catch "payment.completed", key=orderId] → end
//
// A single driver.BroadcastSignal("order.received", …) fans out to CREATE one
// instance of EACH definition — no predecessor/successor relationship, no
// SignalBus required for this half (BroadcastSignal's start-fan-out only needs
// the definitions registered; see [runtime.ProcessDriver.BroadcastSignal]).
// shipment's instance immediately parks on the "payment.completed" message,
// correlated by its own orderId. A subsequent driver.DeliverMessage
// ("payment.completed", "A-1", …) — standing in for an external payment
// service's callback — resumes and completes only that shipment instance
// (message = point-to-point, keyed; see message_correlation for a deeper look
// at correlation, and signal_broadcast for pure fan-out to already-running
// instances).
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/definition"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/runtime/view"
)

func main() {
	ctx := context.Background()

	// payment: signal-started by "order.received", runs a service task, done.
	paymentDef, err := definition.NewBuilder("payment", 1).
		Add(event.NewStart("start", event.WithSignalName("order.received"))).
		Add(activity.NewServiceTask("process-payment", activity.WithTaskAction("process-payment"))).
		Add(event.NewEnd("end")).
		Connect("start", "process-payment").
		Connect("process-payment", "end").
		Build()
	if err != nil {
		log.Fatal("build payment def:", err)
	}

	// shipment: signal-started by the SAME "order.received" signal, then parks
	// on the "payment.completed" message, correlated by the order id.
	shipmentDef, err := definition.NewBuilder("shipment", 1).
		Add(event.NewStart("start", event.WithSignalName("order.received"))).
		Add(event.NewIntermediateCatch("await-payment", event.WithMessageCorrelator("payment.completed", "orderId"))).
		Add(event.NewEnd("end")).
		Connect("start", "await-payment").
		Connect("await-payment", "end").
		Build()
	if err != nil {
		log.Fatal("build shipment def:", err)
	}

	cat := action.NewCatalog(map[string]action.Action{
		"process-payment": action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
			fmt.Printf("  [process-payment] charging order %v\n", in["orderId"])
			return map[string]any{"paid": true}, nil
		}),
	})

	store, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}

	// Both definitions must be registered so the driver can enumerate
	// signal-start matches for BroadcastSignal's fan-out (ADR-0121); no
	// SignalBus is needed for that path since there are no already-running
	// instances to resume.
	reg := kernel.NewMemDefinitionRegistry()
	if err := reg.Register(paymentDef); err != nil {
		log.Fatal("register payment:", err)
	}
	if err := reg.Register(shipmentDef); err != nil {
		log.Fatal("register shipment:", err)
	}

	driver, err := runtime.NewProcessDriver(runtime.WithActionCatalog(cat), runtime.WithInstanceStore(store), runtime.WithDefinitions(reg))
	if err != nil {
		log.Fatal("runner:", err)
	}

	fmt.Println("--- Order Fulfillment: Event-Based Start (ADR-0121) ---")

	// One external signal fans out to start BOTH definitions — the publisher
	// names only the signal, never payment or shipment directly.
	fmt.Println(`broadcasting signal "order.received" (orderId=A-1)...`)
	if err := driver.BroadcastSignal(ctx, "order.received", map[string]any{"orderId": "A-1"}); err != nil {
		log.Fatal("broadcast:", err)
	}

	// Discover the two instances the broadcast just created (their ids are
	// minted by the driver's id generator, not chosen by the caller) and
	// report on each by definition.
	page, err := store.List(ctx, kernel.InstanceFilter{Limit: 200})
	if err != nil {
		log.Fatal("list:", err)
	}
	var paymentID, shipmentID string
	for _, item := range page.Items {
		switch item.DefID {
		case "payment":
			paymentID = item.InstanceID
		case "shipment":
			shipmentID = item.InstanceID
		}
	}
	if paymentID == "" || shipmentID == "" {
		log.Fatalf("expected one payment and one shipment instance, got payment=%q shipment=%q", paymentID, shipmentID)
	}
	fmt.Printf("fan-out created 2 instances from one signal: payment=%s shipment=%s\n", paymentID, shipmentID)

	paymentState, _, err := store.Load(ctx, paymentID)
	if err != nil {
		log.Fatal("load payment:", err)
	}
	shipmentState, _, err := store.Load(ctx, shipmentID)
	if err != nil {
		log.Fatal("load shipment:", err)
	}
	fmt.Printf("  payment  status=%s\n", view.StatusString(paymentState.Status))
	fmt.Printf("  shipment status=%s (parked at %q awaiting message %q key=%q)\n",
		view.StatusString(shipmentState.Status),
		shipmentState.Tokens[0].NodeID, shipmentState.Tokens[0].AwaitMessage, shipmentState.Tokens[0].AwaitMessageKey)

	// The external payment service's callback: a keyed, point-to-point message
	// that correlates to (and completes) only the shipment instance awaiting
	// this exact order id — not a fresh instance, not the payment instance.
	fmt.Println(`delivering message "payment.completed" (key=A-1)...`)
	if err := driver.DeliverMessage(ctx, "payment.completed", "A-1", map[string]any{"orderId": "A-1"}); err != nil {
		log.Fatal("deliver message:", err)
	}

	shipmentState, _, err = store.Load(ctx, shipmentID)
	if err != nil {
		log.Fatal("reload shipment:", err)
	}
	fmt.Printf("  shipment status=%s\n", view.StatusString(shipmentState.Status))

	if paymentState.Status == engine.StatusCompleted && shipmentState.Status == engine.StatusCompleted {
		fmt.Println("OK: one signal started both payment and shipment; a keyed message then completed shipment")
	} else {
		fmt.Printf("unexpected outcome: payment=%s shipment=%s\n",
			view.StatusString(paymentState.Status), view.StatusString(shipmentState.Status))
	}
}
