// Package main demonstrates a BPMN event-based gateway: after the gateway the
// instance arms SEVERAL catch events at once and takes the branch of whichever
// event fires FIRST — the losing arms are cancelled.
//
// Scenario: once an order is placed, the process waits for EITHER a payment
// confirmation OR a payment-window timeout (a timer). Whichever happens first
// decides the outcome:
//
//	start → gw[event-based] ─┬─ await-payment[catch message "payment-confirmed" key=order] → ship[Service]   → shipped-end
//	                         └─ payment-window[catch timer "24h"]                           → cancel[Service]  → cancelled-end
//
// The payment arm is a *correlated message*, NOT a signal. Payment confirmation is
// a per-order fact: "order-fast was paid" must resume ONLY order-fast, never a
// sibling order that happens to be parked at the same node. A signal broadcasts by
// name to every waiting instance, so it would wrongly ship every unpaid order; a
// message carries a correlation key (here the `order` variable) so DeliverMessage
// targets exactly one instance. Use a signal only for genuine fan-out (see the
// signal_broadcast example); use a correlated message for per-entity waits.
//
// Two instances show both resolutions of the same definition:
//   - order-fast: the "payment-confirmed" message for order-fast arrives first →
//     the ship branch runs and the 24h timer arm is cancelled.
//   - order-slow: no payment arrives; the fake clock advances past the 24h window
//     → the timer arm fires, the cancel branch runs, and the message arm is cancelled.
//
// A *clockwork.FakeClock drives both the engine and the gocron-backed scheduler so
// the timer branch is deterministic (advance the clock instead of waiting 24h). The
// message branch resolves synchronously through driver.DeliverMessage. Because the
// gocron scheduler fires on its own executor goroutine, a done channel closed from
// the fired branch's action makes the timer observation deterministic.
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/scheduling"
)

