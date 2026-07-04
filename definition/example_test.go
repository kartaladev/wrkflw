package definition_test

import (
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
)

// ExampleDefinitionBuilder demonstrates how a consumer builds a small process
// definition using the fluent DefinitionBuilder together with the node
// constructors. This example is also executed by "go test" to verify the output.
func ExampleDefinitionBuilder() {
	def, err := definition.NewBuilder("order-fulfillment", 1).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("charge",
			activity.WithActionName("charge-card"),
			activity.WithCompensation("refund-card"),
		)).
		Add(activity.NewUserTask("approve", []string{"manager"})).
		Add(event.NewEnd("end")).
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
