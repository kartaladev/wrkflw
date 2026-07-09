// Package main demonstrates ProcessDriver.ReverseInstance over a reject /
// re-escalate human-approval loop.
//
// Flow:
//
//	start → review[UserTask, completion:log-review, compensate:revert-review]
//	      → escalate[UserTask, completion:record-decision, compensate:revert-decision]
//	      → decision[ExclusiveGateway]
//	           approved == false  → back to escalate (reject / re-escalate)
//	           default            → end
//
// Both UserTasks carry a completion action (the forward side of the round
// trip) AND a compensate action (the undo side) — the Item-4 Build guard
// (ErrCompensateActionWithoutForwardAction) requires a UserTask's compensate
// action to be paired with a completion action, since the completion action
// IS the forward action for a UserTask.
//
// The instance is driven through one review and two rejected escalation
// rounds, then parked awaiting a third decision — never reaching "end". From
// that parked (Running) state the example shows the two ReverseInstance
// modes on two independent instances of the same definition:
//
//   - Scenario A — [runtime.WithTargetNode]("review"): compensates the two
//     recorded "escalate" rounds (newest-first) but excludes "review"'s own
//     record, then resumes Running AT "review" with variables RESTORED to
//     "review"'s own start-of-visit snapshot — the variables as they stood
//     the moment execution first arrived at "review" (just {"applicant": ...},
//     before "approved" was ever set), discarding every mutation made since,
//     including both rejection rounds' approved=false.
//   - Scenario B — [runtime.WithFullReverse]: compensates every recorded
//     round (review + both escalate rounds, newest-first), resets variables
//     back to what the instance started with, and resumes Running at the
//     definition's start node, which auto-drives forward to park at "review"
//     again with a clean slate.
//
// ReverseInstance never terminates the instance — it stays Running (or
// Compensating mid-walk) either way; that is why this example, unlike
// examples/scenarios/compensation_saga, never needs the instance to reach a
// terminal status before rolling it back.
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
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
)

func main() {
	ctx := context.Background()

	def, err := definition.NewBuilder("reject-reescalate-approval", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("review", activity.WithCandidateRoles("reviewer"),
			activity.WithCompletionAction("log-review"),
			activity.WithCompensateAction("revert-review"))).
		Add(activity.NewUserTask("escalate", activity.WithCandidateRoles("approver"),
			activity.WithCompletionAction("record-decision"),
			activity.WithCompensateAction("revert-decision"))).
		Add(gateway.NewExclusive("decision")).
		Add(event.NewEnd("end")).
		Connect("start", "review").
		Connect("review", "escalate").
		Connect("escalate", "decision").
		Connect("decision", "escalate", flow.WithCondition("approved == false")).
		Connect("decision", "end", flow.AsDefault()).
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	cat := action.NewCatalog(map[string]action.Action{
		"log-review": action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
			fmt.Printf("    [log-review] initial review logged (reviewed=%v)\n", in["reviewed"])
			return map[string]any{"logged": true}, nil
		}),
		"revert-review": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
			fmt.Println("    [revert-review] initial review log UNDONE")
			return nil, nil
		}),
		"record-decision": action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
			fmt.Printf("    [record-decision] decision recorded (approved=%v)\n", in["approved"])
			return map[string]any{"decision_recorded": true}, nil
		}),
		"revert-decision": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
			fmt.Println("    [revert-decision] decision record UNDONE")
			return nil, nil
		}),
	})

	// Human-task ports: "alice" reviews first, "bob" (the senior approver) is
	// the one who rejects and re-escalates.
	reviewer := authz.Actor{ID: "alice", Roles: []string{"reviewer"}}
	approver := authz.Actor{ID: "bob", Roles: []string{"approver"}}
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"reviewer": {reviewer},
		"approver": {approver},
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
		runtime.WithClock(clk),
	)
	if err != nil {
		log.Fatal("runner:", err)
	}
	svc, err := task.NewTaskService(taskStore, az, task.WithClock(clk))
	if err != nil {
		log.Fatal("task service:", err)
	}

	fmt.Println("--- Reject / Re-escalate Approval Loop: ReverseInstance ---")

	fmt.Println("\n=== Scenario A: WithTargetNode — partial rollback to a mid-loop node ===")
	parkedA := driveToThirdEscalation(ctx, driver, svc, def, "approval-001", reviewer, approver)
	fmt.Printf("  parked at %q awaiting a 3rd decision (status=%s, compensations=%d, approved=%v)\n",
		parkedA.Tokens[0].NodeID, view.StatusString(parkedA.Status), len(parkedA.RootCompensations), parkedA.Variables["approved"])

	reversedA, err := driver.ReverseInstance(ctx, def, "approval-001", runtime.WithTargetNode("review"))
	if err != nil {
		log.Fatal("reverse (target node):", err)
	}
	fmt.Printf("  after WithTargetNode(%q): status=%s, resumed at %q, records=%d (retained — a partial reverse keeps them for a later full walk), vars RESTORED to review's start-of-visit snapshot (approved=%v, applicant=%v)\n",
		"review", view.StatusString(reversedA.Status), reversedA.Tokens[0].NodeID,
		len(reversedA.RootCompensations), reversedA.Variables["approved"], reversedA.Variables["applicant"])

	fmt.Println("\n=== Scenario B: WithFullReverse — full rollback, reset to start ===")
	parkedB := driveToThirdEscalation(ctx, driver, svc, def, "approval-002", reviewer, approver)
	fmt.Printf("  parked at %q awaiting a 3rd decision (status=%s, compensations=%d, approved=%v)\n",
		parkedB.Tokens[0].NodeID, view.StatusString(parkedB.Status), len(parkedB.RootCompensations), parkedB.Variables["approved"])

	reversedB, err := driver.ReverseInstance(ctx, def, "approval-002", runtime.WithFullReverse())
	if err != nil {
		log.Fatal("reverse (full):", err)
	}
	fmt.Printf("  after WithFullReverse(): status=%s, resumed at %q, records=%d (cleared), vars RESET (approved=%v, applicant=%v)\n",
		view.StatusString(reversedB.Status), reversedB.Tokens[0].NodeID,
		len(reversedB.RootCompensations), reversedB.Variables["approved"], reversedB.Variables["applicant"])
}

