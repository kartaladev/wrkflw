// Package main demonstrates an embedded sub-process.
//
// A SubProcess embeds a nested ProcessDefinition that the engine runs as its own
// scope. When the nested definition completes, the parent token continues past
// the sub-process node. Variables produced inside the sub-process are merged back
// into the parent instance.
//
// Flow:
//
//	parent:  start → reserve-hotel[SubProcess] → send-confirmation[Service] → end
//
//	nested:  hotel-start → book-room[Service] → hotel-end
//
// Contrast with a CallActivity, which references a SEPARATE top-level definition
// by name (resolved through a DefinitionRegistry) rather than embedding it. Use a
// SubProcess for a scope that is private to one definition; use a CallActivity to
// reuse a standalone, independently-versioned definition.
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
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
)

func main() {
	ctx := context.Background()

	// The nested definition embedded inside the sub-process node.
	hotel, err := definition.NewBuilder("hotel-reservation", 1).
		Add(event.NewStart("hotel-start")).
		Add(activity.NewServiceTask("book-room", activity.WithActionName("book-room"))).
		Add(event.NewEnd("hotel-end")).
		Connect("hotel-start", "book-room").
		Connect("book-room", "hotel-end").
		Build()
	if err != nil {
		log.Fatal("build nested def:", err)
	}

	// The parent definition embeds the nested definition as a SubProcess.
	def, err := definition.NewBuilder("travel-booking", 1).
		Add(event.NewStart("start")).
		Add(activity.NewSubProcess("reserve-hotel", hotel)).
		Add(activity.NewServiceTask("send-confirmation", activity.WithActionName("send-confirmation"))).
		Add(event.NewEnd("end")).
		Connect("start", "reserve-hotel").
		Connect("reserve-hotel", "send-confirmation").
		Connect("send-confirmation", "end").
		Build()
	if err != nil {
		log.Fatal("build parent def:", err)
	}

	cat := action.NewMapCatalog(map[string]action.Action{
		"book-room": action.ActionFunc(func(_ context.Context, vars map[string]any) (map[string]any, error) {
			fmt.Printf("  [book-room] reserving a room in %v\n", vars["city"])
			return map[string]any{"confirmation": "HOTEL-7788"}, nil
		}),
		"send-confirmation": action.ActionFunc(func(_ context.Context, vars map[string]any) (map[string]any, error) {
			fmt.Printf("  [send-confirmation] emailing confirmation %v\n", vars["confirmation"])
			return map[string]any{"emailed": true}, nil
		}),
	})

	memSt, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}
	r, err := runtime.NewProcessDriver(runtime.WithActionCatalog(cat), runtime.WithInstanceStore(memSt))
	if err != nil {
		log.Fatal("runner:", err)
	}

	fmt.Println("--- Travel Booking: Embedded Sub-process ---")
	state, err := r.Run(ctx, def, "trip-001", map[string]any{"city": "Lisbon"})
	if err != nil {
		log.Fatal("run:", err)
	}

	if state.Status == engine.StatusCompleted {
		fmt.Println("trip booked!")
		fmt.Println("  hotel confirmation:", state.Variables["confirmation"])
		fmt.Println("  email sent:", state.Variables["emailed"])
	} else {
		fmt.Printf("unexpected status: %s\n", view.StatusString(state.Status))
	}
}
