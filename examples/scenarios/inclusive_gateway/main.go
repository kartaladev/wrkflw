// Package main demonstrates an inclusive (OR) gateway split and join.
//
// An InclusiveGateway split activates EVERY outgoing flow whose condition is
// true — zero, one, or many branches. The matching join waits for exactly the
// branches that were activated (no more, no less) before continuing.
//
// Flow:
//
//	start → assess[Service] → split[Inclusive]
//	          score < 600        → notify-risk[Service]    ┐
//	          amount > 10000     → senior-review[Service]  ┤→ join[Inclusive] → end
//	          flagged == true    → fraud-check[Service]    ┘
//
// In this run the applicant has score=580 (< 600) and amount=25000 (> 10000) but
// flagged=false, so notify-risk and senior-review run while fraud-check is
// skipped. The join waits for the two active branches only.
//
// Contrast with an ExclusiveGateway (exactly one branch) and a ParallelGateway
// (all branches unconditionally).
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

	def, err := model.NewDefinition("application-screening", 1).
		Add(model.NewStartEvent("start")).
		Add(model.NewServiceTask("assess", model.WithActionName("assess"))).
		Add(model.NewInclusiveGateway("split")).
		Add(model.NewServiceTask("notify-risk", model.WithActionName("notify-risk"))).
		Add(model.NewServiceTask("senior-review", model.WithActionName("senior-review"))).
		Add(model.NewServiceTask("fraud-check", model.WithActionName("fraud-check"))).
		Add(model.NewInclusiveGateway("join")).
		Add(model.NewEndEvent("end")).
		Connect("start", "assess").
		Connect("assess", "split").
		Connect("split", "notify-risk", model.WithCondition("score < 600")).
		Connect("split", "senior-review", model.WithCondition("amount > 10000")).
		Connect("split", "fraud-check", model.WithCondition("flagged == true")).
		Connect("notify-risk", "join").
		Connect("senior-review", "join").
		Connect("fraud-check", "join").
		Connect("join", "end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	mk := func(name, key string) action.ServiceAction {
		return action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			fmt.Printf("  [%s] ran\n", name)
			return map[string]any{key: true}, nil
		})
	}
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"assess":        mk("assess", "assessed"),
		"notify-risk":   mk("notify-risk", "risk_notified"),
		"senior-review": mk("senior-review", "senior_reviewed"),
		"fraud-check":   mk("fraud-check", "fraud_checked"),
	})

	memSt, err := kernel.NewMemStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}
	r, err := runtime.NewProcessDriver(cat, memSt)
	if err != nil {
		log.Fatal("runner:", err)
	}

	fmt.Println("--- Application Screening: Inclusive Gateway ---")
	state, err := r.Run(ctx, def, "app-001", map[string]any{
		"score":   580,
		"amount":  25000,
		"flagged": false,
	})
	if err != nil {
		log.Fatal("run:", err)
	}

	if state.Status == engine.StatusCompleted {
		fmt.Println("screening completed!")
		fmt.Println("  risk_notified:  ", state.Variables["risk_notified"])
		fmt.Println("  senior_reviewed:", state.Variables["senior_reviewed"])
		_, ranFraud := state.Variables["fraud_checked"]
		fmt.Println("  fraud_checked:  ", ranFraud, "(skipped — flagged was false)")
	} else {
		fmt.Printf("unexpected status: %s\n", view.StatusString(state.Status))
	}
}
