// Package main demonstrates a parallel fork/join workflow for order fulfillment.
//
// Flow:
//
//	start → fork[Parallel] → pick-items[Service]
//	                       → charge-card[Service]
//	                       ← join[Parallel] ← both
//	                       → ship[Service] → end
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
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

func main() {
	ctx := context.Background()

	// Build the process definition.
	def, err := definition.NewBuilder("order-fulfillment", 1).
		Add(event.NewStart("start")).
		Add(gateway.NewParallel("fork")).
		Add(activity.NewServiceTask("pick-items", activity.WithActionName("pick-items"))).
		Add(activity.NewServiceTask("charge-card", activity.WithActionName("charge-card"))).
		Add(gateway.NewParallel("join")).
		Add(activity.NewServiceTask("ship", activity.WithActionName("ship"))).
		Add(event.NewEnd("end")).
		Connect("start", "fork").
		Connect("fork", "pick-items").
		Connect("fork", "charge-card").
		Connect("pick-items", "join").
		Connect("charge-card", "join").
		Connect("join", "ship").
		Connect("ship", "end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	// Wire up service actions.
	cat := action.NewMapCatalog(map[string]action.Action{
		"pick-items": action.ActionFunc(func(_ context.Context, vars map[string]any) (map[string]any, error) {
			fmt.Println("  [pick-items] picking items from warehouse")
			return map[string]any{"items_picked": true}, nil
		}),
		"charge-card": action.ActionFunc(func(_ context.Context, vars map[string]any) (map[string]any, error) {
			fmt.Printf("  [charge-card] charging card for order %v\n", vars["order_id"])
			return map[string]any{"payment_ref": "PAY-001"}, nil
		}),
		"ship": action.ActionFunc(func(_ context.Context, vars map[string]any) (map[string]any, error) {
			fmt.Println("  [ship] shipping order")
			return map[string]any{"tracking": "TRACK-42"}, nil
		}),
	})

	memSt, err := kernel.NewMemStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}
	r, err := runtime.NewProcessDriver(cat, memSt)
	if err != nil {
		log.Fatal("runner:", err)
	}

	fmt.Println("--- Order Fulfillment: Parallel Fork/Join ---")
	state, err := r.Run(ctx, def, "order-001", map[string]any{"order_id": "ORD-001"})
	if err != nil {
		log.Fatal("run:", err)
	}

	if state.Status == engine.StatusCompleted {
		fmt.Println("Order completed successfully!")
		fmt.Println("  items_picked:", state.Variables["items_picked"])
		fmt.Println("  payment_ref:", state.Variables["payment_ref"])
		fmt.Println("  tracking:", state.Variables["tracking"])
	} else {
		fmt.Printf("Unexpected status: %v\n", state.Status)
	}
}
