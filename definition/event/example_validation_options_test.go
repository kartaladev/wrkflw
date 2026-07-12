package event_test

import (
	"fmt"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
	vexpr "github.com/kartaladev/wrkflw/definition/model/validate/expr"
)

// ExampleWithInputValidation shows how to attach an expr-lang predicate that
// validates the manually-provided start variables (Drive) before a process
// instance is created from this start event.
func ExampleWithInputValidation() {
	n := event.NewStart("start", event.WithInputValidation(vexpr.New("amount > 0")))
	se := n.(event.StartEvent)
	d, _ := model.DescriptorOf(se.InputValidation)
	fmt.Println(d.Kind)
	// Output:
	// expr
}

// ExampleWithPayloadValidation shows how to attach an expr-lang predicate that
// validates a message IntermediateCatchEvent's payload before it is applied to
// the process instance's variables.
func ExampleWithPayloadValidation() {
	n := event.NewIntermediateCatch("wait",
		event.WithMessageCorrelator("m", "k"),
		event.WithPayloadValidation(vexpr.New("ok == true")),
	)
	ice := n.(event.IntermediateCatchEvent)
	d, _ := model.DescriptorOf(ice.PayloadValidation)
	fmt.Println(d.Kind)
	// Output:
	// expr
}