// driveToThirdEscalation drives a fresh instance of def through one review
// completion and two rejected escalation rounds (re-escalating via the
// "approved == false" gateway edge each time), then stops WITHOUT completing
// the third escalation — leaving the instance parked (StatusRunning) at
// "escalate" with 3 recorded compensation entries (review + 2 escalations).
// ReverseInstance requires a Running (non-terminal) instance, so the example
// deliberately never lets the loop reach "end".
func driveToThirdEscalation(
	ctx context.Context,
	driver *runtime.ProcessDriver,
	svc *task.TaskService,
	def *model.ProcessDefinition,
	instanceID string,
	reviewer, approver authz.Actor,
) engine.InstanceState {
	fmt.Printf("\ndriving instance %q:\n", instanceID)

	parked, err := driver.Drive(ctx, def, instanceID, map[string]any{"applicant": "Jordan Lee"})
	if err != nil {
		log.Fatalf("drive %s: %v", instanceID, err)
	}
	fmt.Printf("  parked at %q\n", parked.Tokens[0].NodeID)

	parked = completeParkedTask(ctx, driver, svc, def, instanceID, parked, reviewer, map[string]any{"reviewed": true})
	fmt.Printf("  review complete -> parked at %q\n", parked.Tokens[0].NodeID)

	for round := 1; round <= 2; round++ {
		parked = completeParkedTask(ctx, driver, svc, def, instanceID, parked, approver, map[string]any{"approved": false})
		fmt.Printf("  escalation round %d rejected -> re-escalated, parked at %q (compensations=%d)\n",
			round, parked.Tokens[0].NodeID, len(parked.RootCompensations))
	}

	return parked
}

// completeParkedTask claims and completes the currently open task on parked
// (the mechanics from examples/scenarios/usertask_approval and
// examples/scenarios/completion_action), returning the resulting state. The
// completion action registered on the node runs synchronously inside the
// ApplyTrigger call that delivers the completion trigger.
//
// parked.Tasks accumulates every task record ever created for the instance
// (completed ones are never removed, only marked closed), so the open task —
// the one this round's actor must act on — is found by scanning for
// [humantask.HumanTask.IsOpen], not by assuming index 0.
func completeParkedTask(
	ctx context.Context,
	driver *runtime.ProcessDriver,
	svc *task.TaskService,
	def *model.ProcessDefinition,
	instanceID string,
	parked engine.InstanceState,
	actor authz.Actor,
	output map[string]any,
) engine.InstanceState {
	taskToken := ""
	for _, t := range parked.Tasks {
		if t.IsOpen() {
			taskToken = t.TaskToken
		}
	}
	if taskToken == "" {
		log.Fatalf("complete task %s: no open task on instance %q", actor.ID, instanceID)
	}

	claimTrg, err := svc.Claim(ctx, taskToken, actor)
	if err != nil {
		log.Fatal("claim:", err)
	}
	if _, err := driver.ApplyTrigger(ctx, def, instanceID, claimTrg); err != nil {
		log.Fatal("deliver claim:", err)
	}

	completeTrg, err := svc.Complete(ctx, taskToken, actor, output)
	if err != nil {
		log.Fatal("complete:", err)
	}
	next, err := driver.ApplyTrigger(ctx, def, instanceID, completeTrg)
	if err != nil {
		log.Fatal("deliver complete:", err)
	}
	return next
}
