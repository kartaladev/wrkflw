// Package main demonstrates an intermediate catch event carrying an in-wait
// reminder: a recurring action that fires once per interval WHILE the instance
// is parked awaiting the catch, and stops automatically the moment the catch
// resolves.
//
// This is the executable proof that in-wait reminders — previously wired only
// for UserTask — now arm, fire, and cancel for IntermediateCatchEvent too.
//
// Flow:
//
//	start → await[catch message "approved" key=request, reminder every "30m" → "nudge"] → end
//
// The instance parks at the catch awaiting the "approved" message for ITS OWN
// request, correlated on the `request` variable. Approval is a per-request fact:
// "request approval-001 was approved" must resume only that instance. A broadcast
// signal would wake every pending approval parked on the name; a correlated message
// targets exactly this instance (use a signal only for genuine fan-out). A recurring
// in-wait reminder ("nudge") is armed on the scheduler. Advancing the fake clock
// across three 30-minute intervals fires three nudges, one per interval, each
// re-arming the next. Delivering "approved" resumes the instance to completion —
// which CANCELS the reminder. A final clock advance fires nothing: the nudge
// counter stays at three, proving the reminder was cancelled on resume.
//
// A *clockwork.FakeClock drives both the engine and the gocron-backed scheduler
// (ADR-0003), so the recurring timer is deterministic: advancing the clock fires
// the reminder without real waiting. Because the scheduler fires on its own
// executor goroutine, each nudge is observed via a buffered channel guarded by a
// timeout, and clk.BlockUntilContext waits for the timer to be re-armed before
// each advance so we never race the executor.
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
	"github.com/kartaladev/wrkflw/definition"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/scheduling"
)

func main() {
	ctx := context.Background()

	// Build the process: a single intermediate catch event awaiting the
	// "approved" message correlated on the `request` variable, carrying a recurring
	// in-wait reminder that fires the "nudge" action every 30 minutes while the
	// instance sits at the catch.
	def, err := definition.NewBuilder("approval-await", 1).
		Add(event.NewStart("start")).
		Add(event.NewIntermediateCatch("await",
			event.WithMessageCorrelator("approved", "request"),
			event.WithWaitAction(schedule.Every(30*time.Minute), "nudge"),
		)).
		Add(event.NewEnd("end")).
		Connect("start", "await").
		Connect("await", "end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	// nudges counts reminder fires; nudgeCh (buffered) makes each async fire
	// observable from the main goroutine without blocking the executor.
	nudges := 0
	nudgeCh := make(chan struct{}, 8)
	cat := action.NewCatalog(map[string]action.Action{
		"nudge": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			nudges++
			fmt.Printf("  [nudge] still awaiting approval — reminder #%d\n", nudges)
			nudgeCh <- struct{}{}
			return nil, nil
		}),
	})

	// One fake clock drives the engine and the scheduler (ADR-0003).
	startAt := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	clk := clockwork.NewFakeClockAt(startAt)

	sched, err := scheduling.NewScheduler(scheduling.WithClock(clk))
	if err != nil {
		log.Fatal("scheduler:", err)
	}
	defer func() { _ = sched.Close() }()

	store, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}

	// The correlated message arm is delivered via driver.DeliverMessage — no
	// SignalBus is needed (that is for broadcast signals). A standalone message catch
	// parks a token carrying AwaitMessage, which the runtime registers as a message
	// waiter, so a delivered message with the matching name+key resumes this instance.
	// The driver resolves a correlated instance's definition from its own
	// snapshot via the registry, so the definition must be registered (ADR-0121).
	reg := kernel.NewMemDefinitionRegistry()
	if err := reg.Register(def); err != nil {
		log.Fatal("register:", err)
	}
	driver, err := runtime.NewProcessDriver(
		runtime.WithActionCatalog(cat),
		runtime.WithInstanceStore(store),
		runtime.WithClock(clk),
		runtime.WithScheduler(sched),
		runtime.WithDefinitions(reg),
	)
	if err != nil {
		log.Fatal("driver:", err)
	}

	const instanceID = "approval-001"

	fmt.Println("--- Approval Await: Catch-Event In-Wait Reminder ---")

	// Drive parks at the catch; the recurring in-wait reminder is armed. The catch
	// awaits the "approved" message correlated on `request`, so the instance is
	// started with request == its own id.
	parked, err := driver.Drive(ctx, def, instanceID, map[string]any{"request": instanceID})
	if err != nil {
		log.Fatal("drive:", err)
	}
	fmt.Printf("instance parked at %q awaiting message %q (status=%s)\n",
		parked.Tokens[0].NodeID, parked.Tokens[0].AwaitMessage, parked.Status.String())

	// Advance across three 30-minute intervals. Each interval fires one nudge,
	// re-arming the next. BlockUntilContext waits for the reminder to be armed on
	// the fake clock before advancing, so we never outrun the gocron executor.
	for i := 1; i <= 3; i++ {
		if err := clk.BlockUntilContext(ctx, 1); err != nil {
			log.Fatal("block:", err)
		}
		clk.Advance(30 * time.Minute)
		select {
		case <-nudgeCh:
		case <-time.After(3 * time.Second):
			log.Fatalf("timeout: nudge #%d did not fire while parked", i)
		}
	}
	fmt.Printf("%d reminders fired while parked\n", nudges)

	// The approval arrives for THIS request. Delivering the correlated "approved"
	// message resumes the instance to completion, which cancels the recurring
	// reminder. The correlation key targets exactly this instance.
	fmt.Println("delivering message \"approved\" — resuming the instance")
	if err := driver.DeliverMessage(ctx, "approved", instanceID, map[string]any{"by": "manager"}); err != nil {
		log.Fatal("deliver approved:", err)
	}

	// Poll briefly for completion (the terminal commit can lag the resume).
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
	if final.Status != engine.StatusCompleted {
		log.Fatalf("instance did not complete: status=%s", final.Status.String())
	}
	fmt.Printf("instance completed (status=%s)\n", final.Status.String())

	// Prove the reminder was cancelled on resume: advance one more interval and
	// confirm NO further nudge fires. The counter must stay at three.
	before := nudges
	clk.Advance(30 * time.Minute)
	select {
	case <-nudgeCh:
		log.Fatalf("reminder fired AFTER resume: nudge count went %d → %d (cancel failed)", before, nudges)
	case <-time.After(500 * time.Millisecond):
		// Expected: no nudge — the recurring reminder was cancelled on resume.
	}

	if nudges == 3 {
		fmt.Printf("OK: 3 nudges while parked, 0 after resume — catch-event reminder armed, fired, and cancelled\n")
	} else {
		fmt.Printf("unexpected outcome: nudges=%d (want 3)\n", nudges)
	}
}
