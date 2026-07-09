package activity_test

import (
	"context"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/model/validate"
)

// stubStrategy is a no-op validate.ValidationStrategy used to exercise the
// CompletionValidation/PayloadValidation option wiring without depending on any
// concrete adapter package.
type stubStrategy struct{}

func (stubStrategy) NewValidator() (validate.Validator, error) {
	return stubValidator{}, nil
}

type stubValidator struct{}

func (stubValidator) Validate(context.Context, map[string]any) error { return nil }

func TestWithCompletionValidation_SetsSlot(t *testing.T) {
	t.Parallel()
	n := activity.NewUserTask("approve", nil, activity.WithCompletionValidation(stubStrategy{}))
	ut, ok := n.(activity.UserTask)
	if !ok {
		t.Fatalf("node kind = %T", n)
	}
	if ut.CompletionValidation == nil {
		t.Fatal("CompletionValidation not set")
	}
}

func TestWithPayloadValidation_Receive_SetsSlot(t *testing.T) {
	t.Parallel()
	n := activity.NewReceiveTask("await", "OrderPlaced", activity.WithPayloadValidation(stubStrategy{}))
	rt, ok := n.(activity.ReceiveTask)
	if !ok {
		t.Fatalf("node kind = %T", n)
	}
	if rt.PayloadValidation == nil {
		t.Fatal("PayloadValidation not set")
	}
}
