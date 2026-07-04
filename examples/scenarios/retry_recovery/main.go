// Package main demonstrates a retry policy that lets a flaky action RECOVER: the
// action fails twice, then succeeds on the third attempt, and the instance
// completes normally — no incident is raised.
//
// A WithRetryPolicy on an activity turns a retryable failure into a scheduled
// retry rather than an immediate incident. Retries are NOT automatic: a failed
// attempt parks the instance and schedules a backoff timer on the injected
// Scheduler. The retry fires only when the clock advances past the timer and the
// scheduler is ticked — so this example drives a *clockwork.FakeClock and calls
// sched.Tick between attempts, which makes the exponential backoff deterministic
// and instant.
//
// Backoff = InitialInterval × BackoffCoef^attempt (attempt is 0-based). With
// InitialInterval=1s, BackoffCoef=2.0 and a fixed jitter of 1.0:
//
//	attempt 1 fails at T      → retry armed at T+1s   (1s × 2^0)
//	attempt 2 fails at T+1s   → retry armed at T+3s   (2s × 2^1, added)
//	attempt 3 succeeds at T+3s → StatusCompleted
//
// To keep the delays deterministic the example supplies a fixed jitter source
// (fraction 1.0 = use the full computed backoff, no randomization).
//
// Flow:
//
//	start → charge[Service "charge-card", retry ≤5] → end
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
)

// fixedJitter returns the full computed backoff (no randomization) so the retry
// delays are deterministic. It satisfies kernel.JitterSource (Fraction() float64).
type fixedJitter struct{}

func (fixedJitter) Fraction() float64 { return 1.0 }

func main() {
	ctx := context.Background()

	// The action is charged up to 5 times with exponential backoff. It recovers
	// on attempt 3, so it never exhausts the budget.
	def, err := definition.NewBuilder("payment", 1).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("charge",
			activity.WithActionName("charge-card"),
			activity.WithRetryPolicy(&definition.RetryPolicy{
				MaxAttempts:     5,
				InitialInterval: time.Second,
				BackoffCoef:     2.0,
				MaxInterval:     time.Minute,
			}),
		)).
		Add(event.NewEnd("end")).
		Connect("start", "charge").
		Connect("charge", "end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	// Flaky action: fails the first two attempts, succeeds on the third.
	attempts := 0
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"charge-card": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			attempts++
			if attempts < 3 {
				fmt.Printf("  [charge-card] attempt %d — transient gateway error\n", attempts)
				return nil, errors.New("payment gateway timeout")
			}
			fmt.Printf("  [charge-card] attempt %d — charge succeeded\n", attempts)
			return map[string]any{"charged": true}, nil
		}),
	})

	startAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clk := clockwork.NewFakeClockAt(startAt)
	sched := kernel.NewMemScheduler(kernel.WithMemSchedulerClock(clk))
	store, err := kernel.NewMemStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}

	r, err := runtime.NewProcessDriver(cat, store,
		runtime.WithClock(clk),
		runtime.WithScheduler(sched),
		runtime.WithJitterSource(fixedJitter{}),
	)
	if err != nil {
		log.Fatal("runner:", err)
	}

	const instanceID = "pay-1"

	fmt.Println("--- Payment: Retry with Backoff (recovery) ---")

	// Run: attempt 1 fails and the instance parks on the retry timer.
	st, err := r.Run(ctx, def, instanceID, nil)
	if err != nil {
		log.Fatal("run:", err)
	}
	fmt.Printf("after first attempt: status=%s (retry scheduled)\n", view.StatusString(st.Status))

	// Drive the backoff: advance the clock to each armed retry and tick. A timer
	// armed inside a fire callback is not fired in the same Tick, so each attempt
	// needs its own advance+tick. Advancing generously past the next fire time is
	// safe — Tick fires every timer already due.
	for attempts < 3 {
		clk.Advance(1 * time.Minute)
		if err := sched.Tick(ctx); err != nil {
			log.Fatal("tick:", err)
		}
	}

	final, _, err := store.Load(ctx, instanceID)
	if err != nil {
		log.Fatal("load:", err)
	}
	fmt.Printf("final: status=%s after %d attempts (charged=%v)\n",
		view.StatusString(final.Status), attempts, final.Variables["charged"])

	if final.Status == engine.StatusCompleted && final.Variables["charged"] == true {
		fmt.Println("OK: the flaky action recovered via retry; no incident raised")
	} else {
		fmt.Println("unexpected outcome")
	}
}
