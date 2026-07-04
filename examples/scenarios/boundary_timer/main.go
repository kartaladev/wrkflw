// Package main demonstrates an activity deadline (the WithDeadline option,
// formerly WithSLA) that escalates a human task not completed in time.
//
// A deadline is attached directly to an activity via WithDeadline(duration,
// flowID, action). When a token sits in the activity past the duration, the
// engine arms a TimerDeadline that, on breach, does two things:
//
//  1. runs the breach action (a fire-once ServiceAction — the third argument —
//     run for its side effect; its result is not fed back), and
//  2. routes the token down the named deadline flow to an alternative path,
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
// A *clockwork.FakeClock drives both the engine and the in-memory scheduler so
// the example is deterministic and runs instantly: advancing the fake clock and
// ticking the scheduler fires the deadline timer without any real waiting.
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
)

func main() {
	ctx := context.Background()

	// Build the process: a user task carrying a 1-hour deadline. On breach the
	// engine runs the "notify-overdue" breach action (fire-once) and routes the
	// token down the "review-overdue" flow to the escalate service task.
	//
	// Durations are expr-lang expressions parsed by time.ParseDuration, so they
	// are quoted Go-duration strings ("1h", "30m", "45s"). The outer backticks
	// keep the inner quotes literal. The third argument is the fire-once breach
	// action; the escalation work proper is the service task on the deadline path.
	def, err := definition.NewDefinition("review-escalation", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("review", []string{"reviewer"},
			activity.WithDeadline(`"1h"`, "review-overdue", "notify-overdue"),
		)).
		Add(activity.NewServiceTask("escalate", activity.WithActionName("reassign"))).
		Add(event.NewEnd("approved-end")).
		Add(event.NewEnd("escalated-end")).
		Connect("start", "review").
		Connect("review", "approved-end"). // normal completion path
		// The deadline flow: its ID must match the WithDeadline flowID above.
		Connect("review", "escalate", definition.WithFlowID("review-overdue")).
		Connect("escalate", "escalated-end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	escalated := false
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		// Fire-once breach action: run by the engine the moment the deadline
		// elapses, for its side effect only (its result is not fed back).
		"notify-overdue": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			fmt.Println("  [notify-overdue] review deadline breached — notifying the manager")
			return nil, nil
		}),
		// Service action on the escalation path the token is routed to on breach.
		"reassign": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			escalated = true
			fmt.Println("  [reassign] reassigning the review to a senior reviewer")
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
	sched := kernel.NewMemScheduler(kernel.WithMemSchedulerClock(clk))
	store, err := kernel.NewMemStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}

	r, err := runtime.NewProcessDriver(cat, store,
		runtime.WithClock(clk),
		runtime.WithHumanTasks(resolver, taskStore, authz.RoleAuthorizer{}),
		runtime.WithScheduler(sched),
	)
	if err != nil {
		log.Fatal("runner:", err)
	}

	const instanceID = "review-001"

	fmt.Println("--- Review Escalation: Activity Deadline (WithDeadline) ---")

	// Run parks at the user task; the deadline timer is armed.
	parked, err := r.Run(ctx, def, instanceID, nil)
	if err != nil {
		log.Fatal("run:", err)
	}
	fmt.Printf("instance parked at %q (status=%s)\n",
		parked.Tokens[0].NodeID, view.StatusString(parked.Status))

	// The reviewer never claims the task. Advance the clock past the 1h deadline
	// and tick the scheduler — this fires the deadline timer.
	clk.Advance(1*time.Hour + time.Minute)
	if err := sched.Tick(ctx); err != nil {
		log.Fatal("tick:", err)
	}

	final, _, err := store.Load(ctx, instanceID)
	if err != nil {
		log.Fatal("load:", err)
	}
	if final.Status == engine.StatusCompleted && escalated {
		fmt.Println("instance completed via the escalation path:",
			final.Variables["escalated"])
	} else {
		fmt.Printf("unexpected outcome: status=%s escalated=%v\n",
			view.StatusString(final.Status), escalated)
	}
}
