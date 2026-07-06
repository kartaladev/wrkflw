// Package main demonstrates a CallActivity that invokes a separate, reusable
// top-level process definition resolved by name through a DefinitionRegistry.
//
// Flow:
//
//	parent:  parent-start → call[CallActivity → "credit-check"] → parent-end
//
//	child:   child-start → score[Service] → child-end   (registered as "credit-check")
//
// Unlike a SubProcess (which embeds its nested definition inline), a CallActivity
// references a standalone definition by name. The runner resolves "credit-check"
// from the registry, runs the child to completion, and merges the child's output
// variables back into the parent before the parent continues.
//
// This mirrors runtime/subprocess_example_test.go
// (TestCallActivityRunsChildAndResumesParent), the authoritative reference.
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

	// Child definition — a reusable credit-check process.
	child, err := definition.NewBuilder("credit-check", 1).
		Add(event.NewStart("child-start")).
		Add(activity.NewServiceTask("score", activity.WithActionName("score"))).
		Add(event.NewEnd("child-end")).
		Connect("child-start", "score").
		Connect("score", "child-end").
		Build()
	if err != nil {
		log.Fatal("build child def:", err)
	}

	// Parent definition — calls the child by its registered name.
	parent, err := definition.NewBuilder("loan-origination", 1).
		Add(event.NewStart("parent-start")).
		Add(activity.NewCallActivity("call", definition.Latest("credit-check"))).
		Add(event.NewEnd("parent-end")).
		Connect("parent-start", "call").
		Connect("call", "parent-end").
		Build()
	if err != nil {
		log.Fatal("build parent def:", err)
	}

	cat := action.NewMapCatalog(map[string]action.Action{
		"score": action.ActionFunc(func(_ context.Context, vars map[string]any) (map[string]any, error) {
			fmt.Printf("  [score] scoring applicant %v\n", vars["applicant"])
			return map[string]any{"credit_score": 742}, nil
		}),
	})

	// Register the child so the CallActivity can resolve "credit-check".
	reg := kernel.NewMapDefinitionRegistry(child)

	memSt, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}
	r, err := runtime.NewProcessDriver(
		runtime.WithActionCatalog(cat),
		runtime.WithInstanceStore(memSt),
		runtime.WithDefinitions(reg),
	)
	if err != nil {
		log.Fatal("runner:", err)
	}

	fmt.Println("--- Loan Origination: Call Activity ---")
	state, err := r.Run(ctx, parent, "loan-001", map[string]any{"applicant": "Ada"})
	if err != nil {
		log.Fatal("run:", err)
	}

	if state.Status == engine.StatusCompleted {
		fmt.Println("loan origination completed!")
		fmt.Println("  credit_score (merged from child):", state.Variables["credit_score"])
	} else {
		fmt.Printf("unexpected status: %s\n", view.StatusString(state.Status))
	}
}
