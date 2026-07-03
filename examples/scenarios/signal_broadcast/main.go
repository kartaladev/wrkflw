// Package main demonstrates signal events: a signal published to a SignalBus is
// broadcast to every instance parked on a matching catch event.
//
// Unlike a message (point-to-point, correlation-keyed — see message_correlation),
// a signal is fan-out: one Publish resumes ALL instances awaiting that signal
// name. This models "market opened", "shift started", "recall issued" — events
// many running processes react to at once.
//
// Flow (one definition, several instances):
//
//	start → await["market-open" catch] → trade[Service] → end
//
// Three instances park on the "market-open" catch event. A single
// bus.Publish("market-open", …) resumes all three; each then runs its service
// task to completion.
//
// The bus needs a DeliverFunc to push the resume trigger back into an instance,
// and the runner needs the bus — a forward-reference (declare r, build bus with a
// closure over r, then assign r) wires the cycle. After each run the runner
// auto-subscribes parked catchers to the bus, so no manual Subscribe is needed.
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/signal"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
)

func main() {
	ctx := context.Background()

	def, err := model.NewDefinition("trading-desk", 1).
		Add(model.NewStartEvent("start")).
		Add(model.NewIntermediateCatchEvent("await", model.WithSignalName("market-open"))).
		Add(model.NewServiceTask("trade", model.WithActionName("place-trade"))).
		Add(model.NewEndEvent("end")).
		Connect("start", "await").
		Connect("await", "trade").
		Connect("trade", "end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	traded := 0
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"place-trade": action.Func(func(_ context.Context, in map[string]any) (map[string]any, error) {
			traded++
			fmt.Printf("  [place-trade] desk %v trading now that the market is open\n", in["desk"])
			return nil, nil
		}),
	})

	store, err := kernel.NewMemStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}

	// Forward-reference wiring: the bus delivers resume triggers via r.Deliver,
	// and r is constructed with the bus. Declare r first, close over it, assign later.
	var r *runtime.ProcessDriver
	bus, err := signal.NewSignalBus(func(ctx context.Context, instanceID string, trg engine.Trigger) error {
		_, derr := r.Deliver(ctx, def, instanceID, trg)
		return derr
	})
	if err != nil {
		log.Fatal("signal bus:", err)
	}
	r, err = runtime.NewProcessDriver(cat, store, runtime.WithSignalBus(bus))
	if err != nil {
		log.Fatal("runner:", err)
	}

	fmt.Println("--- Trading Desk: Signal Broadcast ---")

	// Three desks start and park on the "market-open" catch event.
	desks := []string{"desk-A", "desk-B", "desk-C"}
	for _, d := range desks {
		st, err := r.Run(ctx, def, d, map[string]any{"desk": d})
		if err != nil {
			log.Fatal("run:", err)
		}
		fmt.Printf("%s parked at %q awaiting signal %q\n",
			d, st.Tokens[0].NodeID, st.Tokens[0].AwaitSignal)
	}

	// One publish resumes every waiting desk.
	fmt.Println("publishing signal \"market-open\" (broadcast)...")
	if err := bus.Publish(ctx, "market-open", map[string]any{"at": "09:30"}); err != nil {
		log.Fatal("publish:", err)
	}

	// All three instances have advanced to completion.
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

	if allDone && traded == len(desks) {
		fmt.Printf("OK: one signal resumed all %d desks\n", len(desks))
	} else {
		fmt.Printf("unexpected outcome: traded=%d/%d\n", traded, len(desks))
	}
}
