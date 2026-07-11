// Package main demonstrates an event sub-process (ADR-0122): an
// activity.SubProcess with NO incoming sequence flow, whose nested definition
// has an event-triggered inner start (event.NewStart + WithMessageCorrelator).
// Such a node is latent — never entered by a flowing token — and instead arms
// the moment its enclosing scope opens, firing only when the matching
// message/signal/timer occurs. It replaces the pre-ADR-0122 EventSubProcess
// kind: "event sub-process" is not a distinct node kind, it is an authoring
// pattern over the ordinary SubProcess + event-triggered start.
//
// This example uses the NON-INTERRUPTING flavor (event.WithNonInterrupting()):
// when the "cancel" message arrives, the event sub-process runs ALONGSIDE the
// main order path instead of cancelling it — the more illustrative case, since
// it shows both paths completing independently. (The interrupting flavor,
// which cancels the enclosing scope's tokens on fire, is exercised in
// engine/step_subprocess_eventstart_test.go — TestEventStartSubprocess_RootInterrupting_Message.)
//
// Flow:
//
//	main:  start → validate-order[Service] → await-delivery[ReceiveTask "DeliveryConfirmed", key=orderId] → close-order[Service] → end
//
//	[event-sub "handleCancel", non-interrupting, NO incoming flow]
//	  onCancel[message "cancel", key=orderId] → notify-cancel[Service] → inner-end
//
// Driving it: start the instance (the root-level event-sub arms immediately,
// alongside the main path parking on await-delivery), deliver a "cancel"
// message correlated to the order id (the event-sub fires and drains without
// touching the main path), then deliver "DeliveryConfirmed" to let the main
// path finish. Both paths are shown completing independently.
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
)

func main() {
	ctx := context.Background()

	// The event-sub's nested definition: a message-triggered, non-interrupting
	// start followed by a single notification step. WithNonInterrupting is
	// what makes this run ALONGSIDE its enclosing scope rather than cancelling
	// it — the flag only has effect on an event-triggered inner start.
	handleCancel, err := definition.NewBuilder("handle-cancel", 1).
		Add(event.NewStart("onCancel",
			event.WithMessageCorrelator("cancel", "orderId"),
			event.WithNonInterrupting())).
		Add(activity.NewServiceTask("notify-cancel", activity.WithTaskAction("notify-cancel"))).
		Add(event.NewEnd("inner-end")).
		Connect("onCancel", "notify-cancel").
		Connect("notify-cancel", "inner-end").
		Build()
	if err != nil {
		log.Fatal("build event-sub def:", err)
	}

	// The main order path. "handleCancel" is added to the definition's node
	// set but deliberately never Connect-ed — an event-triggered SubProcess
	// with no incoming flow is a reachability root in its own right (ADR-0122),
	// latent until its inner start's trigger fires.
	def, err := definition.NewBuilder("order-fulfillment", 1).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("validate-order", activity.WithTaskAction("validate-order"))).
		Add(activity.NewReceiveTask("await-delivery", "DeliveryConfirmed", activity.WithCorrelationKey("orderId"))).
		Add(activity.NewServiceTask("close-order", activity.WithTaskAction("close-order"))).
		Add(event.NewEnd("end")).
		AddSubProcess("handleCancel", handleCancel).
		Connect("start", "validate-order").
		Connect("validate-order", "await-delivery").
		Connect("await-delivery", "close-order").
		Connect("close-order", "end").
		Build()
	if err != nil {
		log.Fatal("build main def:", err)
	}

	cat := action.NewCatalog(map[string]action.Action{
		"validate-order": action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
			fmt.Printf("  [validate-order] order %v validated\n", in["orderId"])
			return nil, nil
		}),
		"notify-cancel": action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
			fmt.Printf("  [notify-cancel] EVENT SUB-PROCESS fired: logging + notifying cancellation for order %v\n", in["orderId"])
			return map[string]any{"cancelNotified": true}, nil
		}),
		"close-order": action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
			fmt.Printf("  [close-order] closing order %v after delivery confirmation\n", in["orderId"])
			return map[string]any{"closed": true}, nil
		}),
	})

	store, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}
	// DeliverMessage resolves a correlated instance's definition from its own
	// snapshot via the registry (ADR-0121), so the definition must be
	// registered even though it is never event-started here.
	reg := kernel.NewMemDefinitionRegistry()
	if err := reg.Register(def); err != nil {
		log.Fatal("register:", err)
	}
	driver, err := runtime.NewProcessDriver(runtime.WithActionCatalog(cat), runtime.WithInstanceStore(store), runtime.WithDefinitions(reg))
	if err != nil {
		log.Fatal("runner:", err)
	}

	fmt.Println("--- Order Fulfillment: Event Sub-process (ADR-0122) ---")

	// Start the instance: validate-order runs, the main token parks on
	// await-delivery, and the root-level event-sub arms immediately alongside it.
	state, err := driver.Drive(ctx, def, "order-1", map[string]any{"orderId": "order-1"})
	if err != nil {
		log.Fatal("run:", err)
	}
	fmt.Printf("main path parked at %q (status=%s); event-sub arms recorded=%d\n",
		state.Tokens[0].NodeID, view.StatusString(state.Status), len(state.EventTriggeredSubprocesses))

	// A "cancel" message arrives mid-run, correlated to this order. Because
	// onCancel is non-interrupting, this spawns the event-sub ALONGSIDE the
	// main path — await-delivery is left untouched.
	//
	// driver.DeliverMessage correlates a delivered message to an event-sub's own
	// message arm (ADR-0123), alongside message-catch tokens, message boundaries,
	// and event-based-gateway arms — so no ApplyTrigger workaround is needed.
	fmt.Println(`delivering message "cancel" (orderId=order-1)...`)
	if err := driver.DeliverMessage(ctx, "cancel", "order-1", map[string]any{"orderId": "order-1"}); err != nil {
		log.Fatal("deliver cancel:", err)
	}

	state, _, err = store.Load(ctx, "order-1")
	if err != nil {
		log.Fatal("reload after cancel:", err)
	}
	fmt.Printf("after cancel: status=%s, main path still at %q, event-sub arms remaining=%d\n",
		view.StatusString(state.Status), state.Tokens[0].NodeID, len(state.EventTriggeredSubprocesses))

	// The delivery confirmation now arrives, resuming and completing the main path.
	fmt.Println(`delivering message "DeliveryConfirmed" (orderId=order-1)...`)
	if err := driver.DeliverMessage(ctx, "DeliveryConfirmed", "order-1", map[string]any{"orderId": "order-1"}); err != nil {
		log.Fatal("deliver delivery confirmation:", err)
	}

	state, _, err = store.Load(ctx, "order-1")
	if err != nil {
		log.Fatal("reload after delivery:", err)
	}
	fmt.Printf("final status=%s\n", view.StatusString(state.Status))

	if state.Status == engine.StatusCompleted {
		fmt.Println("OK: the non-interrupting event-sub fired and drained alongside the main path; both completed")
	} else {
		fmt.Printf("unexpected outcome: status=%s\n", view.StatusString(state.Status))
	}
}
