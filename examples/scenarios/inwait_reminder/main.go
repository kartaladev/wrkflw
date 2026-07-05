// Package main demonstrates in-wait reminder actions: a recurring action that
// runs *during* a wait, not only when it expires.
//
// A human task that sits unclaimed should nudge the assignee periodically. The
// WithReminder(every, action) activity option schedules a recurring in-wait timer
// (TimerInWait): while the task is open, the engine fires the reminder action
// once per interval and re-schedules the next one. The token never moves; the
// reminders simply run as a side effect until the task is completed, cancelled,
// or breaches a deadline — at which point the recurring timer goes stale and stops.
//
// This differs from boundary_timer, which shows a one-shot deadline (WithDeadline)
// that escalates on breach. Here the action fires repeatedly and the task still
// completes normally.
//
// Flow:
//
//	start → review[UserTask, reminder every "30m" → action "nudge-reviewer"] → end
//
// A *clockwork.FakeClock drives both the engine and the in-memory scheduler, so
// advancing the clock and ticking the scheduler fires each reminder deterministically.
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
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
)

func main() {
	ctx := context.Background()

	// The reminder interval is an expr-lang duration string (parsed by
	// time.ParseDuration), so it is a quoted Go-duration literal. The reminder
	// action "nudge-reviewer" runs fire-and-forget once per interval.
	def, err := definition.NewBuilder("periodic-review", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("review", []string{"reviewer"},
			activity.WithReminder(`"30m"`, "nudge-reviewer"),
		)).
		Add(event.NewEnd("end")).
		Connect("start", "review").
		Connect("review", "end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	nudges := 0
	cat := action.NewMapCatalog(map[string]action.Action{
		"nudge-reviewer": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			nudges++
			fmt.Printf("  [nudge-reviewer] reminder #%d — please review the pending item\n", nudges)
			return nil, nil
		}),
	})

	startAt := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	clk := clockwork.NewFakeClockAt(startAt)

	reviewer := authz.Actor{ID: "alice", Roles: []string{"reviewer"}}
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"reviewer": {reviewer},
	})
	az := authz.RoleAuthorizer{}
	sched := kernel.NewMemScheduler(kernel.WithMemSchedulerClock(clk))
	store, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}

	r, err := runtime.NewProcessDriver(cat, store,
		runtime.WithClock(clk),
		runtime.WithHumanTasks(resolver, taskStore, az),
		runtime.WithScheduler(sched),
	)
	if err != nil {
		log.Fatal("runner:", err)
	}

	const instanceID = "review-77"

	fmt.Println("--- Periodic Review: In-Wait Reminders ---")

	// Run parks at the user task; the first reminder timer is armed.
	parked, err := r.Run(ctx, def, instanceID, nil)
	if err != nil {
		log.Fatal("run:", err)
	}
	fmt.Printf("parked at %q (status=%s)\n",
		parked.Tokens[0].NodeID, view.StatusString(parked.Status))

	// The reviewer sits on the task for 90 minutes. Advance the clock in three
	// 30-minute steps, ticking the scheduler each time to fire that interval's
	// reminder. Each tick fires one reminder and re-arms the next.
	for range 3 {
		clk.Advance(30 * time.Minute)
		if err := sched.Tick(ctx); err != nil {
			log.Fatal("tick:", err)
		}
	}
	fmt.Printf("reminders fired while waiting: %d\n", nudges)

	// The reviewer finally claims and completes the task.
	claimable, err := taskStore.ClaimableBy(ctx, reviewer)
	if err != nil {
		log.Fatal("claimable:", err)
	}
	if len(claimable) == 0 {
		log.Fatal("expected a claimable task")
	}
	taskToken := claimable[0].TaskToken

	svc, err := task.NewTaskService(taskStore, az, task.WithClock(clk))
	if err != nil {
		log.Fatal("task service:", err)
	}
	claimTrg, err := svc.Claim(ctx, taskToken, reviewer)
	if err != nil {
		log.Fatal("claim:", err)
	}
	if _, err := r.Deliver(ctx, def, instanceID, claimTrg); err != nil {
		log.Fatal("deliver claim:", err)
	}
	completeTrg, err := svc.Complete(ctx, taskToken, reviewer, map[string]any{"approved": true})
	if err != nil {
		log.Fatal("complete:", err)
	}
	final, err := r.Deliver(ctx, def, instanceID, completeTrg)
	if err != nil {
		log.Fatal("deliver complete:", err)
	}

	// After completion the recurring reminder is stale: further ticks fire nothing.
	nudgesAtCompletion := nudges
	clk.Advance(30 * time.Minute)
	if err := sched.Tick(ctx); err != nil {
		log.Fatal("tick:", err)
	}

	if final.Status == engine.StatusCompleted && nudges == nudgesAtCompletion {
		fmt.Printf("instance completed after %d reminders; no further reminders once completed\n", nudges)
	} else {
		fmt.Printf("unexpected outcome: status=%s nudges=%d\n",
			view.StatusString(final.Status), nudges)
	}
}
