// Package main demonstrates both manual-task completion modes (ADR-0118): a
// form-less human checkpoint that either waits for a bare operator trigger, or
// auto-completes immediately as a documentation marker.
//
// Flow:
//
//	start → handOverBadge[UserTask, manual, wait]      → recordOrientation[UserTask, manual, immediate] → end
//
// Both nodes carry no eligibility (authorization is deferred to the
// consumer's transport layer, see ADR-0117):
//
//   - handOverBadge uses WithManual(false): the instance parks like any user
//     task, and the operator completes it with a bare trigger — no claim, no
//     payload. Attempting to complete it with a non-empty output is rejected
//     with engine.ErrManualTaskPayload (see runtime/manual_task_test.go).
//   - recordOrientation uses WithManual(true): the node never parks. It
//     auto-completes on entry, the engine records a completed human task for
//     audit, and the token advances without waiting for any trigger.
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
		Add(activity.NewUserTask("handOverBadge", activity.WithManual(false))).
		Add(activity.NewUserTask("recordOrientation", activity.WithManual(true))).
		Add(event.NewEnd("end")).
		Connect("start", "handOverBadge").
		Connect("handOverBadge", "recordOrientation").
		Connect("recordOrientation", "end").
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
	fmt.Println("--- Employee Onboarding: Manual Task (wait + immediate modes) ---")

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

	// Bare completion of the wait-mode step — no claim, no payload. A
	// non-nil output here would be rejected with engine.ErrManualTaskPayload.
	trg, err := svc.Complete(ctx, token, authz.Actor{ID: "operator"}, nil)
	if err != nil {
		log.Fatal("complete:", err)
	}
	fmt.Println("handOverBadge completed on bare trigger (wait mode, no payload)")

	final, err := driver.ApplyTrigger(ctx, def, instanceID, trg)
	if err != nil {
		log.Fatal("apply complete:", err)
	}

	// recordOrientation is immediate-mode: the token flowed straight through it
	// with no further trigger, and a completed task was recorded for audit.
	for i := range final.Tasks {
		if final.Tasks[i].NodeID == "recordOrientation" && final.Tasks[i].State == humantask.Completed {
			fmt.Println("recordOrientation auto-completed on entry (immediate mode, recorded for audit)")
			break
		}
	}

	if final.Status == engine.StatusCompleted {
		fmt.Println("instance completed — both manual steps confirmed")
	} else {
		fmt.Printf("unexpected status: %s\n", view.StatusString(final.Status))
	}
}
