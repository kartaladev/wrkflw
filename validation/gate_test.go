package validation_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/validation"
)

// countingStrategy counts how many times NewValidator is invoked.
type countingStrategy struct {
	builds *int32
	fail   bool
}

func (s countingStrategy) NewValidator() (validation.Validator, error) {
	atomic.AddInt32(s.builds, 1)
	return funcValidator(func(_ context.Context, in map[string]any) error {
		if s.fail {
			return errors.New("bad input detail")
		}
		return nil
	}), nil
}

func TestGate_BuildsOncePerKeyAndWrapsError(t *testing.T) {
	t.Parallel()
	g := validation.NewGate()
	var builds int32
	s := countingStrategy{builds: &builds, fail: true}

	err := g.Validate(t.Context(), "def:1:node", s, map[string]any{})
	if !errors.Is(err, validation.ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
	// second call with same key must reuse the built validator (no re-build).
	_ = g.Validate(t.Context(), "def:1:node", s, map[string]any{})
	if got := atomic.LoadInt32(&builds); got != 1 {
		t.Fatalf("want 1 build, got %d", got)
	}
}
