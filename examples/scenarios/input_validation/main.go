// Package main demonstrates optional, definition-declared input validation at
// the manually-provided start-vars boundary (ProcessDriver.Drive). The
// human-task completion-output boundary (TaskService.Complete) is being
// redesigned to an engine-decides / runtime-executes path (see
// docs/specs/2026-07-09-input-validation-redesign.md) and is temporarily
// unenforced; it will be re-added here once that path lands. The message
// boundary (DeliverMessage) is exercised in
// runtime/processdriver_message_validation_test.go and is not repeated here.
//
// Flow:
//
//	start[validated: amount > 0] → approve[UserTask, roles: manager] → end
//
// The start-vars boundary uses the expr-lang adapter
// (definition/model/validate/expr): a [validate.ValidationStrategy] attached
// to a node via [event.WithInputValidation]. The ProcessDriver validates
// start vars BEFORE any instance is created — "input-owner validates"
// placement: whoever accepts the external input validates it, not the engine
// core.
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	vexpr "github.com/zakyalvan/krtlwrkflw/definition/model/validate/expr"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
	"github.com/zakyalvan/krtlwrkflw/runtime/validation"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
)

func main() {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Build the definition: the start event carries an expr-lang validation strategy.
	def, err := definition.NewBuilder("expense-approval-validated", 1).
		Add(event.NewStart("start", event.WithInputValidation(vexpr.New("amount > 0")))).
		Add(activity.NewUserTask("approve", []string{"manager"})).
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
	driver, err := runtime.NewProcessDriver(
		runtime.WithInstanceStore(memSt),
		runtime.WithHumanTasks(resolver, taskStore, az),
	)
	if err != nil {
		log.Fatal("driver:", err)
	}

	svc, err := task.NewTaskService(taskStore, az, task.WithClock(clk))
	if err != nil {
		log.Fatal("task service:", err)
	}

	fmt.Println("--- Input Validation: start vars ---")

	// 1. REJECTED Drive: amount <= 0 fails the start event's InputValidation.
	//    No instance is created — the gate runs before any state mutation.
	_, err = driver.Drive(ctx, def, "expense-rejected", map[string]any{"amount": -50})
	logOutcome(logger, "drive start vars (amount=-50)", err)
	if !errors.Is(err, validation.ErrInvalidInput) {
		log.Fatalf("expected ErrInvalidInput, got: %v", err)
	}

	// 2. ACCEPTED Drive: amount > 0 passes validation; the instance parks at approve.
	const instanceID = "expense-accepted"
	parked, err := driver.Drive(ctx, def, instanceID, map[string]any{"amount": 4200})
	if err != nil {
		log.Fatal("drive (valid vars):", err)
	}
	logOutcome(logger, "drive start vars (amount=4200)", nil)
	fmt.Printf("parked at %q (status=%s)\n", parked.Tokens[0].NodeID, view.StatusString(parked.Status))

	// Discover and claim the resulting task.
	claimable, err := taskStore.ClaimableBy(ctx, manager)
	if err != nil {
		log.Fatal("claimable:", err)
	}
	if len(claimable) == 0 {
		log.Fatal("expected a claimable task")
	}
	taskToken := claimable[0].TaskToken

	claimTrg, err := svc.Claim(ctx, taskToken, manager)
	if err != nil {
		log.Fatal("claim:", err)
	}
	if _, err := driver.ApplyTrigger(ctx, def, instanceID, claimTrg); err != nil {
		log.Fatal("deliver claim:", err)
	}
	fmt.Println("task claimed by", manager.ID)

	// 3. Complete: completion-output validation is temporarily unenforced
	// (see the package doc comment); any output is accepted here.
	completeTrg, err := svc.Complete(ctx, taskToken, manager, map[string]any{"decision": "approve"})
	if err != nil {
		log.Fatal("complete (valid output):", err)
	}
	logOutcome(logger, `complete output (decision="approve")`, nil)

	final, err := driver.ApplyTrigger(ctx, def, instanceID, completeTrg)
	if err != nil {
		log.Fatal("deliver complete:", err)
	}
	if final.Status != engine.StatusCompleted {
		log.Fatalf("unexpected final status: %s", view.StatusString(final.Status))
	}
	fmt.Printf("instance completed — decision=%v\n", final.Variables["decision"])
}

// logOutcome logs a validation checkpoint's result at the appropriate level:
// a rejection (err wrapping validation.ErrInvalidInput) is logged as a WARN
// with the reason, an unexpected error as ERROR, and success as INFO.
func logOutcome(logger *slog.Logger, step string, err error) {
	switch {
	case err == nil:
		logger.Info("validation accepted", slog.String("step", step))
	case errors.Is(err, validation.ErrInvalidInput):
		logger.Warn("validation rejected", slog.String("step", step), slog.String("reason", err.Error()))
	default:
		logger.Error("unexpected error", slog.String("step", step), slog.String("error", err.Error()))
	}
}
