// Package main demonstrates a compensation / saga rollback.
//
// Flow:
//
//	start → book[Service, compensation:cancel-booking]
//	      → pay [Service, compensation:refund]
//	      → ship[Service] -- fails
//	             │ (boundary error, catch-all)
//	             ↓
//	         end-fail
//	      → end (normal path, not reached here)
//
// "ship" fails. A catch-all boundary error event catches the failure and routes
// to end-fail, so the instance reaches StatusCompleted WITHOUT triggering the
// engine's automatic unhandled-error compensation. This keeps the recorded
// compensation entries intact so an operator can then trigger an explicit
// rollback.
//
// After the forward run, the program delivers a CompensateRequested trigger
// (with an empty ToNode = full rollback). The engine invokes the compensation
// actions in REVERSE completion order — refund (for pay) before cancel-booking
// (for book) — and the instance ends StatusTerminated.
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// failError is a plain error returned by the failing "ship" action.
type failError struct{ msg string }

func (e *failError) Error() string { return e.msg }

func main() {
	ctx := context.Background()

	// book and pay each carry a compensation action; ship has none and fails.
	def, err := model.NewDefinition("booking-saga", 1).
		Add(model.NewStartEvent("start")).
		Add(model.NewServiceTask("book", model.WithActionName("book"),
			model.WithCompensation("cancel-booking"))).
		Add(model.NewServiceTask("pay", model.WithActionName("pay"),
			model.WithCompensation("refund"))).
		Add(model.NewServiceTask("ship", model.WithActionName("ship"))).
		// Catch-all boundary error keeps recorded compensations intact for the
		// explicit rollback below.
		Add(model.NewBoundaryEvent("ship-err", "ship",
			model.WithBoundaryErrorCode(""))).
		Add(model.NewEndEvent("end")).
		Add(model.NewEndEvent("end-fail")).
		Connect("start", "book").
		Connect("book", "pay").
		Connect("pay", "ship").
		Connect("ship", "end").          // normal path
		Connect("ship-err", "end-fail"). // failure path
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	// Record invocation order so the reverse-order rollback is observable.
	var invoked []string
	record := func(name string) action.ServiceAction {
		return action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			invoked = append(invoked, name)
			fmt.Printf("  [%s]\n", name)
			return nil, nil
		})
	}
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"book": record("book"),
		"pay":  record("pay"),
		"ship": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			invoked = append(invoked, "ship")
			fmt.Println("  [ship] FAILED — no carrier capacity")
			return nil, &failError{msg: "ship-failed"}
		}),
		"cancel-booking": record("cancel-booking"),
		"refund":         record("refund"),
	})

	clk := clock.System()
	store, err := runtime.NewMemStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}
	r := runtime.NewRunner(cat, store, runtime.WithRunnerClock(clk))

	const instanceID = "saga-001"

	fmt.Println("--- Booking Saga: Compensation Rollback ---")
	fmt.Println("Forward run:")

	st, err := r.Run(ctx, def, instanceID, nil)
	if err != nil {
		log.Fatal("run:", err)
	}
	fmt.Printf("forward outcome: status=%s (ship failure caught by boundary)\n",
		runtime.StatusString(st.Status))

	fmt.Println("Operator triggers full rollback:")
	trg := engine.NewCompensateRequested(clk.Now(), "") // "" = full rollback
	final, err := r.Deliver(ctx, def, instanceID, trg)
	if err != nil {
		log.Fatal("deliver compensate:", err)
	}

	fmt.Printf("rollback outcome: status=%s\n", runtime.StatusString(final.Status))
	fmt.Printf("invocation order: %v\n", invoked)
	fmt.Println("(compensations ran in reverse: refund before cancel-booking)")
}
