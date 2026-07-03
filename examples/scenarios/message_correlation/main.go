// Package main demonstrates message events with correlation: a process parks at
// a ReceiveTask until a named, correlation-keyed message is delivered.
//
// Unlike a signal (broadcast to every waiter — see signal_broadcast), a message
// is point-to-point: it is delivered to the single instance whose parked
// ReceiveTask matches both the message name AND the resolved correlation key.
// The correlation key is an expr expression evaluated over the instance
// variables, so several instances of the same definition can wait on the same
// message name yet be addressed individually by key.
//
// Flow:
//
//	start → await-payment[ReceiveTask "PaymentReceived", key = order.id] → ship → end
//
// Two orders start and both park on "PaymentReceived". Delivering the message
// with correlation key "order-1" resumes only order 1; order 2 keeps waiting
// until its own key arrives.
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
)

func main() {
	ctx := context.Background()

	// The correlation key is an expr expression over the instance variables here
	// `orderID` resolves to each instance's own order id, so each parked
	// ReceiveTask is addressable by that value.
	def, err := model.NewDefinition("order-shipping", 1).
		Add(model.NewStartEvent("start")).
		Add(model.NewReceiveTask("await-payment", "PaymentReceived",
			model.WithCorrelationKey("orderID"))).
		Add(model.NewServiceTask("ship", model.WithActionName("ship-order"))).
		Add(model.NewEndEvent("end")).
		Connect("start", "await-payment").
		Connect("await-payment", "ship").
		Connect("ship", "end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	shipped := map[string]bool{}
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"ship-order": action.Func(func(_ context.Context, in map[string]any) (map[string]any, error) {
			id, _ := in["orderID"].(string)
			shipped[id] = true
			fmt.Printf("  [ship-order] shipping %s\n", id)
			return map[string]any{"shipped": true}, nil
		}),
	})

	store, err := kernel.NewMemStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}
	// No message-specific option is required — waiter tracking is built in.
	r, err := runtime.NewProcessDriver(cat, store)
	if err != nil {
		log.Fatal("runner:", err)
	}

	fmt.Println("--- Order Shipping: Message Correlation ---")

	// Start two orders; each parks at the ReceiveTask keyed by its own orderID.
	for _, id := range []string{"order-1", "order-2"} {
		st, err := r.Run(ctx, def, id, map[string]any{"orderID": id})
		if err != nil {
			log.Fatal("run:", err)
		}
		fmt.Printf("%s parked at %q (status=%s)\n",
			id, st.Tokens[0].NodeID, view.StatusString(st.Status))
	}

	// Deliver the payment message for order-1 only. Name + correlation key must
	// match the parked token's resolved key.
	fmt.Println("delivering PaymentReceived for order-1...")
	if err := r.DeliverMessage(ctx, def, "PaymentReceived", "order-1",
		map[string]any{"amount": 4200}); err != nil {
		log.Fatal("deliver message:", err)
	}

	// order-1 has advanced through ship → end; order-2 is still waiting.
	o1, _, err := store.Load(ctx, "order-1")
	if err != nil {
		log.Fatal("load order-1:", err)
	}
	o2, _, err := store.Load(ctx, "order-2")
	if err != nil {
		log.Fatal("load order-2:", err)
	}
	fmt.Printf("order-1 status=%s (shipped=%v)\n", view.StatusString(o1.Status), shipped["order-1"])
	fmt.Printf("order-2 status=%s (still waiting for its message)\n", view.StatusString(o2.Status))

	// Now deliver order-2's message; it too completes.
	fmt.Println("delivering PaymentReceived for order-2...")
	if err := r.DeliverMessage(ctx, def, "PaymentReceived", "order-2",
		map[string]any{"amount": 999}); err != nil {
		log.Fatal("deliver message:", err)
	}
	o2, _, err = store.Load(ctx, "order-2")
	if err != nil {
		log.Fatal("load order-2:", err)
	}

	if o1.Status == engine.StatusCompleted && o2.Status == engine.StatusCompleted &&
		shipped["order-1"] && shipped["order-2"] {
		fmt.Println("OK: each order resumed only on its own correlated message")
	} else {
		fmt.Printf("unexpected outcome: o1=%s o2=%s\n",
			view.StatusString(o1.Status), view.StatusString(o2.Status))
	}
}
