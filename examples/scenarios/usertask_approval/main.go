// Package main demonstrates a human-task approval lifecycle end to end.
//
// Flow:
//
//	start → approve[UserTask, roles: manager] → end
//
// A UserTask parks the instance: driver.Drive drives until the task is reached, then
// returns with the instance still StatusRunning. A human then:
//
//  1. discovers claimable tasks via TaskStore.ClaimableBy,
//  2. claims the task   — TaskService.Claim → driver.ApplyTrigger(claimTrigger),
//  3. completes the task — TaskService.Complete → driver.ApplyTrigger(completeTrigger),
//
// which drives the instance to completion. The completion output is merged into
// the instance variables.
//
// This mirrors runtime/human_example_test.go (TestHumanTaskEndToEnd), which is
// the authoritative reference for the full lifecycle and assertions.
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/clock"
	"github.com/kartaladev/wrkflw/definition"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/runtime/task"
	"github.com/kartaladev/wrkflw/runtime/view"
)

func main() {
	ctx := context.Background()

	def, err := definition.NewBuilder("expense-approval", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("approve", activity.WithEligibleRoles("manager"))).
		Add(event.NewEnd("end")).
		Connect("start", "approve").
		Connect("approve", "end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

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
	// No service-action catalog needed; the default catalog covers it.
	driver, err := runtime.NewProcessDriver(
		runtime.WithInstanceStore(memSt),
		runtime.WithHumanTasks(resolver, taskStore, az),
	)
	if err != nil {
		log.Fatal("runner:", err)
	}

	const instanceID = "expense-001"

	fmt.Println("--- Expense Approval: Human Task ---")

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

	// 4. Complete the task with an approval decision and deliver the trigger.
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
		fmt.Printf("instance completed — approved=%v\n", final.Variables["approved"])
	} else {
		fmt.Printf("unexpected status: %s\n", view.StatusString(final.Status))
	}
}
