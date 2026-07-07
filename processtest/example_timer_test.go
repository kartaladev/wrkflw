package processtest_test

import (
	"context"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/processtest"
)

// Example_timerFlow drives a timer-based definition to completion using the
// in-memory Harness. The catch event arms a one-hour timer; AutoTimers advances
// the harness clock past it so the instance runs start → wait → end.
func Example_timerFlow() {
	ctx := context.Background()

	// A minimal timer flow: start → intermediate catch (timer) → end. The timer
	// duration is an expr-lang string that evaluates to a Go duration.
	def, _ := definition.NewBuilder("timer-demo", 1).
		Add(event.NewStart("start")).
		Add(event.NewCatch("wait", event.WithCatchTimer(schedule.AfterExpr(`"1h"`)))).
		Add(event.NewEnd("end")).
		Connect("start", "wait").
		Connect("wait", "end").
		Build()

	h, err := processtest.New()
	if err != nil {
		fmt.Println("new harness:", err)
		return
	}

	if _, err := h.Start(ctx, def, "inst-1", nil); err != nil {
		fmt.Println("start:", err)
		return
	}

	// AutoTimers auto-advances the armed timer so the instance reaches its end.
	final, err := h.DriveToCompletion(ctx, def, "inst-1", processtest.AutoTimers())
	if err != nil {
		fmt.Println("drive:", err)
		return
	}

	fmt.Println("status:", final.Status)
	// Output: status: completed
}
