package runtime_test

import (
	"context"
	"fmt"

	clockwork "github.com/jonboulle/clockwork"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// ExampleChainer shows process-instance chaining (ADR-0045): when one instance
// reaches a terminal state, a SuccessorPolicy starts a new, independent root
// instance seeded with the predecessor's result. Here a completing "approval"
// chains into a "fulfillment" instance.
//
// In production the terminal event arrives over the durable outbox and the
// eventing.NewChainHandler / Chainer.Run adapter projects it into a ChainEvent;
// this example drives Chainer.Handle directly so the output is deterministic.
func ExampleChainer() {
	ctx := context.Background()
	store, err := runtime.NewMemStore()
	if err != nil {
		panic(err)
	}
	links := runtime.NewMemChainLinkStore()
	runner := runtime.NewRunner(action.NewMapCatalog(nil), store, runtime.WithRunnerClock(clockwork.NewFakeClock()))

	fulfillment := &model.ProcessDefinition{
		ID: "fulfillment", Version: 1,
		Nodes: []model.Node{model.NewStartEvent("s"), model.NewEndEvent("e")},
		Flows: []model.SequenceFlow{{ID: "f", Source: "s", Target: "e"}},
	}

	// The policy decides the successor for each terminal outcome. A completed
	// approval starts fulfillment seeded with the approval's result; any other
	// outcome ends the chain.
	policy := func(_ context.Context, ev runtime.ChainEvent) (runtime.SuccessorDecision, bool) {
		if ev.Outcome != runtime.OutcomeCompleted {
			return runtime.SuccessorDecision{}, false
		}
		return runtime.SuccessorDecision{Def: fulfillment, Vars: ev.Result}, true
	}

	chainer := runtime.NewChainer(runner, policy, runtime.WithChainLinks(links))

	// Simulate the "approval-1" instance completing.
	_ = chainer.Handle(ctx, runtime.ChainEvent{
		PredecessorID: "approval-1",
		Outcome:       runtime.OutcomeCompleted,
		Result:        map[string]any{"orderID": "o-7"},
	})

	st, _, _ := store.Load(ctx, "approval-1-next-completed")
	fmt.Printf("successor %s is %s\n", st.InstanceID, runtime.StatusString(st.Status))

	link, _, _ := links.LookupBySuccessor(ctx, "approval-1-next-completed")
	fmt.Printf("lineage: %s --%s--> %s\n", link.PredecessorID, link.Outcome, link.SuccessorID)

	// Output:
	// successor approval-1-next-completed is completed
	// lineage: approval-1 --completed--> approval-1-next-completed
}
