// Package validation is the executor-side of external-input validation: it
// runs a definition/model/validate.ValidationStrategy against runtime input and
// wraps any failure in ErrInvalidInput. The declarative port + adapters +
// reconstruction registry live in definition/model/validate (the authoring
// side); this package depends on it.
package validation

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/zakyalvan/krtlwrkflw/definition/model/validate"
)

// ErrInvalidInput is the sentinel wrapping every validation failure. The transport
// layer maps it to HTTP 400. Always wrapped with a detail (which field/predicate/schema).
var ErrInvalidInput = errors.New("workflow-validation: invalid input")

// Gate is the executor-side memoizer shared by the driver and task service. It builds
// a strategy's Validator once per key (compile-once) and wraps any failure in
// ErrInvalidInput. Definitions stay immutable; the executor owns the compiled cache.
type Gate struct {
	mu    sync.RWMutex
	built map[string]validate.Validator
}

// NewGate returns an empty Gate.
func NewGate() *Gate { return &Gate{built: make(map[string]validate.Validator)} }

// Validate builds (once, cached under key) the Validator for s and runs it against input.
// A build error is returned as-is; a validation failure is wrapped in ErrInvalidInput.
func (g *Gate) Validate(ctx context.Context, key string, s validate.ValidationStrategy, input map[string]any) error {
	v, err := g.validator(key, s)
	if err != nil {
		return err
	}
	if err := v.Validate(ctx, input); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())
	}
	return nil
}

func (g *Gate) validator(key string, s validate.ValidationStrategy) (validate.Validator, error) {
	g.mu.RLock()
	v, ok := g.built[key]
	g.mu.RUnlock()
	if ok {
		return v, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if v, ok = g.built[key]; ok { // re-check under write lock
		return v, nil
	}
	v, err := s.NewValidator()
	if err != nil {
		return nil, err
	}
	g.built[key] = v
	return v, nil
}
