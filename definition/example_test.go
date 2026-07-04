package definition_test

import (
	"fmt"
	"strings"

	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
)

// ExampleNewLoader demonstrates authoring a definition from YAML via the root
// NewLoader entry point (the symmetric counterpart to NewBuilder). Importing the
// definition package registers every node kind, so the loader can reconstruct the
// nodes from their serialized form.
func ExampleNewLoader() {
	const src = `
id: order
version: 1
nodes:
  - {id: start, kind: startEvent}
  - {id: charge, kind: serviceTask, action: charge-card}
  - {id: end, kind: endEvent}
flows:
  - {id: f1, source: start, target: charge}
  - {id: f2, source: charge, target: end}
`
	ld, err := definition.NewLoader(strings.NewReader(src))
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	def, err := ld.Build()
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Printf("%s v%d: %d nodes, %d flows\n", def.ID, def.Version, len(def.Nodes), len(def.Flows))

	// Output:
	// order v1: 3 nodes, 2 flows
}

// ExampleNewBuilder demonstrates how a consumer builds a small process
// definition using the fluent builder from definition.NewBuilder together with
// the node constructors. This example is also executed by "go test" to verify
// the output.
func ExampleNewBuilder() {
	def, err := definition.NewBuilder("order-fulfillment", 1).
		AddStartEvent("start").
		AddServiceTask("charge",
			activity.WithActionName("charge-card"),
			activity.WithCompensation("refund-card"),
		).
		AddUserTask("approve", []string{"manager"}).
		AddEndEvent("end").
		Connect("start", "charge").
		Connect("charge", "approve").
		Connect("approve", "end").
		Build()
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}

	fmt.Printf("nodes: %d\n", len(def.Nodes))
	fmt.Printf("flows: %d\n", len(def.Flows))
	for _, n := range def.Nodes {
		fmt.Printf("  %s (%s)\n", n.ID(), n.Kind())
	}

	// Output:
	// nodes: 4
	// flows: 3
	//   start (startEvent)
	//   charge (serviceTask)
	//   approve (userTask)
	//   end (endEvent)
}
