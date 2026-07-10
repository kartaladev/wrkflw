package processtest_test

import (
	"context"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/processtest"
)

// Example_approvalFlow drives a single human-task approval definition to
// completion using the processtest Harness and CompleteTasks helper.
func Example_approvalFlow() {
	ctx := context.Background()

	def, _ := definition.NewBuilder("approval-demo", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("approve", activity.WithEligibleRoles("manager"))).
		Add(event.NewEnd("end")).
		Connect("start", "approve").
		Connect("approve", "end").
		Build()

	h, err := processtest.New()
	if err != nil {
		fmt.Println("new harness:", err)
		return
	}

	if _, err := h.Start(ctx, def, "inst-1", map[string]any{}); err != nil {
		fmt.Println("start:", err)
		return
	}

	decide := func(t humantask.HumanTask) (authz.Actor, map[string]any, bool) {
		return authz.Actor{ID: "alice", Roles: []string{"manager"}}, map[string]any{"approved": true}, true
	}

	final, err := h.DriveToCompletion(ctx, def, "inst-1", h.CompleteTasks(decide))
	if err != nil {
		fmt.Println("drive:", err)
		return
	}

	fmt.Println("status:", final.Status)
	// Output:
	// status: completed
}
