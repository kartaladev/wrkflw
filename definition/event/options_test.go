package event_test

import (
	"context"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/validation"
)

// stubStrategy is a no-op validation.ValidationStrategy used to exercise the
// InputValidation/PayloadValidation option wiring without depending on any
// concrete adapter package.
type stubStrategy struct{}

func (stubStrategy) NewValidator() (validation.Validator, error) {
	return stubValidator{}, nil
}

type stubValidator struct{}

func (stubValidator) Validate(context.Context, map[string]any) error { return nil }

func TestWithInputValidation_Start_SetsSlot(t *testing.T) {
	t.Parallel()
	n := event.NewStart("s", event.WithInputValidation(stubStrategy{}))
	se, ok := n.(event.StartEvent)
	if !ok {
		t.Fatalf("node kind = %T", n)
	}
	if se.InputValidation == nil {
		t.Fatal("InputValidation not set")
	}
}

func TestWithPayloadValidation_Catch_SetsSlot(t *testing.T) {
	t.Parallel()
	n := event.NewIntermediateCatch("wait", event.WithPayloadValidation(stubStrategy{}))
	ce, ok := n.(event.IntermediateCatchEvent)
	if !ok {
		t.Fatalf("node kind = %T", n)
	}
	if ce.PayloadValidation == nil {
		t.Fatal("PayloadValidation not set")
	}
}
