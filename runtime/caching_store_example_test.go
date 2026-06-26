package runtime_test

import (
	"context"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// ExampleNewCachingStore shows how a library consumer wires a [runtime.CachingStore]
// as the store for a [runtime.Runner]. In this configuration:
//
//   - [runtime.AlwaysOwn] is used — correct for single-replica or sticky-routed
//     deployments where this process is the sole writer for every instance it touches.
//   - [clock.System] drives TTL expiry; a fake clock may be substituted in tests.
//
// The example drives a single instance through a park→resume cycle using a
// signal-catch definition (start → signal-catch("approved") → end). The second
// [runtime.Runner.Deliver] call is served from the write-through cache because
// [runtime.AlwaysOwn] grants ownership on every [runtime.CachingStore.Load].
func ExampleNewCachingStore() {
	ctx := context.Background()

	// Wrap an in-memory backing store with the write-through cache.
	// AlwaysOwn is appropriate for a single-process embedding.
	store := runtime.NewCachingStore(
		runtime.NewMemStore(),
		runtime.AlwaysOwn{},
	)

	def := signalCatchDef("approved")

	r := runtime.NewRunner(nil, store)

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
