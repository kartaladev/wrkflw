// Package main demonstrates the README quickstart: define a process, run it.
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func main() {
	ctx := context.Background()

	// --- Define a process (Go builder) ---
	def, err := model.NewDefinition("order-fulfillment", 1).
		Add(model.NewStartEvent("start")).
		Add(model.NewServiceTask("charge", "charge-card",
			model.WithCompensation("refund-card"),
		)).
		Add(model.NewUserTask("approve", []string{"manager"})).
		Add(model.NewEndEvent("end")).
		Connect("start", "charge").
		Connect("charge", "approve").
		Connect("approve", "end").
		Build()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("defined %q v%d with %d nodes\n", def.ID, def.Version, len(def.Nodes))

	// --- Author in YAML ---
	const yamlSrc = `
id: order
version: 1
nodes:
  - id: s
    kind: startEvent
  - id: charge
    kind: serviceTask
    action: charge-card
    compensationAction: refund-card
  - id: e
    kind: endEvent
flows:
  - { id: f1, source: s, target: charge }
  - { id: f2, source: charge, target: e }
`
	yamlDef, err := model.ParseYAML([]byte(yamlSrc))
	if err != nil {
		fmt.Fprintln(os.Stderr, "yaml parse:", err)
		os.Exit(1)
	}
	fmt.Printf("yaml def %q v%d with %d nodes\n", yamlDef.ID, yamlDef.Version, len(yamlDef.Nodes))

	// --- Run it ---
	simpleDef, _ := model.NewDefinition("order", 1).
		Add(model.NewStartEvent("s")).
		Add(model.NewServiceTask("charge", "charge-card")).
		Add(model.NewEndEvent("e")).
		Connect("s", "charge").
		Connect("charge", "e").
		Build()

	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"charge-card": action.Func(func(_ context.Context, vars map[string]any) (map[string]any, error) {
			return map[string]any{"charged": true}, nil
		}),
	})

	r := runtime.NewRunner(cat, clock.System(), runtime.NewMemStore())

	state, err := r.Run(ctx, simpleDef, "order-001", map[string]any{"amount": 99.0})
	if err != nil {
		log.Fatal(err)
	}
	if state.Status == engine.StatusCompleted {
		fmt.Println("order completed:", state.Variables["charged"])
	}
}
