package activity_test

import (
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	vexpr "github.com/zakyalvan/krtlwrkflw/definition/model/validate/expr"
)

// ExampleWithCompletionValidation shows how to attach an expr-lang predicate
// that validates a UserTask's completion output before it is applied to the
// process instance's variables.
func ExampleWithCompletionValidation() {
	n := activity.NewUserTask("approve", []string{"mgr"},
		activity.WithCompletionValidation(vexpr.New("decision in ['approve','reject']")),
	)
	u := n.(activity.UserTask)
	d, _ := model.DescriptorOf(u.CompletionValidation)
	fmt.Println(d.Kind)
	// Output:
	// expr
}

// ExampleWithPayloadValidation shows how to attach an expr-lang predicate that
// validates a ReceiveTask's inbound message payload before it is applied to
// the process instance's variables.
func ExampleWithPayloadValidation() {
	n := activity.NewReceiveTask("recv", "m",
		activity.WithPayloadValidation(vexpr.New("ok == true")),
	)
	r := n.(activity.ReceiveTask)
	d, _ := model.DescriptorOf(r.PayloadValidation)
	fmt.Println(d.Kind)
	// Output:
	// expr
}
