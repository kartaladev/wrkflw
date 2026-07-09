package runtime_test

import (
	"context"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
)

// ExampleDefinitionBuilder_RegisterAction shows the two ways to bind an action
// to a task: a definition-scoped catalog entry referenced by name, and
// default-by-id (no name → the node id is the lookup key).
func ExampleDefinitionBuilder_RegisterAction() {
	score := action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
		return map[string]any{"score": 42}, nil
	})
	notify := action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
		return in, nil
	})
	def, err := definition.NewBuilder("loan", 1).
		RegisterAction("score", score).   // def-scoped, by name
		RegisterAction("notify", notify). // def-scoped, resolved by default-by-id below
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("risk", activity.WithTaskAction("score"))). // scoped→global
		Add(activity.NewServiceTask("notify")).                                 // default-by-id → looks up "notify"
		Add(activity.NewServiceTask("archive")).                                // default-by-id → looks up "archive"
		Add(event.NewEnd("end")).
		Connect("start", "risk").Connect("risk", "notify").
		Connect("notify", "archive").Connect("archive", "end").
		Build()
	if err != nil {
		fmt.Println("build error:", err)
		return
	}
	fmt.Println(def.ScopedCatalog() != nil)
	// Output: true
}