func main() {
	ctx := context.Background()

	// Build the process. gateway.NewEventBased fans out to two competing catch
	// events; the engine arms both (a correlated message waiter and a timer) and,
	// when one fires, routes down its flow and cancels the other.
	def, err := definition.NewBuilder("order-fulfillment", 1).
		Add(event.NewStart("start")).
		Add(gateway.NewEventBased("gw")).
		Add(event.NewIntermediateCatch("await-payment", event.WithCatchMessage("payment-confirmed", "order"))).
		Add(event.NewIntermediateCatch("payment-window", event.WithCatchTimer(schedule.AfterDuration(24*time.Hour)))).
		Add(activity.NewServiceTask("ship", activity.WithTaskAction("ship-order"))).
		Add(activity.NewServiceTask("cancel", activity.WithTaskAction("cancel-order"))).
		Add(event.NewEnd("shipped-end")).
		Add(event.NewEnd("cancelled-end")).
		Connect("start", "gw").
		Connect("gw", "await-payment").
		Connect("gw", "payment-window").
		Connect("await-payment", "ship").
		Connect("ship", "shipped-end").
		Connect("payment-window", "cancel").
		Connect("cancel", "cancelled-end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	// Buffered so the actions never block; each branch's action signals which arm won.
	shippedCh := make(chan struct{}, 1)
	cancelledCh := make(chan struct{}, 1)
	cat := action.NewCatalog(map[string]action.Action{
		"ship-order": action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
			fmt.Printf("  [ship-order] payment confirmed for %v — shipping\n", in["order"])
			shippedCh <- struct{}{}
			return map[string]any{"shipped": true}, nil
		}),
		"cancel-order": action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
			fmt.Printf("  [cancel-order] payment window elapsed for %v — cancelling\n", in["order"])
			cancelledCh <- struct{}{}
			return map[string]any{"cancelled": true}, nil
		}),
	})

	// One fake clock drives the engine and the scheduler (ADR-0003).
	startAt := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	clk := clockwork.NewFakeClockAt(startAt)

	sched, err := scheduling.NewScheduler(scheduling.WithClock(clk))
	if err != nil {
		log.Fatal("scheduler:", err)
	}
	defer func() { _ = sched.Close() }()

	store, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}

	// The correlated message arm is delivered via driver.DeliverMessage — no
	// SignalBus is needed (that is for broadcast signals). The runtime registers the
	// event-gateway message arm as a message waiter, so a delivered message with the
	// matching name+correlation key resumes exactly this instance.
	driver, err := runtime.NewProcessDriver(
		runtime.WithActionCatalog(cat),
		runtime.WithInstanceStore(store),
		runtime.WithClock(clk),
		runtime.WithScheduler(sched),
	)
	if err != nil {
		log.Fatal("driver:", err)
	}

	fmt.Println("--- Order Fulfillment: Event-Based Gateway (payment message vs. timeout) ---")

	// ── Instance 1: the payment message wins ──────────────────────────────────
	const fast = "order-fast"
	parked, err := driver.Drive(ctx, def, fast, map[string]any{"order": fast})
	if err != nil {
		log.Fatal("run fast:", err)
	}
	fmt.Printf("%s parked at the event gateway (status=%s) — both arms live\n", fast, parked.Status.String())

	// ApplyTrigger the payment for THIS order only. The correlation key ("order-fast",
	// the resolved value of the `order` variable) targets exactly this instance;
	// order-slow, parked on the same node, is untouched.
	fmt.Printf("payment confirmation arrives for %s\n", fast)
	if err := driver.DeliverMessage(ctx, def, "payment-confirmed", fast, map[string]any{"order": fast}); err != nil {
		log.Fatal("deliver payment:", err)
	}
	select {
	case <-shippedCh:
	case <-time.After(3 * time.Second):
		log.Fatal("timeout: ship branch did not run")
	}
	reportOutcome(ctx, store, fast)

	// ── Instance 2: the payment window times out ──────────────────────────────
	const slow = "order-slow"
	parked2, err := driver.Drive(ctx, def, slow, map[string]any{"order": slow})
	if err != nil {
		log.Fatal("run slow:", err)
	}
	fmt.Printf("%s parked at the event gateway (status=%s) — both arms live\n", slow, parked2.Status.String())

	// No payment arrives (order-slow is never subscribed nor published to). Advance
	// past the 24h window; the gocron executor fires the timer arm, which wins.
	fmt.Printf("no payment for %s — advancing the clock past the 24h window\n", slow)
	// Wait until gocron has armed its timer waiter on the fake clock before
	// advancing, so the advance deterministically fires the timer (never races
	// ahead of the waiter registration).
	if err := clk.BlockUntilContext(ctx, 1); err != nil {
		log.Fatal("block until timer armed:", err)
	}
	clk.Advance(24*time.Hour + time.Minute)
	select {
	case <-cancelledCh:
	case <-time.After(3 * time.Second):
		log.Fatal("timeout: cancel branch did not run")
	}
	reportOutcome(ctx, store, slow)
}

// reportOutcome polls briefly for the instance to reach a terminal state (the
// final commit can lag a hair behind an async timer fire) and prints the result.
func reportOutcome(ctx context.Context, store *kernel.MemInstanceStore, instanceID string) {
	var st engine.InstanceState
	for range 200 {
		loaded, _, err := store.Load(ctx, instanceID)
		if err == nil && loaded.Status == engine.StatusCompleted {
			st = loaded
			break
		}
		st = loaded
		time.Sleep(5 * time.Millisecond)
	}
	fmt.Printf("%s status=%s (reached %s)\n", instanceID, st.Status.String(), reachedEnd(st))
}

// reachedEnd returns the id of the terminal end node the instance visited, so the
// output makes clear which arm of the event gateway won.
func reachedEnd(st engine.InstanceState) string {
	for _, v := range st.History {
		switch v.NodeID {
		case "shipped-end", "cancelled-end":
			return v.NodeID
		}
	}
	return "?"
}
