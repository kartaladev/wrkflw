package chain_test

import (
	"context"
	"fmt"

	clockwork "github.com/jonboulle/clockwork"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/chain"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
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
	store, err := kernel.NewMemStore()
	if err != nil {
		panic(err)
	}
	links := kernel.NewMemChainLinkStore()
	runner, err := runtime.NewProcessDriver(action.NewMapCatalog(nil), store, runtime.WithClock(clockwork.NewFakeClock()))
	if err != nil {
		panic(err)
	}

	fulfillment := &model.ProcessDefinition{
		ID: "fulfillment", Version: 1,
		Nodes: []model.Node{model.NewStartEvent("s"), model.NewEndEvent("e")},
		Flows: []model.SequenceFlow{{ID: "f", Source: "s", Target: "e"}},
	}

	// The policy decides the successor for each terminal outcome. A completed
	// approval starts fulfillment seeded with the approval's result; any other
	// outcome ends the chain.
	policy := func(_ context.Context, ev chain.ChainEvent) (chain.SuccessorDecision, bool) {
		if ev.Outcome != kernel.OutcomeCompleted {
			return chain.SuccessorDecision{}, false
		}
		return chain.SuccessorDecision{Def: fulfillment, Vars: ev.Result}, true
	}

	chainer, err := chain.NewChainer(runner, policy, chain.WithChainLinks(links))
	if err != nil {
		panic(err)
	}

	// Simulate the "approval-1" instance completing.
	_ = chainer.Handle(ctx, chain.ChainEvent{
		PredecessorID: "approval-1",
		Outcome:       kernel.OutcomeCompleted,
		Result:        map[string]any{"orderID": "o-7"},
	})

	st, _, _ := store.Load(ctx, "approval-1-next-completed")
	fmt.Printf("successor %s is %s\n", st.InstanceID, view.StatusString(st.Status))

	link, _, _ := links.LookupBySuccessor(ctx, "approval-1-next-completed")
	fmt.Printf("lineage: %s --%s--> %s\n", link.PredecessorID, link.Outcome, link.SuccessorID)

	// Output:
	// successor approval-1-next-completed is completed
	// lineage: approval-1 --completed--> approval-1-next-completed
}
