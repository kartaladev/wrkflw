// Package main demonstrates a TIMER boundary event that combines routing with a
// fire-once boundary action via event.WithBoundaryAction.
//
// An interrupting timer boundary is attached to a UserTask. When the timer
// fires it does two things atomically:
//
//  1. Runs the fire-once "notify-overdue" catalog action (WithBoundaryAction)
//     for its side effect — the result is discarded; failure logs and continues.
//  2. Cancels the human task and routes the token down the escalation path.
//
// Contrast with the sibling usertask_deadline example, which demonstrates the
// activity.WithWaitDeadline + activity.WithDeadlineAction options — a timer +
// fire-once action + escape flow expressed as activity options rather than an
// explicit boundary node. WithBoundaryAction on a true event.NewBoundary node gives the same
// fire-once action semantics but as a first-class boundary-event feature,
// available on any trigger type (timer, message, signal, error).
//
// Flow:
//
//	start → review[UserTask] ──(reviewer approves)──────────→ end-approved
//	             └─◄ timer "1h" (interrupting) + notify-overdue
//	                           → escalate[Service] → end-escalated
//
// A *clockwork.FakeClock drives both the engine and the gocron-backed scheduler
// (ADR-0003) so the example is deterministic and runs instantly. Because the
// gocron scheduler fires on its own executor goroutine, a done channel
// signalled from the escalation-path action makes the observation deterministic:
// schedule → Advance → <-done → assert.
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
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/scheduling"
)

func main() {
	ctx := context.Background()

	// Build the process. A timer boundary is attached to the UserTask via
	// event.NewBoundary("bnd-overdue", "review"). WithBoundaryAction adds a
	// fire-once action name: the engine invokes it before routing when the
	// boundary fires. The single outgoing Connect from the boundary is the
	// escalation path taken on timeout.
	def, err := definition.NewBuilder("review-with-boundary-action", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("review", activity.WithEligibleRoles("reviewer"))).
		// Interrupting timer boundary with a fire-once notify action.
		Add(event.NewBoundary("bnd-overdue", "review",
			event.WithBoundaryTimer(schedule.AfterDuration(time.Hour)),
			event.WithBoundaryAction("notify-overdue"),
		)).
		Add(activity.NewServiceTask("escalate", activity.WithTaskAction("reassign"))).
		Add(event.NewEnd("end-approved")).
		Add(event.NewEnd("end-escalated")).
		Connect("start", "review").
		Connect("review", "end-approved").  // normal path (reviewer approves)
		Connect("bnd-overdue", "escalate"). // boundary fires → escalation path
		Connect("escalate", "end-escalated").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	notifyRan := false
	escalated := false
	// Signalled from the escalation-path action so the main goroutine can wait
	// for the async timer fire deterministically (gocron fires on its own goroutine).
	escalatedCh := make(chan struct{})

	cat := action.NewCatalog(map[string]action.Action{
		// Fire-once boundary action: runs when the timer boundary fires, before
		// the token is routed. Result discarded; non-fatal on failure.
		"notify-overdue": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			notifyRan = true
			fmt.Println("  [notify-overdue] review deadline breached — notifying manager (fire-once boundary action)")
			return nil, nil
		}),
		// Service action on the escalation path the token is routed to after the
		// boundary fires and the notify action has run.
		"reassign": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			escalated = true
			fmt.Println("  [reassign] reassigning the review to a senior reviewer")
			close(escalatedCh)
			return map[string]any{"escalated": true}, nil
		}),
	})

	// Fake clock shared by the engine and the gocron-backed scheduler (ADR-0003).
	startAt := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	clk := clockwork.NewFakeClockAt(startAt)

	// Human-task wiring: the UserTask parks correctly only when a TaskStore and
	// actor resolver are provided.
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
		log.Fatal("driver:", err)
	}

	const instanceID = "review-001"

	fmt.Println("--- Review Escalation: Boundary Action (WithBoundaryAction) ---")

	// Run parks at the user task; the timer boundary is armed.
	parked, err := driver.Drive(ctx, def, instanceID, nil)
	if err != nil {
		log.Fatal("run:", err)
	}
	fmt.Printf("instance parked at %q (status=%s, boundaries armed=%d)\n",
		parked.Tokens[0].NodeID, parked.Status.String(), len(parked.Boundaries))

	// The reviewer never claims the task. Wait until the scheduler has armed the
	// boundary timer on the fake clock, then advance past 1 hour — the gocron
	// executor goroutine fires the timer, triggering the boundary.
	if err := clk.BlockUntilContext(ctx, 1); err != nil {
		log.Fatal("block:", err)
	}
	clk.Advance(1*time.Hour + time.Minute)

	// The timer fires asynchronously; the escalation-path action closes escalatedCh
	// once it runs, giving a deterministic signal that the full breach path executed
	// (boundary action ran, then the escalation service task ran).
	select {
	case <-escalatedCh:
	case <-time.After(3 * time.Second):
		log.Fatal("timeout: timer boundary did not fire")
	}

	// The terminal commit may still be in-flight after the escalation action ran;
	// poll briefly for completion.
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

	fmt.Printf("instance: status=%s, notifyRan=%v, escalated=%v\n",
		final.Status.String(), notifyRan, escalated)

	if final.Status != engine.StatusCompleted {
		fmt.Println("FAIL: expected StatusCompleted")
		log.Fatal("unexpected status:", final.Status)
	}
	if !notifyRan {
		fmt.Println("FAIL: notify-overdue boundary action did not run")
		log.Fatal("boundary action not invoked")
	}
	if !escalated {
		fmt.Println("FAIL: escalation path did not execute")
		log.Fatal("escalation path not taken")
	}
	// The human task token must be gone (UserTask was Cancelled by the interrupting
	// boundary); final state should have no active tokens.
	if len(final.Tokens) != 0 {
		fmt.Printf("FAIL: expected 0 active tokens, got %d\n", len(final.Tokens))
		log.Fatal("unexpected active tokens")
	}

	fmt.Println("boundary_action scenario: all assertions passed")
	fmt.Println("  ✓ notify-overdue boundary action ran (fire-once, before routing)")
	fmt.Println("  ✓ human task was Cancelled by the interrupting timer boundary")
	fmt.Println("  ✓ instance completed via the escalation path")
}
