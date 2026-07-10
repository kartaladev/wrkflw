// Package main demonstrates in-wait reminder actions: a recurring action that
// runs *during* a wait, not only when it expires.
//
// A human task that sits unclaimed should nudge the assignee periodically. The
// WithWaitAction(every, action) activity option schedules a recurring in-wait timer
// (TimerInWait): while the task is open, the engine fires the reminder action
// once per interval and re-schedules the next one. The token never moves; the
// reminders simply run as a side effect until the task is completed, cancelled,
// or breaches a deadline — at which point the recurring timer goes stale and stops.
//
// This differs from usertask_deadline, which shows a one-shot deadline (WithWaitDeadline)
// that escalates on breach. Here the action fires repeatedly and the task still
// completes normally.
//
// Flow:
//
//	start → review[UserTask, reminder every "30m" → action "nudge-reviewer"] → end
//
// A *clockwork.FakeClock drives both the engine and the gocron-backed scheduler,
// so advancing the clock fires each reminder deterministically. The reminder
// recurs natively in the scheduler (no per-fire reschedule); because it fires on
// its own executor goroutine, the nudge action signals a channel per fire so the
// main goroutine can observe each reminder deterministically: Advance → <-nudgeCh.
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
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
	"github.com/zakyalvan/krtlwrkflw/scheduling"
)

func main() {
	ctx := context.Background()

	// The reminder interval is an expr-lang duration string (parsed by
	// time.ParseDuration), so it is a quoted Go-duration literal. The reminder
	// action "nudge-reviewer" runs fire-and-forget once per interval.
	def, err := definition.NewBuilder("periodic-review", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("review", activity.WithEligibleRoles("reviewer"),
			activity.WithWaitAction(schedule.Every(30*time.Minute), "nudge-reviewer"),
		)).
		Add(event.NewEnd("end")).
		Connect("start", "review").
		Connect("review", "end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	nudges := 0
	// Signalled once per reminder fire so the main goroutine can wait for each
	// async reminder deterministically (the scheduler fires on its own goroutine).
	nudgeCh := make(chan struct{}, 8)
	cat := action.NewCatalog(map[string]action.Action{
		"nudge-reviewer": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			nudges++
			fmt.Printf("  [nudge-reviewer] reminder #%d — please review the pending item\n", nudges)
			select {
			case nudgeCh <- struct{}{}:
			default:
			}
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
		runtime.WithHumanTasks(resolver, taskStore, az),
		runtime.WithScheduler(sched),
	)
	if err != nil {
		log.Fatal("runner:", err)
	}

	const instanceID = "review-77"

	fmt.Println("--- Periodic Review: In-Wait Reminders ---")

	// Run parks at the user task; the first reminder timer is armed.
	parked, err := driver.Drive(ctx, def, instanceID, nil)
	if err != nil {
		log.Fatal("run:", err)
	}
	fmt.Printf("parked at %q (status=%s)\n",
		parked.Tokens[0].NodeID, view.StatusString(parked.Status))

	// The reviewer sits on the task for 90 minutes. Advance the clock in three
	// 30-minute steps. The recurring reminder timer re-arms itself natively in the
	// scheduler, so each step: wait for the waiter to be armed, advance past it, and
	// wait for that interval's reminder to fire.
	for range 3 {
		if err := clk.BlockUntilContext(ctx, 1); err != nil {
			log.Fatal("block:", err)
		}
		clk.Advance(30 * time.Minute)
		select {
		case <-nudgeCh:
		case <-time.After(3 * time.Second):
			log.Fatal("timeout: reminder did not fire")
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
	if _, err := driver.ApplyTrigger(ctx, def, instanceID, claimTrg); err != nil {
		log.Fatal("deliver claim:", err)
	}
	completeTrg, err := svc.Complete(ctx, taskToken, reviewer, map[string]any{"approved": true})
	if err != nil {
		log.Fatal("complete:", err)
	}
	final, err := driver.ApplyTrigger(ctx, def, instanceID, completeTrg)
	if err != nil {
		log.Fatal("deliver complete:", err)
	}

	// After completion the recurring reminder is cancelled: advancing the clock
	// further must fire nothing. The timer no longer has a waiter, so don't block on
	// one — just advance and confirm no reminder arrives within a short window.
	nudgesAtCompletion := nudges
	clk.Advance(30 * time.Minute)
	select {
	case <-nudgeCh:
		log.Fatal("unexpected reminder fired after completion")
	case <-time.After(200 * time.Millisecond):
	}

	if final.Status == engine.StatusCompleted && nudges == nudgesAtCompletion {
		fmt.Printf("instance completed after %d reminders; no further reminders once completed\n", nudges)
	} else {
		fmt.Printf("unexpected outcome: status=%s nudges=%d\n",
			view.StatusString(final.Status), nudges)
	}
}
