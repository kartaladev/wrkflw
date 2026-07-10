// Package main demonstrates the unified force-termination End event (ADR-0119):
// an EndEvent built with event.WithForceTermination ends the WHOLE instance the
// moment a token reaches it, sweeping every other in-flight branch — not just
// the branch that reached the end.
//
// Flow (shared by both runs below):
//
//	start → fork[ParallelGateway] ──┬─→ reviewApplication[UserTask] → reviewComplete[End]
//	                                 └─→ checkFraud[ServiceTask] → halt[End, WithForceTermination]
//
// The parallel fork places two tokens. Branch A (reviewApplication) is wired
// FIRST, so it is driven first: it parks (creates an open human task) instead
// of reaching reviewComplete, because no one completes it in this demo.
// Branch B (checkFraud → halt) is driven next; when its token reaches halt,
// the force-termination sweep cancels ALL remaining parallel work — including
// branch A's already-open task — in the SAME Drive call. No second trigger is
// needed to reconcile the sibling.
//
// The two runs below share this shape but differ in the End event's outcome:
//
//   - abortRun uses event.OutcomeAbort: a fraud signal aborts the whole loan
//     review. The instance ends at engine.StatusTerminated.
//   - completeRun uses event.OutcomeComplete: the same shape, but the business
//     meaning is a SUCCESSFUL early halt (e.g. fraud check cleared and the
//     review may fast-track close without waiting on the parked reviewer).
//     The instance ends at engine.StatusCompleted instead.
//
// Both outcomes cancel the sibling branch identically — only the terminal
// status and the emitted instance-completion command (FailInstance vs
// CompleteInstance) differ. See engine/end_force_termination_test.go for the
// engine-level behaviour test this example mirrors.
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
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
)

func main() {
	ctx := context.Background()

	abortRun(ctx)
	fmt.Println()
	completeRun(ctx)
}

// abortRun drives the fraud-abort path: checkFraud reaches halt with
// OutcomeAbort, terminating the instance and cancelling the parked
// reviewApplication task.
func abortRun(ctx context.Context) {
	def, err := buildDef("loan-review-abort", event.WithForceTermination("fraud detected", event.OutcomeAbort))
	if err != nil {
		log.Fatal("build def:", err)
	}

	driver := newDriver()

	fmt.Println("--- Loan Review: Force-Termination Abort (ADR-0119) ---")
	const instanceID = "loan-review-abort-001"
	final, err := driver.Drive(ctx, def, instanceID, nil)
	if err != nil {
		log.Fatal("drive:", err)
	}

	fmt.Printf("instance status: %s\n", view.StatusString(final.Status))
	if final.Status != engine.StatusTerminated {
		log.Fatalf("expected StatusTerminated, got %s", view.StatusString(final.Status))
	}

	reportSweep(final)
}

// completeRun mirrors abortRun's exact shape but with OutcomeComplete: the same
// force-termination end represents a SUCCESSFUL early halt rather than an
// abort, so the instance ends at StatusCompleted instead of StatusTerminated.
// The sibling reviewApplication task is swept identically either way.
func completeRun(ctx context.Context) {
	def, err := buildDef("loan-review-complete", event.WithForceTermination("fraud check cleared — fast-track close", event.OutcomeComplete))
	if err != nil {
		log.Fatal("build def:", err)
	}

	driver := newDriver()

	fmt.Println("--- Loan Review: Force-Termination Complete (successful early halt) ---")
	const instanceID = "loan-review-complete-001"
	final, err := driver.Drive(ctx, def, instanceID, nil)
	if err != nil {
		log.Fatal("drive:", err)
	}

	fmt.Printf("instance status: %s\n", view.StatusString(final.Status))
	if final.Status != engine.StatusCompleted {
		log.Fatalf("expected StatusCompleted, got %s", view.StatusString(final.Status))
	}

	reportSweep(final)
}

// reportSweep prints and sanity-checks that the sibling reviewApplication task
// was reconciled to Cancelled and that no live tokens remain — proving the
// force-termination sweep, not just the terminal status, actually happened.
func reportSweep(final engine.InstanceState) {
	if len(final.Tasks) != 1 {
		log.Fatalf("expected exactly one recorded task, got %d", len(final.Tasks))
	}
	tsk := final.Tasks[0]
	fmt.Printf("sibling task %q (node=%s) reconciled to state=%s\n", tsk.TaskToken, tsk.NodeID, tsk.State)
	if tsk.State != humantask.Cancelled {
		log.Fatalf("expected sibling task Cancelled, got %s", tsk.State)
	}
	if len(final.Tokens) != 0 {
		log.Fatalf("expected all tokens dropped on force-termination, got %d", len(final.Tokens))
	}
	fmt.Println("no live tokens remain — parallel work fully swept")
}

// buildDef builds the shared fork shape, parameterized only by the halt end's
// force-termination option so both runs share one definition of record.
//
// reviewApplication is wired to its own reviewComplete end so the definition
// validates (every non-end node needs an outgoing flow) — but in both demo
// runs the token never gets there: branchB reaches halt first, in the same
// Drive call, and force-termination sweeps reviewApplication's still-open
// task before a human ever completes it.
func buildDef(id string, haltOpt event.EndOption) (*model.ProcessDefinition, error) {
	return definition.NewBuilder(id, 1).
		Add(event.NewStart("start")).
		Add(gateway.NewParallel("fork")).
		Add(activity.NewUserTask("reviewApplication", activity.WithEligibleRoles("reviewer"))).
		Add(event.NewEnd("reviewComplete")).
		Add(activity.NewServiceTask("checkFraud", activity.WithTaskAction("check-fraud"))).
		Add(event.NewEnd("halt", haltOpt)).
		Connect("start", "fork").
		// branchA (reviewApplication) wired FIRST: its token is driven and
		// parked before branchB's force-termination sweeps it.
		Connect("fork", "reviewApplication").
		Connect("reviewApplication", "reviewComplete").
		Connect("fork", "checkFraud").
		Connect("checkFraud", "halt").
		Build()
}

// newDriver wires a fresh in-memory ProcessDriver (own instance/task stores per
// run, so the two runs below don't share state) with the check-fraud service
// action and human-task support so reviewApplication actually parks.
func newDriver() *runtime.ProcessDriver {
	cat := action.NewCatalog(map[string]action.Action{
		"check-fraud": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			fmt.Println("  [checkFraud] signal detected — escalating to force-termination")
			return nil, nil
		}),
	})

	taskStore := humantask.NewMemTaskStore()
	memSt, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}
	driver, err := runtime.NewProcessDriver(
		runtime.WithActionCatalog(cat),
		runtime.WithInstanceStore(memSt),
		runtime.WithHumanTasks(humantask.NewStaticActorResolver(nil), taskStore, authz.RoleAuthorizer{}),
	)
	if err != nil {
		log.Fatal("driver:", err)
	}
	return driver
}
