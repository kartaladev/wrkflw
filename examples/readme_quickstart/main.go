// Package main mirrors the README quickstart end to end. It walks the two
// authoring forms the engine supports and then executes an instance, so a reader
// arriving from the README sees the exact code those snippets came from, running.
//
// It is deliberately three self-contained blocks, each teaching one thing:
//
//  1. Define a process with the Go BUILDER — the fluent, compile-checked authoring
//     form. Shows richer nodes (a service task with a compensation action, a user
//     task) to hint at what a real definition looks like.
//  2. Author the SAME shape in YAML and load it — the declarative authoring form
//     (definition.NewLoader over an io.Reader). Note there is NO BPMN2 XML loader;
//     YAML and Go code are the only authoring forms (see the project README).
//     Blocks 1 and 2 produce equivalent definitions; they differ only in how the
//     definition is *authored*, not in what it means.
//  3. RUN an instance — build a minimal definition, register the action it names,
//     and Drive it to completion against an in-memory store.
//
// Blocks 1 and 2 only build definitions (nothing executes); block 3 is the one that
// actually runs a process. They are separated so a reader can copy just the block
// they need.
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

func main() {
	ctx := context.Background()

	// ── Block 1: define a process with the Go builder ─────────────────────────
	// The fluent builder is the compile-checked authoring form. Nothing runs here —
	// Build() just validates and returns the definition template. WithCompensateAction
	// attaches a rollback action to the charge step; the user task parks for a
	// "manager" actor. Neither is exercised in this quickstart; they show the shape.
	def, err := definition.NewBuilder("order-fulfillment", 1).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("charge", activity.WithTaskAction("charge-card"),
			activity.WithCompensateAction("refund-card"),
		)).
		Add(activity.NewUserTask("approve", activity.WithCandidateRoles("manager"))).
		Add(event.NewEnd("end")).
		Connect("start", "charge").
		Connect("charge", "approve").
		Connect("approve", "end").
		Build()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("defined %q v%d with %d nodes\n", def.ID, def.Version, len(def.Nodes))

	// ── Block 2: author the same process in YAML ──────────────────────────────
	// The declarative authoring form. definition.NewLoader parses any io.Reader of
	// this YAML schema into an equivalent definition. This is the second (and last)
	// authoring form — there is no BPMN2 XML loader. Like block 1, it only builds a
	// template; nothing executes.
	const yamlSrc = `
id: order
version: 1
nodes:
  - id: s
    kind: startEvent
  - id: charge
    kind: serviceTask
    action: charge-card
    compensateAction: refund-card
  - id: e
    kind: endEvent
flows:
  - { id: f1, source: s, target: charge }
  - { id: f2, source: charge, target: e }
`
	yamlLd, err := definition.NewLoader(strings.NewReader(yamlSrc))
	if err != nil {
		fmt.Fprintln(os.Stderr, "yaml parse:", err)
		os.Exit(1)
	}
	yamlDef, err := yamlLd.Build()
	if err != nil {
		fmt.Fprintln(os.Stderr, "yaml build:", err)
		os.Exit(1)
	}
	fmt.Printf("yaml def %q v%d with %d nodes\n", yamlDef.ID, yamlDef.Version, len(yamlDef.Nodes))

	// ── Block 3: run an instance ──────────────────────────────────────────────
	// This is the block that actually executes a process. Build a minimal linear
	// definition, register the "charge-card" action it references by name in a
	// catalog, wire an in-memory store into a ProcessDriver, and Drive one instance
	// to completion. Drive returns the terminal InstanceState with its variables.
	simpleDef, _ := definition.NewBuilder("order", 1).
		Add(event.NewStart("s")).
		Add(activity.NewServiceTask("charge", activity.WithTaskAction("charge-card"))).
		Add(event.NewEnd("e")).
		Connect("s", "charge").
		Connect("charge", "e").
		Build()

	cat := action.NewCatalog(map[string]action.Action{
		"charge-card": action.ActionFunc(func(_ context.Context, vars map[string]any) (map[string]any, error) {
			return map[string]any{"charged": true}, nil
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

	state, err := driver.Drive(ctx, simpleDef, "order-001", map[string]any{"amount": 99.0})
	if err != nil {
		log.Fatal(err)
	}
	if state.Status == engine.StatusCompleted {
		fmt.Println("order completed:", state.Variables["charged"])
	}
}
