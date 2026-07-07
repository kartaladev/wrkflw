// Package main demonstrates an ERROR boundary event: a service task that throws
// a coded business error which is caught by a boundary error event and routed to
// a recovery path — the BPMN "try/catch" for activities.
//
// A payment process charges a card. The "charge-card" action fails with the
// error code "INSUFFICIENT_FUNDS". A boundary error event attached to the charge
// task and configured for exactly that code catches the failure and routes the
// token to a decline path instead of failing the whole instance.
//
// Flow:
//
//	start → charge[ServiceTask] ──(success)──────────────────────→ end-paid
//	             └─◄ INSUFFICIENT_FUNDS (error boundary) → decline[Service] → end-declined
//
// How the error code is matched:
//
//   - The engine matches a boundary error event by the FAILING ACTION'S ERROR
//     STRING (ActionFailed.Err == err.Error()) against the boundary's ErrorCode.
//     So an action "throws" a coded error simply by returning an error whose
//     Error() is that code, e.g. errors.New("INSUFFICIENT_FUNDS").
//   - event.WithBoundaryErrorCode("INSUFFICIENT_FUNDS") matches ONLY that code.
//     event.WithBoundaryErrorCode("") (empty) is a CATCH-ALL that matches any
//     error. A coded error with no matching boundary propagates as an unhandled
//     workflow error (StatusFailed / incident), not shown here.
//   - Plain errors are non-retryable, so the boundary fires SYNCHRONOUSLY during
//     driver.Drive — no clock, scheduler, or follow-up Deliver is needed. (A
//     retryable action error would be retried first; see the retry_recovery
//     scenario.)
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// errInsufficientFunds is the coded business error the charge action throws. Its
// Error() string IS the error code the boundary matches against.
const codeInsufficientFunds = "INSUFFICIENT_FUNDS"

func main() {
	ctx := context.Background()

	// Build the process. The error boundary is a node attached to "charge"; its
	// single outgoing flow (Connect) is the recovery path taken when the boundary
	// catches a matching error.
	def, err := definition.NewBuilder("payment", 1).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("charge", activity.WithActionName("charge-card"))).
		// Error boundary matching exactly "INSUFFICIENT_FUNDS". Use
		// WithBoundaryErrorCode("") for a catch-all instead.
		Add(event.NewBoundary("bnd-declined", "charge",
			event.WithBoundaryErrorCode(codeInsufficientFunds))).
		Add(activity.NewServiceTask("decline", activity.WithActionName("notify-decline"))).
		Add(event.NewEnd("end-paid")).
		Add(event.NewEnd("end-declined")).
		Connect("start", "charge").
		Connect("charge", "end-paid").      // normal (successful) path
		Connect("bnd-declined", "decline"). // error boundary flow
		Connect("decline", "end-declined").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	declined := false
	cat := action.NewMapCatalog(map[string]action.Action{
		// Throws the coded business error. Returning a plain error makes it
		// non-retryable, so the boundary catches it immediately.
		"charge-card": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			fmt.Println("  [charge-card] attempting to charge the card...")
			return nil, errors.New(codeInsufficientFunds)
		}),
		// Runs on the boundary recovery path.
		"notify-decline": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			declined = true
			fmt.Println("  [notify-decline] payment declined — notifying the customer")
			return map[string]any{"declined": true}, nil
		}),
	})

	store, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}
	r, err := runtime.NewProcessDriver(
		runtime.WithActionCatalog(cat),
		runtime.WithInstanceStore(store),
	)
	if err != nil {
		log.Fatal("driver:", err)
	}

	const instanceID = "payment-7"

	fmt.Println("--- Payment: Error Boundary Event ---")

	// Run drives the process to a terminal state. The charge fails, the error
	// boundary catches "INSUFFICIENT_FUNDS", and the instance completes via the
	// decline path — all within this single Run call.
	final, err := r.Drive(ctx, def, instanceID, nil)
	if err != nil {
		log.Fatal("run:", err)
	}

	if final.Status == engine.StatusCompleted && declined && len(final.Tokens) == 0 {
		fmt.Printf("charge failed with %q — caught by the error boundary and completed via the decline path (status=%s)\n",
			codeInsufficientFunds, final.Status.String())
	} else {
		fmt.Printf("unexpected outcome: status=%s declined=%v tokens=%d\n",
			final.Status.String(), declined, len(final.Tokens))
	}
}
