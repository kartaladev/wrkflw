// Package main demonstrates message BOUNDARY events attached to an activity —
// the true event.NewBoundary API, distinct from the activity WithDeadline shown
// in the sibling usertask_deadline example.
//
// An order-approval process parks at a UserTask("approve") that hosts TWO
// message boundary events, showing both interrupting and non-interrupting
// behavior on the same host:
//
//   - INTERRUPTING boundary "bnd-cancel" (message "order.cancel"): delivering
//     the cancel message interrupts the approval task (marks it Cancelled) and
//     routes the token to end-cancelled.
//   - NON-INTERRUPTING boundary "bnd-remind" (message "order.remind"):
//     delivering the reminder message spawns an ADDITIONAL token down a
//     reminder side-path (notify → end-reminded) WITHOUT disturbing the
//     still-parked approval task.
//
// Both boundaries correlate on the order's business key ("orderID"), so a
// delivered cancel/remind targets exactly THIS order's instance. Delivering a
// message with an empty correlation key would instead broadcast by message name
// to every instance parked on that message — almost never the intent for a
// per-order cancel or reminder.
//
// Flow:
//
//	start → approve[UserTask] ──(approver approves)──────────→ end-approved
//	             ├─◄ order.cancel  (interrupting)   → end-cancelled
//	             └─◌ order.remind  (non-interrupting)→ notify[Service] → end-reminded
//
// Driving sequence (fully deterministic — message delivery drives everything,
// so no clock or scheduler is needed):
//
//  1. Run parks the instance at "approve"; both message boundary waiters arm.
//  2. ApplyTrigger "order.remind": the non-interrupting boundary fires ONCE — the
//     "notify" reminder action runs, but the instance is STILL running and
//     still parked at "approve". (A non-interrupting boundary fires once then
//     de-arms; a second reminder delivery would be a clean no-op.)
//  3. ApplyTrigger "order.cancel": the interrupting boundary fires — the human task
//     is Cancelled and the instance completes via the end-cancelled path.
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

func main() {
	ctx := context.Background()

	// Build the process. A boundary event is a separate node attached to a host
	// activity via its second argument; its single outgoing sequence flow (added
	// with Connect) is the path taken when the boundary fires.
	//
	// Both message boundaries correlate on the "orderID" variable (resolved from
	// the instance's variables when the boundary arms). Correlation targets the
	// delivered message to THIS order: DeliverMessage(name, key, …) fires only the
	// boundary whose resolved key matches. An empty correlation key would instead
	// broadcast by message name to EVERY instance parked on that message — almost
	// never the intent, so a per-order cancel/remind must be correlated.
	def, err := definition.NewBuilder("order-approval", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("approve", []string{"approver"})).
		// Interrupting message boundary: cancels the approval on "order.cancel"
		// correlated to this order.
		Add(event.NewBoundary("bnd-cancel", "approve",
			event.WithMessageCorrelator("order.cancel", "orderID"))).
		// Non-interrupting message boundary: reminds without interrupting,
		// correlated to this order.
		Add(event.NewBoundary("bnd-remind", "approve",
			event.WithMessageCorrelator("order.remind", "orderID"),
			event.WithBoundaryNonInterrupting())).
		Add(activity.NewServiceTask("notify", activity.WithTaskAction("notify-approver"))).
		Add(event.NewEnd("end-approved")).
		Add(event.NewEnd("end-cancelled")).
		Add(event.NewEnd("end-reminded")).
		Connect("start", "approve").
		Connect("approve", "end-approved").     // normal completion path
		Connect("bnd-cancel", "end-cancelled"). // interrupting boundary flow
		Connect("bnd-remind", "notify").        // non-interrupting boundary flow
		Connect("notify", "end-reminded").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	reminded := false
	cat := action.NewCatalog(map[string]action.Action{
		// Runs on the non-interrupting reminder side-path.
		"notify-approver": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			reminded = true
			fmt.Println("  [notify-approver] reminder sent — approval is still pending")
			return map[string]any{"reminded": true}, nil
		}),
	})

	// Human-task wiring is required for the UserTask to park correctly.
	approver := authz.Actor{ID: "alice", Roles: []string{"approver"}}
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"approver": {approver},
	})
	store, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}

	driver, err := runtime.NewProcessDriver(
		runtime.WithActionCatalog(cat),
		runtime.WithInstanceStore(store),
		runtime.WithHumanTasks(resolver, taskStore, authz.RoleAuthorizer{}),
	)
	if err != nil {
		log.Fatal("driver:", err)
	}

	const instanceID = "order-42"
	// orderID is the order's business correlation key (here it equals the order
	// number). The boundaries correlate on it, so a delivered cancel/remind
	// targets exactly this order rather than broadcasting to all instances.
	vars := map[string]any{"orderID": instanceID}

	fmt.Println("--- Order Approval: Message Boundary Events ---")

	// 1) Run parks at the user task; both message boundaries arm, each resolving
	//    its correlation key from vars["orderID"].
	parked, err := driver.Drive(ctx, def, instanceID, vars)
	if err != nil {
		log.Fatal("run:", err)
	}
	fmt.Printf("instance parked at %q (status=%s, boundaries armed=%d)\n",
		parked.Tokens[0].NodeID, parked.Status.String(), len(parked.Boundaries))

	// 2) ApplyTrigger the reminder message: the NON-INTERRUPTING boundary fires once.
	//    The reminder runs, but the approval task stays parked and running.
	fmt.Println("delivering order.remind (non-interrupting), correlated to this order...")
	if err := driver.DeliverMessage(ctx, def, "order.remind", instanceID, nil); err != nil {
		log.Fatal("deliver remind:", err)
	}
	afterRemind, _, err := store.Load(ctx, instanceID)
	if err != nil {
		log.Fatal("load after remind:", err)
	}
	fmt.Printf("after reminder: status=%s (still parked at %q), reminded=%v\n",
		afterRemind.Status.String(), afterRemind.Tokens[0].NodeID, reminded)

	// 3) ApplyTrigger the cancel message: the INTERRUPTING boundary fires — the human
	//    task is Cancelled and the instance completes via the cancelled path.
	fmt.Println("delivering order.cancel (interrupting), correlated to this order...")
	if err := driver.DeliverMessage(ctx, def, "order.cancel", instanceID, nil); err != nil {
		log.Fatal("deliver cancel:", err)
	}
	final, _, err := store.Load(ctx, instanceID)
	if err != nil {
		log.Fatal("load final:", err)
	}

	if final.Status == engine.StatusCompleted && reminded && len(final.Tokens) == 0 {
		fmt.Println("order cancelled via the interrupting boundary after a reminder — completed cleanly")
	} else {
		fmt.Printf("unexpected outcome: status=%s reminded=%v tokens=%d\n",
			final.Status.String(), reminded, len(final.Tokens))
	}
}
