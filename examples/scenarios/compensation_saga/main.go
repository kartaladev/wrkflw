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
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
)

// failError is a plain error returned by the failing "ship" action.
type failError struct{ msg string }

func (e *failError) Error() string { return e.msg }

func main() {
	ctx := context.Background()

	// book and pay each carry a compensation action; ship has none and fails.
	def, err := definition.NewBuilder("booking-saga", 1).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("book", activity.WithActionName("book"),
			activity.WithCompensation("cancel-booking"))).
		Add(activity.NewServiceTask("pay", activity.WithActionName("pay"),
			activity.WithCompensation("refund"))).
		Add(activity.NewServiceTask("ship", activity.WithActionName("ship"))).
		// Catch-all boundary error keeps recorded compensations intact for the
		// explicit rollback below.
		Add(event.NewBoundary("ship-err", "ship",
			event.WithBoundaryErrorCode(""))).
		Add(event.NewEnd("end")).
		Add(event.NewEnd("end-fail")).
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
	record := func(name string) action.Action {
		return action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			invoked = append(invoked, name)
			fmt.Printf("  [%s]\n", name)
			return nil, nil
		})
	}
	cat := action.NewMapCatalog(map[string]action.Action{
		"book": record("book"),
		"pay":  record("pay"),
		"ship": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			invoked = append(invoked, "ship")
			fmt.Println("  [ship] FAILED — no carrier capacity")
			return nil, &failError{msg: "ship-failed"}
		}),
		"cancel-booking": record("cancel-booking"),
		"refund":         record("refund"),
	})

	clk := clock.System()
	store, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}
	driver, err := runtime.NewProcessDriver(runtime.WithActionCatalog(cat), runtime.WithInstanceStore(store), runtime.WithClock(clk))
	if err != nil {
		log.Fatal("runner:", err)
	}

	const instanceID = "saga-001"

	fmt.Println("--- Booking Saga: Compensation Rollback ---")
	fmt.Println("Forward run:")

	st, err := driver.Drive(ctx, def, instanceID, nil)
	if err != nil {
		log.Fatal("run:", err)
	}
	fmt.Printf("forward outcome: status=%s (ship failure caught by boundary)\n",
		view.StatusString(st.Status))

	fmt.Println("Operator triggers full rollback:")
	trg := engine.NewCompensateRequested(clk.Now(), "") // "" = full rollback
	final, err := driver.Deliver(ctx, def, instanceID, trg)
	if err != nil {
		log.Fatal("deliver compensate:", err)
	}

	fmt.Printf("rollback outcome: status=%s\n", view.StatusString(final.Status))
	fmt.Printf("invocation order: %v\n", invoked)
	fmt.Println("(compensations ran in reverse: refund before cancel-booking)")
}
