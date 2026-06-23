// Package main demonstrates an interrupting boundary timer that escalates a
// human task that is not completed in time.
//
// Flow:
//
//	start → review[UserTask] ───────────────→ approved-end
//	             │
//	             │ (boundary timer 1h, interrupting)
//	             ↓
//	         escalate[Service] → escalated-end
//
// The reviewer never claims the task. After the boundary timer fires, the host
// user task is interrupted, the escalation service runs, and the instance
// completes via the escalation path.
//
// A *clockwork.FakeClock drives both the engine and the in-memory scheduler so
// the example is deterministic and runs instantly: advancing the fake clock and
// ticking the scheduler fires the timer without any real waiting.
//
// NOTE: timer/signal/error boundary events are armed by the engine. Message
// boundary events are a known limitation and are not yet armed.
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

	// Build the process: a user task with an interrupting boundary timer.
	// Timer durations are evaluated by expr-lang, so they are quoted strings.
	def, err := model.NewDefinition("review-escalation", 1).
		Add(model.NewStartEvent("start")).
		Add(model.NewUserTask("review", []string{"reviewer"})).
		Add(model.NewBoundaryEvent("review-timeout", "review",
			// Durations are expr-lang expressions parsed by time.ParseDuration,
			// so they are quoted Go-duration strings ("1h", "30m", "45s"), not
			// ISO-8601. The outer backticks keep the inner quotes literal.
			model.WithBoundaryTimer(`"1h"`), // interrupting by default
		)).
		Add(model.NewServiceTask("escalate", "escalate")).
		Add(model.NewEndEvent("approved-end")).
		Add(model.NewEndEvent("escalated-end")).
		Connect("start", "review").
		Connect("review", "approved-end").
		Connect("review-timeout", "escalate").
		Connect("escalate", "escalated-end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	escalated := false
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"escalate": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			escalated = true
			fmt.Println("  [escalate] review timed out — escalating to a manager")
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

	fmt.Println("--- Review Escalation: Boundary Timer ---")

	// Run parks at the user task; the boundary timer is armed.
	parked, err := r.Run(ctx, def, instanceID, nil)
	if err != nil {
		log.Fatal("run:", err)
	}
	fmt.Printf("instance parked at %q (status=%s)\n",
		parked.Tokens[0].NodeID, runtime.StatusString(parked.Status))

	// The reviewer never claims the task. Advance the clock past the 1h timer
	// and tick the scheduler — this fires the boundary timer.
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
