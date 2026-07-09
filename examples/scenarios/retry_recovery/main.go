// Package main demonstrates a retry policy that lets a flaky action RECOVER: the
// action fails twice, then succeeds on the third attempt, and the instance
// completes normally — no incident is raised.
//
// A WithRetryPolicy on an activity turns a retryable failure into a scheduled
// retry rather than an immediate incident. Retries are NOT automatic: a failed
// attempt parks the instance and schedules a backoff timer on the injected
// Scheduler. The retry fires only when the clock advances past the timer — so
// this example drives a *clockwork.FakeClock shared by the engine and the
// gocron-backed scheduler, which makes the exponential backoff deterministic and
// instant. Because the scheduler fires on its own executor goroutine, an attempt
// channel signalled from the action makes each retry observable:
// BlockUntilContext → Advance → <-attemptCh.
//
// Backoff = InitialInterval × BackoffCoef^attempt (attempt is 0-based). With
// InitialInterval=1s, BackoffCoef=2.0 and a fixed jitter of 1.0:
//
//	attempt 1 fails at T      → retry armed at T+1s   (1s × 2^0)
//	attempt 2 fails at T+1s   → retry armed at T+3s   (1s × 2^1 = 2s delay)
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
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
	"github.com/zakyalvan/krtlwrkflw/scheduling"
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
			activity.WithTaskAction("charge-card"),
			activity.WithRetryPolicy(&model.RetryPolicy{
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

	// Flaky action: fails the first two attempts, succeeds on the third. attemptCh
	// is signalled at the end of every invocation so the main goroutine can observe
	// each async retry fire deterministically.
	attempts := 0
	attemptCh := make(chan struct{}, 8)
	cat := action.NewCatalog(map[string]action.Action{
		"charge-card": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			attempts++
			if attempts < 3 {
				fmt.Printf("  [charge-card] attempt %d — transient gateway error\n", attempts)
				select {
				case attemptCh <- struct{}{}:
				default:
				}
				return nil, errors.New("payment gateway timeout")
			}
			fmt.Printf("  [charge-card] attempt %d — charge succeeded\n", attempts)
			select {
			case attemptCh <- struct{}{}:
			default:
			}
			return map[string]any{"charged": true}, nil
		}),
	})

	startAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
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
		runtime.WithJitterSource(fixedJitter{}),
	)
	if err != nil {
		log.Fatal("runner:", err)
	}

	const instanceID = "pay-1"

	fmt.Println("--- Payment: Retry with Backoff (recovery) ---")

	// Run: attempt 1 fails and the instance parks on the retry timer.
	st, err := driver.Drive(ctx, def, instanceID, nil)
	if err != nil {
		log.Fatal("run:", err)
	}
	fmt.Printf("after first attempt: status=%s (retry scheduled)\n", view.StatusString(st.Status))

	// Attempt 1 ran synchronously inside Run and already signalled attemptCh; drain
	// that so the loop below observes only the async retry attempts.
	select {
	case <-attemptCh:
	default:
	}

	// Drive the backoff: each failed attempt arms a fresh one-shot retry timer once
	// the previous fire completes. Per iteration, wait until the scheduler has armed
	// the next retry waiter on the fake clock, advance generously past the backoff,
	// then wait for the retry attempt to run. The loop is capped so a stuck retry
	// cannot spin forever.
	for i := 0; i < 5 && attempts < 3; i++ {
		if err := clk.BlockUntilContext(ctx, 1); err != nil {
			log.Fatal("block:", err)
		}
		clk.Advance(1 * time.Minute)
		select {
		case <-attemptCh:
		case <-time.After(3 * time.Second):
			log.Fatal("timeout: retry attempt did not fire")
		}
	}

	// The final terminal commit may still be in-flight after the last attempt ran;
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
	fmt.Printf("final: status=%s after %d attempts (charged=%v)\n",
		view.StatusString(final.Status), attempts, final.Variables["charged"])

	if final.Status == engine.StatusCompleted && final.Variables["charged"] == true {
		fmt.Println("OK: the flaky action recovered via retry; no incident raised")
	} else {
		fmt.Println("unexpected outcome")
	}
}
