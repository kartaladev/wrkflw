// Package main demonstrates a NODE-DRIVEN signal broadcast: a signal is thrown
// from inside a running process (an IntermediateThrowEvent) and fans out to every
// OTHER instance parked on a matching catch event — with NO consumer code calling
// driver.BroadcastSignal at the publishing site.
//
// Contrast with the signal_broadcast scenario, where an OUTSIDE actor injects the
// signal by calling driver.BroadcastSignal directly. Here the trigger is a node in
// the process graph: when a "coordinator" instance's token passes through the throw
// node, the engine emits an engine.ThrowSignal command, and the runtime publishes it
// to the SignalBus, which resumes every parked catcher. The broadcast is engine-
// internal; the consumer only starts processes and wires the bus once.
//
// The seam:
//
//	engine/step_nodes.go   intermediateThrowEventStrategy → emits ThrowSignal
//	runtime/processdriver_action.go  perform(ThrowSignal)  → SignalBus.Publish → waiters
//
// Two definitions, one shared signal name "market-open":
//
//	subscriber:  start → await["market-open" catch] → trade[Service] → end
//	coordinator: start → announce[Throw "market-open"] → end
//
// Three subscriber instances park on the catch event. Driving ONE coordinator
// instance through its throw node resumes all three — no BroadcastSignal call.
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/signal"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
)

func main() {
	ctx := context.Background()

	// Subscribers park on the "market-open" catch event and trade once resumed.
	subDef, err := definition.NewBuilder("trading-desk", 1).
		Add(event.NewStart("start")).
		Add(event.NewIntermediateCatch("await", event.WithSignalName("market-open"))).
		Add(activity.NewServiceTask("trade", activity.WithTaskAction("place-trade"))).
		Add(event.NewEnd("end")).
		Connect("start", "await").
		Connect("await", "trade").
		Connect("trade", "end").
		Build()
	if err != nil {
		log.Fatal("build subscriber def:", err)
	}

	// The coordinator throws the signal from WITHIN the process graph: its token
	// passes through the throw node, which is what publishes "market-open". No
	// consumer code calls BroadcastSignal.
	coordDef, err := definition.NewBuilder("market-coordinator", 1).
		Add(event.NewStart("start")).
		Add(event.NewIntermediateThrow("announce", event.WithThrowSignalName("market-open"))).
		Add(event.NewEnd("end")).
		Connect("start", "announce").
		Connect("announce", "end").
		Build()
	if err != nil {
		log.Fatal("build coordinator def:", err)
	}

	traded := 0
	cat := action.NewCatalog(map[string]action.Action{
		"place-trade": action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
			traded++
			fmt.Printf("  [place-trade] desk %v trading now that the market is open\n", in["desk"])
			return nil, nil
		}),
	})

	store, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}

	// Forward-reference wiring: the bus delivers resume triggers via driver.ApplyTrigger,
	// and the driver is constructed with the bus. Declare driver first, close over it,
	// assign later. Every waiter resumed by "market-open" is a subscriber instance, so
	// the deliver closure captures subDef.
	var driver *runtime.ProcessDriver
	bus, err := signal.NewSignalBus(func(ctx context.Context, instanceID string, trg engine.Trigger) error {
		_, derr := driver.ApplyTrigger(ctx, subDef, instanceID, trg)
		return derr
	})
	if err != nil {
		log.Fatal("signal bus:", err)
	}
	driver, err = runtime.NewProcessDriver(
		runtime.WithActionCatalog(cat),
		runtime.WithInstanceStore(store),
		runtime.WithSignalBus(bus), // required for the runtime to perform ThrowSignal
	)
	if err != nil {
		log.Fatal("driver:", err)
	}

	fmt.Println("--- Trading Desk: Node-Driven Signal Throw ---")

	// Three desks start and park on the "market-open" catch event. The driver
	// auto-subscribes each parked catcher to the bus — no manual Subscribe.
	desks := []string{"desk-A", "desk-B", "desk-C"}
	for _, d := range desks {
		st, err := driver.Drive(ctx, subDef, d, map[string]any{"desk": d})
		if err != nil {
			log.Fatal("start subscriber:", err)
		}
		fmt.Printf("%s parked at %q awaiting signal %q\n",
			d, st.Tokens[0].NodeID, st.Tokens[0].AwaitSignal)
	}

	// Driving the coordinator through its throw node is the ENTIRE trigger. The token
	// reaches "announce", the engine emits ThrowSignal, and the runtime publishes to
	// the bus — resuming all three desks. We never call driver.BroadcastSignal.
	fmt.Println(`coordinator running; its throw node publishes "market-open"...`)
	coordSt, err := driver.Drive(ctx, coordDef, "coordinator-1", nil)
	if err != nil {
		log.Fatal("run coordinator:", err)
	}
	fmt.Printf("coordinator status=%s (throw is fire-and-forget)\n", view.StatusString(coordSt.Status))

	// All three subscriber instances have advanced to completion.
	allDone := true
	for _, d := range desks {
		st, _, err := store.Load(ctx, d)
		if err != nil {
			log.Fatal("load:", err)
		}
		fmt.Printf("%s status=%s\n", d, view.StatusString(st.Status))
		if st.Status != engine.StatusCompleted {
			allDone = false
		}
	}

	if allDone && traded == len(desks) && coordSt.Status == engine.StatusCompleted {
		fmt.Printf("OK: one throw node resumed all %d desks — no BroadcastSignal call\n", len(desks))
	} else {
		fmt.Printf("unexpected outcome: traded=%d/%d\n", traded, len(desks))
	}
}
