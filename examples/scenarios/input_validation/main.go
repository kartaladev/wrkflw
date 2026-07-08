// Package main demonstrates optional, definition-declared input validation at
// two of the three external-input boundaries: manually-provided start vars
// (ProcessDriver.Drive) and human-task completion output (TaskService.Complete).
// The third boundary — message payloads (DeliverMessage) — is exercised in
// runtime/processdriver_message_validation_test.go and is not repeated here.
//
// Flow:
//
//	start[validated: amount > 0] → approve[UserTask, roles: manager,
//	  validated: decision in ['approve','reject']] → end
//
// Both boundaries use the expr-lang adapter (validation/expr): a
// [validation.ValidationStrategy] attached to a node via
// [event.WithInputValidation] / [activity.WithCompletionValidation]. The
// ProcessDriver validates start vars BEFORE any instance is created; the
// TaskService validates completion output AFTER authorization succeeds but
// BEFORE issuing the completion trigger — see TaskService.Complete's doc
// comment. Both share the "input-owner validates" placement: whoever accepts
// the external input validates it, not the engine core.
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
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
	"github.com/zakyalvan/krtlwrkflw/validation"
	vexpr "github.com/zakyalvan/krtlwrkflw/validation/expr"
)

func main() {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Build the definition: both boundaries carry an expr-lang validation strategy.
	def, err := definition.NewBuilder("expense-approval-validated", 1).
		Add(event.NewStart("start", event.WithInputValidation(vexpr.New("amount > 0")))).
		Add(activity.NewUserTask("approve", []string{"manager"},
			activity.WithCompletionValidation(vexpr.New(`decision in ['approve','reject']`)))).
		Add(event.NewEnd("end")).
		Connect("start", "approve").
		Connect("approve", "end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	// A registry indexing this definition lets TaskService.Complete resolve the
	// completing node's CompletionValidation strategy by Qualifier (def id + version,
	// carried on the HumanTask since the task was created by driving this def).
	registry := kernel.NewMapDefinitionRegistry(def)

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

	// Wiring the DefinitionResolver opts Complete into completion-output validation.
	svc, err := task.NewTaskService(taskStore, az,
		task.WithClock(clk),
		task.WithDefinitionResolver(registry),
	)
	if err != nil {
		log.Fatal("task service:", err)
	}

	fmt.Println("--- Input Validation: start vars + completion output ---")

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

	// 3. REJECTED Complete: "maybe" is not in ['approve','reject'].
	_, err = svc.Complete(ctx, taskToken, manager, map[string]any{"decision": "maybe"})
	logOutcome(logger, `complete output (decision="maybe")`, err)
	if !errors.Is(err, validation.ErrInvalidInput) {
		log.Fatalf("expected ErrInvalidInput, got: %v", err)
	}

	// 4. ACCEPTED Complete: "approve" satisfies the completion-validation predicate.
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
