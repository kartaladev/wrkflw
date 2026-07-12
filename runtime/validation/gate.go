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

	"github.com/kartaladev/wrkflw/definition/model/validate"
)

// ErrInvalidInput is the sentinel wrapping every validation failure. The transport
// layer maps it to HTTP 400. Always wrapped with a detail (which field/predicate/schema).
var ErrInvalidInput = errors.New("workflow-validation: invalid input")

// Gate is the ProcessDriver's executor-side memoizer. It builds a strategy's
// Validator once per strategy DESCRIPTOR (kind + schema — what actually
// determines the compiled validator) and wraps any failure in ErrInvalidInput.
// Keying by descriptor rather than node location is correct (same schema ⇒ same
// validator), bounded (finitely many distinct schemas — no leak), and immune to
// node-id collisions across nested scopes. Definitions stay immutable; the
// driver owns the compiled cache.
type Gate struct {
	mu    sync.RWMutex
	built map[string]validate.Validator
}

// NewGate returns an empty Gate.
func NewGate() *Gate { return &Gate{built: make(map[string]validate.Validator)} }

// Validate builds (once, cached per descriptor) the Validator for s and runs it
// against input. A build error is returned as-is; a validation failure is wrapped
// in ErrInvalidInput.
func (g *Gate) Validate(ctx context.Context, s validate.ValidationStrategy, input map[string]any) error {
	v, err := g.validator(s)
	if err != nil {
		return err
	}
	if err := v.Validate(ctx, input); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())
	}
	return nil
}

// validator resolves s's Validator. Describable strategies are cached under their
// descriptor (kind + schema); non-describable strategies (e.g. callback) have no
// stable key, so their Validator is built fresh each call — their NewValidator is
// a trivial identity wrap, so caching would buy nothing.
func (g *Gate) validator(s validate.ValidationStrategy) (validate.Validator, error) {
	ds, ok := s.(validate.DescribableStrategy)
	if !ok {
		return s.NewValidator()
	}
	d := ds.Descriptor()
	key := d.Kind + "\x00" + d.Schema

	g.mu.RLock()
	v, cached := g.built[key]
	g.mu.RUnlock()
	if cached {
		return v, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if v, cached = g.built[key]; cached { // re-check under write lock
		return v, nil
	}
	v, err := s.NewValidator()
	if err != nil {
		return nil, err
	}
	g.built[key] = v
	return v, nil
}
