package runtime_test

import (
	"context"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// ExampleDefinitionBuilder_RegisterAction shows the three ways to bind an action
// to a task: a definition-scoped catalog entry referenced by name, a node-local
// inline function, and default-by-id (no name → the node id is the lookup key).
func ExampleDefinitionBuilder_RegisterAction() {
	score := action.Func(func(_ context.Context, in map[string]any) (map[string]any, error) {
		return map[string]any{"score": 42}, nil
	})
	def, err := model.NewDefinition("loan", 1).
		RegisterAction("score", score). // def-scoped, by name
		Add(model.NewStartEvent("start")).
		Add(model.NewServiceTask("risk", model.WithActionName("score"))). // scoped→global
		Add(model.NewServiceTask("notify", model.WithActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
			return in, nil // node-local inline
		}))).
		Add(model.NewServiceTask("archive")). // default-by-id → looks up "archive"
		Add(model.NewEndEvent("end")).
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
