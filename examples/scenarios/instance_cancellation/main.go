// Package main demonstrates cancelling a running process instance mid-flight
// via ProcessDriver.CancelInstance, and the best-effort cleanup that runs on the
// way out.
//
// A process instance may need to be terminated before it reaches an end event —
// an order is retracted, a request is withdrawn, an operator aborts a stuck run.
// CancelInstance delivers a CancelRequested trigger that:
//
//  1. runs the definition-level CancelActions (in declared order) best-effort —
//     these are compensating/cleanup Actions (release a hold, notify a
//     customer, roll back an external side effect). A CancelAction that fails or
//     is unresolved is logged and skipped; it never fails the cancel (ADR-0028).
//  2. clears all live tokens and marks the instance StatusTerminated, and
//  3. reconciles the human-task projection: every parked task is marked
//     Cancelled so it no longer surfaces in an inbox query (ADR-0088).
//
// When definition-level compensation records exist, cancel runs the reverse-order
// compensation walk first (see the compensation_saga scenario); here there are
// none, so termination is immediate. When CallLinks and a DefinitionRegistry are
// both wired, CancelInstance also propagates recursively to running async child
// instances (ADR-0032) — see the call_activity scenario for parent/child wiring.
//
// Flow:
//
//	start → fulfil[UserTask, roles: fulfiller] → end
//	         (parks here; the operator cancels before it is claimed)
//
// CancelActions on the definition: "release-inventory", then "notify-customer".
//
// A *clockwork.FakeClock keeps the run deterministic; no real time passes.
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jonboulle/clockwork"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
)

func main() {
	ctx := context.Background()

	// Build the process. CancelActions lists cleanup Actions the engine
	// invokes best-effort, in order, when the instance is cancelled.
	def, err := definition.NewBuilder("order-fulfilment", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("fulfil", []string{"fulfiller"})).
		Add(event.NewEnd("end")).
		Connect("start", "fulfil").
		Connect("fulfil", "end").
		CancelActions("release-inventory", "notify-customer").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	// Cleanup actions. "notify-customer" fails on purpose to show that a failing
	// (or unresolved) CancelAction is logged and skipped — it never fails the
	// cancel. Order is preserved: release-inventory runs before notify-customer.
	var ran []string
	cat := action.NewMapCatalog(map[string]action.Action{
		"release-inventory": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			ran = append(ran, "release-inventory")
			fmt.Println("  [release-inventory] returning the reserved stock to the pool")
			return nil, nil
		}),
		"notify-customer": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			ran = append(ran, "notify-customer")
			fmt.Println("  [notify-customer] send failed — logged and skipped, cancel still succeeds")
			return nil, errors.New("mail gateway unavailable")
		}),
	})

	clk := clockwork.NewFakeClock()

	// Human-task wiring so the UserTask parks (the instance stays Running until we
	// cancel it).
	fulfiller := authz.Actor{ID: "sam", Roles: []string{"fulfiller"}}
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"fulfiller": {fulfiller},
	})
	store, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}

	r, err := runtime.NewProcessDriver(
		runtime.WithActionCatalog(cat),
		runtime.WithInstanceStore(store),
		runtime.WithClock(clk),
		runtime.WithHumanTasks(resolver, taskStore, authz.RoleAuthorizer{}),
	)
	if err != nil {
		log.Fatal("runner:", err)
	}

	const instanceID = "order-9001"

	fmt.Println("--- Order Fulfilment: Instance Cancellation ---")

	// 1. Run → parks at the user task; the instance is Running.
	parked, err := r.Run(ctx, def, instanceID, map[string]any{"orderID": "9001"})
	if err != nil {
		log.Fatal("run:", err)
	}
	fmt.Printf("parked at %q (status=%s, live tokens=%d)\n",
		parked.Tokens[0].NodeID, view.StatusString(parked.Status), len(parked.Tokens))

	// Capture the parked task token so we can observe it transition to Cancelled.
	claimable, err := taskStore.ClaimableBy(ctx, fulfiller)
	if err != nil {
		log.Fatal("claimable:", err)
	}
	if len(claimable) == 0 {
		log.Fatal("expected a claimable task before cancel")
	}
	taskToken := claimable[0].TaskToken

	// 2. The order is retracted before anyone works it — cancel the instance.
	//    CancelActions run best-effort; the failing "notify-customer" is swallowed.
	fmt.Println("cancelling instance (running cleanup actions)...")
	final, err := r.CancelInstance(ctx, def, instanceID)
	if err != nil {
		log.Fatal("cancel:", err)
	}

	// 3. Report the outcome: Terminated, no live tokens, both cleanup actions
	//    attempted in declared order (the failing one did not abort the cancel),
	//    and the parked human task reconciled to Cancelled.
	fmt.Printf("instance status=%s, live tokens=%d\n",
		view.StatusString(final.Status), len(final.Tokens))
	fmt.Printf("cancel actions attempted (in order): %v\n", ran)

	task, err := taskStore.Get(ctx, taskToken)
	if err != nil {
		log.Fatal("get task:", err)
	}
	fmt.Printf("human task %q state: %s\n", task.TaskToken, task.State.String())

	if final.Status == engine.StatusTerminated &&
		len(final.Tokens) == 0 &&
		task.State == humantask.Cancelled {
		fmt.Println("OK: instance terminated, tokens cleared, cleanup attempted, task cancelled")
	} else {
		fmt.Println("unexpected outcome")
	}
}
