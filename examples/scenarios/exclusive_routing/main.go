// Package main demonstrates exclusive gateway routing in a loan approval process.
//
// Flow:
//
//	start → check-credit[Service] → route[ExclusiveGateway]
//	          amount > 50000   → manual-review[Service] → end
//	          amount <= 50000  → auto-approve[Service]  → end
//	          (default)        → reject[Service]        → end
//
// The example runs the definition twice with different loan amounts.
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
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

func main() {
	ctx := context.Background()

	// Build the process definition once; run it multiple times with different vars.
	def, err := definition.NewBuilder("loan-approval", 1).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("check-credit", activity.WithActionName("check-credit"))).
		Add(gateway.NewExclusive("route")).
		Add(activity.NewServiceTask("manual-review", activity.WithActionName("manual-review"))).
		Add(activity.NewServiceTask("auto-approve", activity.WithActionName("auto-approve"))).
		Add(activity.NewServiceTask("reject", activity.WithActionName("reject"))).
		Add(event.NewEnd("end")).
		Connect("start", "check-credit").
		Connect("check-credit", "route").
		Connect("route", "manual-review", flow.WithCondition("amount > 50000")).
		Connect("route", "auto-approve", flow.WithCondition("amount <= 50000")).
		Connect("route", "reject", flow.AsDefault()).
		Connect("manual-review", "end").
		Connect("auto-approve", "end").
		Connect("reject", "end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	cat := action.NewMapCatalog(map[string]action.Action{
		"check-credit": action.ActionFunc(func(_ context.Context, vars map[string]any) (map[string]any, error) {
			fmt.Printf("  [check-credit] amount=%.0f\n", vars["amount"])
			return map[string]any{"credit_ok": true}, nil
		}),
		"manual-review": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			fmt.Println("  [manual-review] escalated to underwriter")
			return map[string]any{"outcome": "manual-review"}, nil
		}),
		"auto-approve": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			fmt.Println("  [auto-approve] loan auto-approved")
			return map[string]any{"outcome": "approved"}, nil
		}),
		"reject": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			fmt.Println("  [reject] loan rejected (default path)")
			return map[string]any{"outcome": "rejected"}, nil
		}),
	})

	memSt, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}
	r, err := runtime.NewProcessDriver(runtime.WithActionCatalog(cat), runtime.WithInstanceStore(memSt))
	if err != nil {
		log.Fatal("runner:", err)
	}

	cases := []struct {
		id     string
		amount float64
	}{
		{"loan-high-001", 75000},
		{"loan-low-002", 20000},
	}

	fmt.Println("--- Loan Approval: Exclusive Routing ---")
	for _, tc := range cases {
		fmt.Printf("\nRunning instance %q, amount=%.0f\n", tc.id, tc.amount)
		state, err := r.Drive(ctx, def, tc.id, map[string]any{"amount": tc.amount})
		if err != nil {
			log.Fatalf("run %s: %v", tc.id, err)
		}
		if state.Status == engine.StatusCompleted {
			fmt.Printf("  outcome: %v\n", state.Variables["outcome"])
		} else {
			fmt.Printf("  unexpected status: %v\n", state.Status)
		}
	}
}
