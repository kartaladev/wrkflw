// Package main demonstrates a TIMER boundary event: an interrupting timer
// attached to a waiting activity that fires if the activity does not complete in
// time — a per-activity SLA/timeout.
//
// NOTE: this is distinct from the sibling boundary_timer example, which
// demonstrates activity.WithDeadline (a different feature). This one uses the
// true boundary-event API, event.NewBoundary + event.WithBoundaryTimer.
//
// An order-settlement process parks at a ReceiveTask awaiting a
// "payment.confirmed" message. A 30-minute interrupting timer boundary is armed
// on that task. If the confirmation arrives first, the task resumes normally and
// the timer is disarmed; if the timer fires first, it interrupts the wait and
// routes to an escalation path.
//
// Flow:
//
//	start → await-payment[ReceiveTask] ──(payment.confirmed)──────→ end-settled
//	              └─◄ timer "30m" (interrupting) → escalate[Service] → end-escalated
//
// The example runs TWO instances to contrast both outcomes:
//
//   - "order-ontime": the confirmation message is delivered before the deadline
//     → settles normally; the timer boundary is disarmed (never fires).
//   - "order-late": no message arrives; advancing the fake clock past 30m fires
//     the timer boundary → escalation path.
//
// A *clockwork.FakeClock drives both the engine and the gocron-backed scheduler
// (ADR-0003) so the example is deterministic and runs instantly. Because the
// gocron scheduler fires on its own executor goroutine, a done channel signalled
// from the escalation path makes the observation deterministic:
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
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/scheduling"
)

func main() {
	ctx := context.Background()

	// Build the process. The timer boundary is attached to the ReceiveTask; its
	// single outgoing flow (Connect) is the escalation path taken on timeout.
	// The duration is an expr-lang expression parsed by time.ParseDuration, so it
	// is a quoted Go-duration string — the outer backticks keep the inner quotes
	// literal ("30m"). The ReceiveTask correlates by the instance's orderID
	// variable so each parked instance is addressable by its own id.
	def, err := definition.NewBuilder("order-settlement", 1).
		Add(event.NewStart("start")).
		Add(activity.NewReceiveTask("await-payment", "payment.confirmed",
			activity.WithCorrelationKey("orderID"))).
		Add(event.NewBoundary("bnd-timeout", "await-payment",
			event.WithBoundaryTimer(schedule.AfterDuration(30*time.Minute)))).
		Add(activity.NewServiceTask("escalate", activity.WithActionName("escalate-payment"))).
		Add(event.NewEnd("end-settled")).
		Add(event.NewEnd("end-escalated")).
		Connect("start", "await-payment").
		Connect("await-payment", "end-settled"). // message-arrived path
		Connect("bnd-timeout", "escalate").      // timer boundary flow
		Connect("escalate", "end-escalated").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	escalated := map[string]bool{}
	// Signalled from the escalation-path action so the main goroutine can wait for
	// the async timer fire deterministically. Only order-late escalates, so a
	// single close is correct.
	escalatedCh := make(chan struct{})
	cat := action.NewMapCatalog(map[string]action.Action{
		// Runs on the timer-boundary escalation path.
		"escalate-payment": action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
			id, _ := in["orderID"].(string)
			escalated[id] = true
			fmt.Printf("  [escalate-payment] %s: payment not confirmed in time — escalating\n", id)
			close(escalatedCh)
			return map[string]any{"escalated": true}, nil
		}),
	})

	// Fake clock shared by the engine and the scheduler (ADR-0003).
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

	driver, err := runtime.NewProcessDriver(
		runtime.WithActionCatalog(cat),
		runtime.WithInstanceStore(store),
		runtime.WithClock(clk),
		runtime.WithScheduler(sched),
	)
	if err != nil {
		log.Fatal("driver:", err)
	}

	fmt.Println("--- Order Settlement: Timer Boundary Event ---")

	// Start both orders; each parks at the ReceiveTask with a 30m timer armed.
	for _, id := range []string{"order-ontime", "order-late"} {
		st, rerr := driver.Drive(ctx, def, id, map[string]any{"orderID": id})
		if rerr != nil {
			log.Fatal("run:", rerr)
		}
		fmt.Printf("%s parked at %q (status=%s, boundaries armed=%d)\n",
			id, st.Tokens[0].NodeID, st.Status.String(), len(st.Boundaries))
	}

	// order-ontime: deliver the confirmation before the deadline. The ReceiveTask
	// resumes and the timer boundary is disarmed (it will never fire).
	fmt.Println("delivering payment.confirmed for order-ontime (before the deadline)...")
	if derr := driver.DeliverMessage(ctx, def, "payment.confirmed", "order-ontime", nil); derr != nil {
		log.Fatal("deliver:", derr)
	}

	// order-late: no message. Wait until order-late's timer waiter is armed on the
	// fake clock (order-ontime's timer was cancelled when its message arrived), then
	// advance past 30m — the gocron executor goroutine fires the timer boundary.
	fmt.Println("advancing the clock past 30m to fire order-late's timer boundary...")
	if berr := clk.BlockUntilContext(ctx, 1); berr != nil {
		log.Fatal("block:", berr)
	}
	clk.Advance(31 * time.Minute)

	// The timer fires asynchronously; the escalation-path action closes escalatedCh
	// once it runs, giving a deterministic signal that the breach path executed.
	select {
	case <-escalatedCh:
	case <-time.After(3 * time.Second):
		log.Fatal("timeout: order-late timer boundary did not fire")
	}

	// order-late's terminal commit may still be in-flight after the action ran;
	// poll briefly for its completion (order-ontime is already terminal).
	var ontime, late engine.InstanceState
	for range 200 {
		ontime, _, err = store.Load(ctx, "order-ontime")
		if err != nil {
			log.Fatal("load order-ontime:", err)
		}
		late, _, err = store.Load(ctx, "order-late")
		if err != nil {
			log.Fatal("load order-late:", err)
		}
		if late.Status == engine.StatusCompleted {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	fmt.Printf("order-ontime: status=%s (settled via the message path), escalated=%v\n",
		ontime.Status.String(), escalated["order-ontime"])
	fmt.Printf("order-late:   status=%s (escalated via the timer boundary), escalated=%v\n",
		late.Status.String(), escalated["order-late"])

	if ontime.Status == engine.StatusCompleted && !escalated["order-ontime"] &&
		late.Status == engine.StatusCompleted && escalated["order-late"] {
		fmt.Println("both outcomes correct: on-time settled normally, late escalated via the timer boundary")
	} else {
		fmt.Println("unexpected outcome")
	}
}
