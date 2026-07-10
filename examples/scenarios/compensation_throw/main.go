// Package main demonstrates a scope-wide compensation throw (ADR-0120):
// throw-then-continue, non-terminating, intra-process compensation.
//
// Flow:
//
//	start → reserveHotel[Service, compensate:cancel-hotel]
//	      → reserveCar  [Service, compensate:cancel-car] -- inventory desync found
//	      → validate    [ExclusiveGateway]
//	           carAvailable == false → rollback[CompensationThrowEvent, scope-wide]
//	           default (carAvailable == true) → notifyCustomer
//	      rollback → notifyCustomer[Service]
//	      notifyCustomer → end
//
// reserveHotel and reserveCar both succeed provisionally, but reserveCar's
// downstream availability check comes back false — an inventory-sync failure
// only discoverable after both bookings were already placed. The exclusive
// gateway routes to "rollback", a CompensationThrowEvent
// (event.NewCompensateThrow("rollback"), no CompensateRef = scope-wide).
//
// Reaching "rollback" does NOT fail or terminate the instance. The engine
// walks the throwing scope's recorded compensable activities in REVERSE
// completion order — cancel-car (for reserveCar) before cancel-hotel (for
// reserveHotel) — then RESUMES at the throw's single outgoing successor,
// notifyCustomer, and the instance still reaches StatusCompleted normally.
// This is throw-then-continue: the throw is a step in the flow, not an exit.
//
// Contrast with a signal throw (event.NewIntermediateThrow +
// WithThrowSignalName): a signal BROADCASTS cross-instance, to every OTHER
// instance currently waiting on that name. A compensation throw never leaves
// this instance — it only unwinds work THIS process already did
// (InstanceState.RootCompensations), then continues within the same walk. No
// operator-triggered ApplyTrigger is needed here, unlike the explicit,
// operator-driven full rollback in examples/scenarios/compensation_saga: this
// throw fires automatically the moment the token reaches it during Drive.
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
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
)

func main() {
	ctx := context.Background()

	def, err := definition.NewBuilder("trip-booking-saga", 1).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("reserveHotel", activity.WithTaskAction("reserve-hotel"),
			activity.WithCompensateAction("cancel-hotel"))).
		Add(activity.NewServiceTask("reserveCar", activity.WithTaskAction("reserve-car"),
			activity.WithCompensateAction("cancel-car"))).
		Add(gateway.NewExclusive("validate")).
		Add(event.NewCompensateThrow("rollback")). // scope-wide: no WithCompensateRef
		Add(activity.NewServiceTask("notifyCustomer", activity.WithTaskAction("notify-customer"))).
		Add(event.NewEnd("end")).
		Connect("start", "reserveHotel").
		Connect("reserveHotel", "reserveCar").
		Connect("reserveCar", "validate").
		Connect("validate", "rollback", flow.WithCondition("carAvailable == false")).
		Connect("validate", "notifyCustomer", flow.AsDefault()).
		Connect("rollback", "notifyCustomer").
		Connect("notifyCustomer", "end").
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
	cat := action.NewCatalog(map[string]action.Action{
		"reserve-hotel": record("reserve-hotel"),
		"reserve-car": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			invoked = append(invoked, "reserve-car")
			fmt.Println("  [reserve-car] booked provisionally — inventory desync detected downstream")
			return map[string]any{"carAvailable": false}, nil
		}),
		"cancel-hotel": record("cancel-hotel"),
		"cancel-car":   record("cancel-car"),
		"notify-customer": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			invoked = append(invoked, "notify-customer")
			fmt.Println("  [notify-customer] car unavailable — booking rolled back, customer notified")
			return nil, nil
		}),
	})

	clk := clock.System()
	store, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}
	driver, err := runtime.NewProcessDriver(runtime.WithActionCatalog(cat), runtime.WithInstanceStore(store), runtime.WithClock(clk))
	if err != nil {
		log.Fatal("driver:", err)
	}

	const instanceID = "trip-001"

	fmt.Println("--- Trip Booking Saga: Scope-Wide Compensation Throw ---")

	st, err := driver.Drive(ctx, def, instanceID, nil)
	if err != nil {
		log.Fatal("drive:", err)
	}

	fmt.Printf("outcome: status=%s (throw-then-continue — instance completed, not terminated)\n",
		view.StatusString(st.Status))
	fmt.Printf("invocation order: %v\n", invoked)
	fmt.Println("(rollback ran in reverse: cancel-car before cancel-hotel, then execution continued to notify-customer)")

	if st.Status != engine.StatusCompleted {
		log.Fatalf("unexpected status: %v", st.Status)
	}
}
