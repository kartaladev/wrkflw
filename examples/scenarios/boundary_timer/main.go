// Package main demonstrates an activity deadline (the WithDeadline option,
// formerly WithSLA) that escalates a human task not completed in time.
//
// A deadline is attached directly to an activity via WithDeadline(duration,
// flowID, action). When a token sits in the activity past the duration, the
// engine arms a TimerDeadline that, on breach, routes the token down the named
// deadline flow to an alternative path and cancels the in-progress human task.
// (WithDeadline can also run a fire-once breach action — the third argument;
// here it is left empty and the escalation work is done by the service task on
// the alternative path instead.)
//
// Flow:
//
//	start → review[UserTask, deadline "1h" → flow "review-overdue"]
//	             │                                   │
//	             │ (reviewer approves)               │ (deadline breach)
//	             ↓                                   ↓
//	         approved-end                    escalate[Service "reassign"] → escalated-end
//
// The reviewer never claims the task. After the deadline elapses, the token is
// routed via the "review-overdue" flow to the escalate service task ("reassign"),
// the human task is marked Cancelled, and the instance completes via the
// escalation path.
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
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func main() {
	ctx := context.Background()

	// Build the process: a user task carrying a 1-hour deadline. On breach the
	// engine routes the token down the "review-overdue" flow to the escalate
	// service task.
	//
	// Durations are expr-lang expressions parsed by time.ParseDuration, so they
	// are quoted Go-duration strings ("1h", "30m", "45s"). The outer backticks
	// keep the inner quotes literal. The empty third argument means no fire-once
	// breach action; escalation is handled by the service task on the deadline path.
	def, err := model.NewDefinition("review-escalation", 1).
		Add(model.NewStartEvent("start")).
		Add(model.NewUserTask("review", []string{"reviewer"},
			model.WithDeadline(`"1h"`, "review-overdue", ""),
		)).
		Add(model.NewServiceTask("escalate", model.WithActionName("reassign"))).
		Add(model.NewEndEvent("approved-end")).
		Add(model.NewEndEvent("escalated-end")).
		Connect("start", "review").
		Connect("review", "approved-end"). // normal completion path
		// The deadline flow: its ID must match the WithDeadline flowID above.
		Connect("review", "escalate", model.WithFlowID("review-overdue")).
		Connect("escalate", "escalated-end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	escalated := false
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		// Service action on the escalation path the token is routed to on breach.
		"reassign": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			escalated = true
			fmt.Println("  [reassign] review deadline breached — reassigning to a senior reviewer")
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
	sched := runtime.NewMemScheduler(clk)
	store := runtime.NewMemStore()

	r := runtime.NewRunner(cat, clk, store,
		runtime.WithHumanTasks(resolver, taskStore, authz.RoleAuthorizer{}),
		runtime.WithScheduler(sched),
	)

	const instanceID = "review-001"

	fmt.Println("--- Review Escalation: Activity Deadline (WithDeadline) ---")

	// Run parks at the user task; the deadline timer is armed.
	parked, err := r.Run(ctx, def, instanceID, nil)
	if err != nil {
		log.Fatal("run:", err)
	}
	fmt.Printf("instance parked at %q (status=%s)\n",
		parked.Tokens[0].NodeID, runtime.StatusString(parked.Status))

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
			runtime.StatusString(final.Status), escalated)
	}
}
