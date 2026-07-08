// Package main demonstrates a BPMN parallel gateway (AND-split / AND-join): a
// fork splits the flow into concurrent branches that are ALL taken, and a join
// synchronises them — the flow past the join proceeds only once EVERY inbound
// branch has arrived.
//
// Scenario: fulfilling an order requires two independent steps — picking the
// items from the warehouse and charging the customer's card. They have no data
// dependency on each other, so the process runs them as parallel branches and
// then waits for both to finish before shipping.
//
//	                 ┌─ pick-items[Service] ─┐
//	start → fork[Parallel]                    join[Parallel] → ship[Service] → end
//	                 └─ charge-card[Service] ─┘
//
// Token semantics (the non-obvious part). Movement through the graph is modelled
// by tokens. When a token reaches the parallel FORK, the engine consumes it and
// emits ONE new token per outgoing flow — here two — so both branches become live
// simultaneously. Each branch carries its own token down its path. At the parallel
// JOIN the engine does the inverse: it WAITS until a token has arrived on every
// inbound flow (both branches), then merges them into a SINGLE outgoing token.
// This is why a parallel join is a synchronisation barrier, not a passthrough —
// the first branch to arrive parks at the join until its sibling catches up. An
// exclusive gateway, by contrast, would take exactly one branch and need no merge.
//
// The demo store is in-memory, and the two branch actions run to completion within
// a single Drive call, so the whole fork → join → merge cycle resolves
// synchronously; no scheduler or clock is involved. When the instance reaches the
// end event its variables carry the outputs merged from both branches (items_picked
// from one, payment_ref from the other, plus tracking from the post-join ship step).
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

	// Build the process. gateway.NewParallel serves as BOTH the fork and the join;
	// the engine reads the node's fan-out/fan-in from the connected flows. "fork"
	// has two outgoing flows (AND-split → two tokens); "join" has two incoming flows
	// (AND-join → wait for both, then one token). The same node type does both roles.
	def, err := definition.NewBuilder("order-fulfillment", 1).
		Add(event.NewStart("start")).
		Add(gateway.NewParallel("fork")).
		Add(activity.NewServiceTask("pick-items", activity.WithActionName("pick-items"))).
		Add(activity.NewServiceTask("charge-card", activity.WithActionName("charge-card"))).
		Add(gateway.NewParallel("join")).
		Add(activity.NewServiceTask("ship", activity.WithActionName("ship"))).
		Add(event.NewEnd("end")).
		Connect("start", "fork").
		// Two flows out of the fork → the branches the AND-split token-splits into.
		Connect("fork", "pick-items").
		Connect("fork", "charge-card").
		// Two flows into the join → the branches the AND-join waits to synchronise.
		Connect("pick-items", "join").
		Connect("charge-card", "join").
		Connect("join", "ship").
		Connect("ship", "end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	// Wire up the service actions referenced by name from the definition. The two
	// branch actions (pick-items, charge-card) both run within this Drive call — the
	// engine invokes each as its branch token advances; "ship" runs only after the
	// join has merged both branches.
	cat := action.NewCatalog(map[string]action.Action{
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

	memSt, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}
	driver, err := runtime.NewProcessDriver(runtime.WithActionCatalog(cat), runtime.WithInstanceStore(memSt))
	if err != nil {
		log.Fatal("runner:", err)
	}

	fmt.Println("--- Order Fulfillment: Parallel Fork/Join ---")
	// Drive runs the instance through fork → both branches → join → ship → end in a
	// single call. The interleaving of the two branch prints is an engine detail; the
	// join guarantees both have completed before ship runs.
	state, err := driver.Drive(ctx, def, "order-001", map[string]any{"order_id": "ORD-001"})
	if err != nil {
		log.Fatal("run:", err)
	}

	if state.Status == engine.StatusCompleted {
		// The completed instance's variables are the MERGE of both branches' outputs
		// (items_picked and payment_ref) plus the post-join ship step's tracking.
		fmt.Println("Order completed successfully!")
		fmt.Println("  items_picked:", state.Variables["items_picked"])
		fmt.Println("  payment_ref:", state.Variables["payment_ref"])
		fmt.Println("  tracking:", state.Variables["tracking"])
	} else {
		fmt.Printf("Unexpected status: %v\n", state.Status)
	}
}
