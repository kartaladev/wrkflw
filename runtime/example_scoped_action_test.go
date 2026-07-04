package runtime_test

import (
	"context"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
)

// ExampleDefinitionBuilder_RegisterAction shows the three ways to bind an action
// to a task: a definition-scoped catalog entry referenced by name, a node-local
// inline function, and default-by-id (no name → the node id is the lookup key).
func ExampleDefinitionBuilder_RegisterAction() {
	score := action.Func(func(_ context.Context, in map[string]any) (map[string]any, error) {
		return map[string]any{"score": 42}, nil
	})
	def, err := definition.NewDefinition("loan", 1).
		RegisterAction("score", score). // def-scoped, by name
		Add(definition.NewStartEvent("start")).
		Add(definition.NewServiceTask("risk", definition.WithActionName("score"))). // scoped→global
		Add(definition.NewServiceTask("notify", definition.WithActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
			return in, nil // node-local inline
		}))).
		Add(definition.NewServiceTask("archive")). // default-by-id → looks up "archive"
		Add(definition.NewEndEvent("end")).
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
