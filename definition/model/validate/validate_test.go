package validate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/definition/model/validate"
)

// funcValidator adapts a func to the Validator port for tests.
type funcValidator func(ctx context.Context, input map[string]any) error

func (f funcValidator) Validate(ctx context.Context, input map[string]any) error {
	return f(ctx, input)
}

// funcStrategy builds a fixed Validator.
type funcStrategy struct{ v validate.Validator }

func (s funcStrategy) NewValidator() (validate.Validator, error) { return s.v, nil }

func TestValidator_ReturnsError(t *testing.T) {
	t.Parallel()
	v := funcValidator(func(_ context.Context, in map[string]any) error {
		if in["amount"] == nil {
			return errors.New("amount required")
		}
		return nil
	})
	if err := v.Validate(t.Context(), map[string]any{}); err == nil {
		t.Fatal("expected error for missing amount")
	}
	if err := v.Validate(t.Context(), map[string]any{"amount": 1}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
