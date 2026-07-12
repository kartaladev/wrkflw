// Package main demonstrates the shipped input-validation design: validation
// is declared on definition nodes, and the runtime validates external input
// BEFORE Step, not inside the engine core and not inside the human-task
// service. See docs/specs/2026-07-09-input-validation-redesign.md and
// ADR-0110 (declarative architecture) / ADR-0115 (engine-decides /
// runtime-executes redesign superseding the earlier boundary-injection
// design).
//
// Flow:
//
//	start[validated: amount > 0] → approve[UserTask, roles: manager] → end
//
// "Engine decides, runtime executes": the engine exposes a pure,
// validation-agnostic scope-aware resolver, [engine.TargetNode], that answers
// "which node does this trigger target" for the three external-input
// triggers (StartInstance, MessageReceived, HumanCompleted). The
// ProcessDriver composes that query with the node's declared
// [validate.ValidationStrategy] and an impure [validation.Gate], running the
// check BEFORE Step commits any state:
//
//   - Start vars are validated in [runtime.ProcessDriver.Drive], before any
//     instance is created — see the expr-lang strategy attached to the start
//     event via [event.WithInputValidation].
//   - Completion output is validated in [runtime.ProcessDriver.ApplyTrigger],
//     not in [humantask.TaskService.Complete]. TaskService.Complete now does
//     authorization ONLY and always returns a HumanCompleted trigger; it is
//     ApplyTrigger's pre-Step hook that resolves the parked task's node (in
//     its scope) and rejects a bad completion output before it is ever
//     committed — see the expr-lang strategy attached to the UserTask via
//     [activity.WithCompletionValidation]. On rejection the task stays open:
//     nothing was committed, so the same claimed task token can be retried.
//
// The message boundary (DeliverMessage) follows the same ApplyTrigger
// pre-Step hook and is exercised in
// runtime/processdriver_message_validation_test.go; it is not repeated here.
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

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/clock"
	"github.com/kartaladev/wrkflw/definition"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	vexpr "github.com/kartaladev/wrkflw/definition/model/validate/expr"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/runtime/task"
	"github.com/kartaladev/wrkflw/runtime/validation"
	"github.com/kartaladev/wrkflw/runtime/view"
)

func main() {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Build the definition: the start event carries an expr-lang validation strategy.
	def, err := definition.NewBuilder("expense-approval-validated", 1).
		Add(event.NewStart("start", event.WithInputValidation(vexpr.New("amount > 0")))).
		Add(activity.NewUserTask("approve", activity.WithEligibleRoles("manager"),
			activity.WithCompletionValidation(vexpr.New(`decision in ['approve','reject']`)))).
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

	fmt.Println("--- Input Validation: completion output ---")

	// 3. REJECTED completion: decision="maybe" is not in the UserTask's
	//    CompletionValidation set. svc.Complete does AUTHZ ONLY and always
	//    returns a HumanCompleted trigger.
	//    The rejection surfaces at driver.ApplyTrigger instead: that is the
	//    engine-decides design — ApplyTrigger's pre-Step hook resolves the
	//    trigger's target node via the pure engine.TargetNode query, then
	//    runs the node's declared strategy through the Gate BEFORE Step ever
	//    commits. Nothing is committed on rejection, so the task stays open
	//    and the same taskToken can be completed again below.
	badCompleteTrg, err := svc.Complete(ctx, taskToken, manager, map[string]any{"decision": "maybe"})
	if err != nil {
		log.Fatal("complete (bad output) unexpectedly rejected by authz:", err)
	}
	_, err = driver.ApplyTrigger(ctx, def, instanceID, badCompleteTrg)
	logOutcome(logger, `complete output (decision="maybe") at ApplyTrigger`, err)
	if !errors.Is(err, validation.ErrInvalidInput) {
		log.Fatalf("expected ErrInvalidInput, got: %v", err)
	}

	// 4. ACCEPTED completion: decision="approve" passes validation at
	//    ApplyTrigger; the instance completes. Reuse the same taskToken —
	//    the rejected attempt above never committed, so the task is still
	//    open/claimed by manager.
	completeTrg, err := svc.Complete(ctx, taskToken, manager, map[string]any{"decision": "approve"})
	if err != nil {
		log.Fatal("complete (valid output):", err)
	}
	final, err := driver.ApplyTrigger(ctx, def, instanceID, completeTrg)
	logOutcome(logger, `complete output (decision="approve") at ApplyTrigger`, err)
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
