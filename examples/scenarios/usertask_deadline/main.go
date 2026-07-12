// Package main demonstrates an activity deadline via the
// activity.WithWaitDeadline + activity.WithDeadlineAction options — a
// UserTask that escalates when not completed in time.
//
// WithWaitDeadline sets the deadline timer and escape flow; WithDeadlineAction
// attaches an optional fire-once breach action. Together they are the
// ergonomic shorthand for the common "wait up to N, then do something else"
// pattern. For the boundary-native equivalent — an explicit event.NewBoundary
// node with event.WithBoundaryAction — see the sibling boundary_action
// example, which shows the same fire-once action semantics through the true
// boundary-event API.
//
// WithWaitDeadline(triggerSpec, flowID) attaches a TimerDeadline to the
// activity; WithDeadlineAction(action) attaches the breach action. When a
// token sits past the duration the engine, on breach, does two things:
//
//  1. Runs the breach action (a fire-once action.Action, invoked for its
//     side effect; its result is not fed back), and
//  2. Routes the token down the named deadline flow to an alternative path,
//     cancelling the in-progress human task.
//
// Flow:
//
//	start → review[UserTask, deadline "1h" → flow "review-overdue", action "notify-overdue"]
//	             │                                   │
//	             │ (reviewer approves)               │ (deadline breach)
//	             ↓                                   ↓
//	         approved-end                    escalate[Service "reassign"] → escalated-end
//
// The reviewer never claims the task. After the deadline elapses, the breach
// action "notify-overdue" runs (fire-once), the token is routed via the
// "review-overdue" flow to the escalate service task ("reassign"), the human
// task is marked Cancelled, and the instance completes via the escalation path.
//
// A *clockwork.FakeClock drives both the engine and the gocron-backed scheduler
// so the example is deterministic and runs instantly: advancing the fake clock
// fires the deadline timer without any real waiting. Because the gocron scheduler
// fires on its own executor goroutine, a done channel signalled from the breach
// path makes the observation deterministic: schedule → Advance → <-done → assert.
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/scheduling"
)

func main() {
	ctx := context.Background()

	// Build the process: a user task carrying a 1-hour deadline. On breach the
	// engine runs the "notify-overdue" breach action (fire-once) and routes the
	// token down the "review-overdue" flow to the escalate service task.
	//
	// Durations are expr-lang expressions parsed by time.ParseDuration, so they
	// are quoted Go-duration strings ("1h", "30m", "45s"). The outer backticks
	// keep the inner quotes literal. WithDeadlineAction attaches the fire-once
	// breach action; the escalation work proper is the service task on the
	// deadline path.
	def, err := definition.NewBuilder("review-escalation", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("review", activity.WithEligibleRoles("reviewer"),
			activity.WithWaitDeadline(schedule.AfterDuration(time.Hour), "review-overdue"),
			activity.WithDeadlineAction("notify-overdue"),
		)).
		Add(activity.NewServiceTask("escalate", activity.WithTaskAction("reassign"))).
		Add(event.NewEnd("approved-end")).
		Add(event.NewEnd("escalated-end")).
		Connect("start", "review").
		Connect("review", "approved-end"). // normal completion path
		// The deadline flow: its ID must match the WithWaitDeadline flowID above.
		Connect("review", "escalate", flow.WithFlowID("review-overdue")).
		Connect("escalate", "escalated-end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	escalated := false
	// Signalled from the escalation-path action so the main goroutine can wait for
	// the async timer fire deterministically.
	escalatedCh := make(chan struct{})
	cat := action.NewCatalog(map[string]action.Action{
		// Fire-once breach action: run by the engine the moment the deadline
		// elapses, for its side effect only (its result is not fed back).
		"notify-overdue": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			fmt.Println("  [notify-overdue] review deadline breached — notifying the manager")
			return nil, nil
		}),
		// Service action on the escalation path the token is routed to on breach.
		"reassign": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			escalated = true
			fmt.Println("  [reassign] reassigning the review to a senior reviewer")
			close(escalatedCh)
			return map[string]any{"escalated": true}, nil
		}),
	})

	// Fake clock shared by the engine and the scheduler (ADR-0003).
	startAt := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	clk := clockwork.NewFakeClockAt(startAt)

	// Human-task wiring is required for the UserTask to park correctly.
	reviewer := authz.Actor{ID: "alice", Roles: []string{"reviewer"}}
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"reviewer": {reviewer},
	})
	sched, err := scheduling.NewScheduler(scheduling.WithClock(clk))
	if err != nil {
		log.Fatal("scheduler:", err)
	}
	defer func() { _ = sched.Close() }()
	store, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}

	driver, err := runtime.NewProcessDriver(
		runtime.WithActionCatalog(cat),
		runtime.WithInstanceStore(store),
		runtime.WithClock(clk),
		runtime.WithHumanTasks(resolver, taskStore, authz.RoleAuthorizer{}),
		runtime.WithScheduler(sched),
	)
	if err != nil {
		log.Fatal("runner:", err)
	}

	const instanceID = "review-001"

	fmt.Println("--- Review Escalation: Activity Deadline (WithWaitDeadline + WithDeadlineAction) ---")

	// Run parks at the user task; the deadline timer is armed.
	parked, err := driver.Drive(ctx, def, instanceID, nil)
	if err != nil {
		log.Fatal("run:", err)
	}
	fmt.Printf("instance parked at %q (status=%s)\n",
		parked.Tokens[0].NodeID, parked.Status.String())

	// The reviewer never claims the task. Wait until the scheduler has armed its
	// deadline waiter on the fake clock, then advance past the 1h deadline — the
	// gocron executor goroutine fires the timer, delivering the deadline breach.
	if err := clk.BlockUntilContext(ctx, 1); err != nil {
		log.Fatal("block:", err)
	}
	clk.Advance(1*time.Hour + time.Minute)

	// The timer fires asynchronously; the escalation-path action closes escalatedCh
	// once it runs, giving a deterministic signal that the breach path executed.
	select {
	case <-escalatedCh:
	case <-time.After(3 * time.Second):
		log.Fatal("timeout: deadline breach did not fire")
	}

	// The final terminal commit may still be in-flight after the action ran; poll
	// briefly for completion.
	var final engine.InstanceState
	for range 200 {
		final, _, err = store.Load(ctx, instanceID)
		if err == nil && final.Status == engine.StatusCompleted {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		log.Fatal("load:", err)
	}
	if final.Status == engine.StatusCompleted && escalated {
		fmt.Println("instance completed via the escalation path:",
			final.Variables["escalated"])
	} else {
		fmt.Printf("unexpected outcome: status=%s escalated=%v\n",
			final.Status.String(), escalated)
	}
}
