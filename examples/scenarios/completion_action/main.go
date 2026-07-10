// Package main demonstrates activity.WithCompletionAction — a named catalog
// action the engine invokes synchronously when a UserTask (or ReceiveTask) is
// completed, before the token advances.
//
// Flow:
//
//	start → approve[UserTask, roles: manager, completion action "recordApproval"] → end
//
// WithCompletionAction("recordApproval") differs from a plain UserTask: on
// human completion the engine first merges the completion input into instance
// variables, then invokes the named catalog action with those variables as
// input, merges its output back into the instance variables, and only then
// advances the token — all within the ONE driver.ApplyTrigger call that
// delivers the completion trigger. No extra round-trip is needed, the same way
// the "reassign" service action in the sibling usertask_deadline example runs
// synchronously as part of a single step.
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
)

func main() {
	ctx := context.Background()

	def, err := definition.NewBuilder("expense-approval", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("approve", activity.WithEligibleRoles("manager"),
			activity.WithCompletionAction("recordApproval"),
		)).
		Add(event.NewEnd("end")).
		Connect("start", "approve").
		Connect("approve", "end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	// Named catalog action invoked by the engine on completion. It reads the
	// completion input (already merged into instance variables by the time it
	// runs) and returns vars that get merged back — the "domain record" side
	// effect a consumer would use to persist an approval decision elsewhere.
	cat := action.NewCatalog(map[string]action.Action{
		"recordApproval": action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
			fmt.Printf("  [recordApproval] domain record updated: approved=%v\n", in["approved"])
			return map[string]any{"recorded": true}, nil
		}),
	})

	// Human-task ports.
	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"manager": {manager},
	})
	az := authz.RoleAuthorizer{}
	clk := clock.System()

	memSt, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}
	driver, err := runtime.NewProcessDriver(
		runtime.WithActionCatalog(cat),
		runtime.WithInstanceStore(memSt),
		runtime.WithHumanTasks(resolver, taskStore, az),
	)
	if err != nil {
		log.Fatal("runner:", err)
	}

	const instanceID = "expense-001"

	fmt.Println("--- Expense Approval: Completion Action (WithCompletionAction) ---")

	// 1. Run → parks at the user task.
	parked, err := driver.Drive(ctx, def, instanceID, map[string]any{"amount": 4200})
	if err != nil {
		log.Fatal("run:", err)
	}
	fmt.Printf("parked at %q (status=%s)\n",
		parked.Tokens[0].NodeID, view.StatusString(parked.Status))

	// 2. Discover claimable tasks for the manager.
	claimable, err := taskStore.ClaimableBy(ctx, manager)
	if err != nil {
		log.Fatal("claimable:", err)
	}
	if len(claimable) == 0 {
		log.Fatal("expected a claimable task")
	}
	taskToken := claimable[0].TaskToken
	fmt.Printf("manager %q sees %d claimable task(s)\n", manager.ID, len(claimable))

	svc, err := task.NewTaskService(taskStore, az, task.WithClock(clk))
	if err != nil {
		log.Fatal("task service:", err)
	}

	// 3. Claim the task and deliver the trigger.
	claimTrg, err := svc.Claim(ctx, taskToken, manager)
	if err != nil {
		log.Fatal("claim:", err)
	}
	if _, err := driver.ApplyTrigger(ctx, def, instanceID, claimTrg); err != nil {
		log.Fatal("deliver claim:", err)
	}
	fmt.Println("task claimed")

	// 4. Complete the task with an approval decision. This ONE ApplyTrigger call
	// merges the completion input, invokes "recordApproval", merges its output,
	// and advances the token to the end event — the instance completes in the
	// same round-trip.
	completeTrg, err := svc.Complete(ctx, taskToken, manager,
		map[string]any{"approved": true})
	if err != nil {
		log.Fatal("complete:", err)
	}
	final, err := driver.ApplyTrigger(ctx, def, instanceID, completeTrg)
	if err != nil {
		log.Fatal("deliver complete:", err)
	}

	if final.Status == engine.StatusCompleted {
		fmt.Printf("instance completed — approved=%v recorded=%v\n",
			final.Variables["approved"], final.Variables["recorded"])
	} else {
		fmt.Printf("unexpected status: %s\n", view.StatusString(final.Status))
	}
}
