package kernel_test

import (
	"context"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// ExampleNewCachingStore shows how a library consumer wires a [kernel.CachingInstanceStore]
// as the store for a [runtime.ProcessDriver]. In this configuration:
//
//   - [kernel.AlwaysOwn] is used — correct for single-replica or sticky-routed
//     deployments where this process is the sole writer for every instance it touches.
//   - [clock.System] drives TTL expiry; a fake clock may be substituted in tests.
//
// The example drives a single instance through a park→resume cycle using a
// signal-catch definition (start → signal-catch("approved") → end). The second
// [runtime.ProcessDriver.Deliver] call is served from the write-through cache because
// [kernel.AlwaysOwn] grants ownership on every [kernel.CachingInstanceStore.Load].
func ExampleNewCachingInstanceStore() {
	ctx := context.Background()

	// Wrap an in-memory backing store with the write-through cache.
	// AlwaysOwn is appropriate for a single-process embedding.
	backing, err := kernel.NewMemInstanceStore()
	if err != nil {
		panic(err)
	}
	store, err := kernel.NewCachingInstanceStore(
		backing,
		kernel.AlwaysOwn{},
	)
	if err != nil {
		panic(err)
	}

	def := runtimetest.SignalCatchDef("approved")

	r, err := runtime.NewProcessDriver(runtime.WithInstanceStore(store))
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
