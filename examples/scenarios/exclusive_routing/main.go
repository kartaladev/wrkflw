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
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func main() {
	ctx := context.Background()

	// Build the process definition once; run it multiple times with different vars.
	def, err := model.NewDefinition("loan-approval", 1).
		Add(model.NewStartEvent("start")).
		Add(model.NewServiceTask("check-credit", model.WithActionName("check-credit"))).
		Add(model.NewExclusiveGateway("route")).
		Add(model.NewServiceTask("manual-review", model.WithActionName("manual-review"))).
		Add(model.NewServiceTask("auto-approve", model.WithActionName("auto-approve"))).
		Add(model.NewServiceTask("reject", model.WithActionName("reject"))).
		Add(model.NewEndEvent("end")).
		Connect("start", "check-credit").
		Connect("check-credit", "route").
		Connect("route", "manual-review", model.WithCondition("amount > 50000")).
		Connect("route", "auto-approve", model.WithCondition("amount <= 50000")).
		Connect("route", "reject", model.AsDefault()).
		Connect("manual-review", "end").
		Connect("auto-approve", "end").
		Connect("reject", "end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"check-credit": action.Func(func(_ context.Context, vars map[string]any) (map[string]any, error) {
			fmt.Printf("  [check-credit] amount=%.0f\n", vars["amount"])
			return map[string]any{"credit_ok": true}, nil
		}),
		"manual-review": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			fmt.Println("  [manual-review] escalated to underwriter")
			return map[string]any{"outcome": "manual-review"}, nil
		}),
		"auto-approve": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			fmt.Println("  [auto-approve] loan auto-approved")
			return map[string]any{"outcome": "approved"}, nil
		}),
		"reject": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			fmt.Println("  [reject] loan rejected (default path)")
			return map[string]any{"outcome": "rejected"}, nil
		}),
	})

	r := runtime.NewRunner(cat, runtime.NewMemStore())

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
		state, err := r.Run(ctx, def, tc.id, map[string]any{"amount": tc.amount})
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
