package event_test

import (
	"errors"
	"fmt"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
)

// ExampleWithBoundaryAction shows how to attach a fire-once catalog action to a
// boundary event. The action is invoked before the boundary routes the token
// (any trigger type, interrupting or non-interrupting). Its result is discarded;
// failure is logged and routing continues normally.
func ExampleWithBoundaryAction() {
	n := event.NewBoundary("bnd-timeout", "review-task",
		event.WithBoundaryAction("notify-overdue"),
	)
	b := n.(event.BoundaryEvent)
	fmt.Println(b.Action)
	// Output:
	// notify-overdue
}

// ExampleWithBoundaryErrorExpr shows how to set a serializable expr-lang
// predicate that decides whether an error boundary catches a thrown error.
// The predicate is evaluated over the process-instance variables plus the
// injected _error variable (the thrown error code string). Use plain equality
// comparisons for multi-code matching — the default evaluator does not support
// strings.HasPrefix or the in operator on strings.
func ExampleWithBoundaryErrorExpr() {
	n := event.NewBoundary("bnd-payment-error", "charge-task",
		event.WithBoundaryErrorExpr(`_error == "INSUFFICIENT_FUNDS" || _error == "CARD_EXPIRED"`),
	)
	b := n.(event.BoundaryEvent)
	fmt.Println(b.ErrorExpr)
	// Output:
	// _error == "INSUFFICIENT_FUNDS" || _error == "CARD_EXPIRED"
}

// ExampleWithBoundaryErrorCheck shows how to set a Go predicate that decides
// whether an error boundary catches. The predicate receives the current instance
// variables and the original error (enabling errors.Is / errors.As). This is the
// highest-precedence matching path (Check → Expr → Code) and is not serialized
// to the definition wire format — use it for Go-authored definitions only.
func ExampleWithBoundaryErrorCheck() {
	// sentinel error we want to catch via errors.Is
	var ErrPaymentDeclined = errors.New("PAYMENT_DECLINED")

	n := event.NewBoundary("bnd-declined", "charge-task",
		event.WithBoundaryErrorCheck(func(_ map[string]any, err error) bool {
			return errors.Is(err, ErrPaymentDeclined)
		}),
	)
	b := n.(event.BoundaryEvent)
	fmt.Println(b.ErrorCheck != nil)
	fmt.Println(b.Kind() == model.KindBoundaryEvent)
	// Output:
	// true
	// true
}
