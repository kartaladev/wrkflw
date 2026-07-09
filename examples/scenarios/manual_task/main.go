// Package main demonstrates a manual user task: a form-less human checkpoint.
//
// Flow:
//
//	start → handOverBadge[UserTask, manual, no roles] → end
//
// A manual task parks the instance like any user task, but it carries no
// eligibility (authorization is deferred to the consumer's transport layer, see
// ADR-0117) and completes on a bare trigger with no payload (ADR-0118).
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zakyalvan/krtlwrkflw/authz"
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

	def, err := definition.NewBuilder("employee-onboarding", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("handOverBadge", activity.WithManual())).
		Add(event.NewEnd("end")).
		Connect("start", "handOverBadge").
		Connect("handOverBadge", "end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	taskStore := humantask.NewMemTaskStore()
	memSt, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}
	driver, err := runtime.NewProcessDriver(
		runtime.WithInstanceStore(memSt),
		runtime.WithHumanTasks(humantask.NewStaticActorResolver(nil), taskStore, authz.RoleAuthorizer{}),
	)
	if err != nil {
		log.Fatal("driver:", err)
	}

	const instanceID = "onboarding-001"
	fmt.Println("--- Employee Onboarding: Manual Task ---")

	parked, err := driver.Drive(ctx, def, instanceID, nil)
	if err != nil {
		log.Fatal("drive:", err)
	}
	fmt.Printf("parked at %q (status=%s)\n", parked.Tokens[0].NodeID, view.StatusString(parked.Status))

	// Find the open manual task (Tasks accumulates — use IsOpen()).
	var token string
	for i := range parked.Tasks {
		if parked.Tasks[i].IsOpen() {
			token = parked.Tasks[i].TaskToken
			break
		}
	}
	if token == "" {
		log.Fatal("expected an open manual task")
	}

	svc, err := task.NewTaskService(taskStore, authz.RoleAuthorizer{})
	if err != nil {
		log.Fatal("task service:", err)
	}

	// Bare completion — no claim, no payload.
	trg, err := svc.Complete(ctx, token, authz.Actor{ID: "operator"}, nil)
	if err != nil {
		log.Fatal("complete:", err)
	}
	final, err := driver.ApplyTrigger(ctx, def, instanceID, trg)
	if err != nil {
		log.Fatal("apply complete:", err)
	}

	if final.Status == engine.StatusCompleted {
		fmt.Println("instance completed — manual step confirmed")
	} else {
		fmt.Printf("unexpected status: %s\n", view.StatusString(final.Status))
	}
}
