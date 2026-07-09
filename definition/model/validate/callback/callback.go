// Package callback is a code-only validation adapter wrapping a Go func. It is NOT
// declarative: it has no Descriptor and cannot be serialized. A definition carrying a
// callback strategy fails MarshalJSON (fail-closed) — use a declarative strategy to persist.
package callback

import (
	"context"

	"github.com/zakyalvan/krtlwrkflw/definition/model/validate"
)

type strategy struct {
	fn func(ctx context.Context, input map[string]any) error
}

// New wraps fn as a (non-serializable) validation strategy.
func New(fn func(ctx context.Context, input map[string]any) error) validate.ValidationStrategy {
	return strategy{fn: fn}
}

func (s strategy) NewValidator() (validate.Validator, error) { return s, nil }

func (s strategy) Validate(ctx context.Context, input map[string]any) error {
	return s.fn(ctx, input)
}
