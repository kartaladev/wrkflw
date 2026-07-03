package kernel_test

import (
	"context"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// signalCatchDef returns: start → signal-catch(name) → end.
// The instance parks at the signal-catch node until a SignalReceived trigger arrives.
func signalCatchDef(signalName string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "signal-catch-" + signalName,
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewIntermediateCatchEvent("wait-signal", model.WithSignalName(signalName)),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait-signal"},
			{ID: "f2", Source: "wait-signal", Target: "end"},
		},
	}
}

// ExampleNewCachingStore shows how a library consumer wires a [kernel.CachingStore]
// as the store for a [runtime.ProcessDriver]. In this configuration:
//
//   - [kernel.AlwaysOwn] is used — correct for single-replica or sticky-routed
//     deployments where this process is the sole writer for every instance it touches.
//   - [clock.System] drives TTL expiry; a fake clock may be substituted in tests.
//
// The example drives a single instance through a park→resume cycle using a
// signal-catch definition (start → signal-catch("approved") → end). The second
// [runtime.ProcessDriver.Deliver] call is served from the write-through cache because
// [kernel.AlwaysOwn] grants ownership on every [kernel.CachingStore.Load].
func ExampleNewCachingStore() {
	ctx := context.Background()

	// Wrap an in-memory backing store with the write-through cache.
	// AlwaysOwn is appropriate for a single-process embedding.
	backing, err := kernel.NewMemStore()
	if err != nil {
		panic(err)
	}
	store, err := kernel.NewCachingStore(
		backing,
		kernel.AlwaysOwn{},
	)
	if err != nil {
		panic(err)
	}

	def := signalCatchDef("approved")

	r, err := runtime.NewProcessDriver(action.NewMapCatalog(nil), store)
	if err != nil {
		panic(err)
	}

	// Run parks at the signal-catch node.
	parked, err := r.Run(ctx, def, "cache-demo-1", nil)
	if err != nil {
		panic(err)
	}
	_ = parked // StatusRunning

	// Deliver a matching SignalReceived trigger; the Load is served from cache.
	trg := engine.NewSignalReceived(clock.System().Now(), "approved", map[string]any{"decision": "yes"})
	final, err := r.Deliver(ctx, def, "cache-demo-1", trg)
	if err != nil {
		panic(err)
	}

	// Status is an int; compare against the sentinel and print a stable string.
	if final.Status == engine.StatusCompleted {
		fmt.Println("completed")
	}
	// Output:
	// completed
}
