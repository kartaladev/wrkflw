package rest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zakyalvan/krtlwrkflw/engine"
	rest "github.com/zakyalvan/krtlwrkflw/transport/rest"
)

// TestWithInstanceMapperNilPanics asserts that passing nil to WithInstanceMapper
// panics immediately at option-construction time, not at request time.
func TestWithInstanceMapperNilPanics(t *testing.T) {
	assert.Panics(t, func() {
		rest.WithInstanceMapper(nil)
	}, "WithInstanceMapper(nil) must panic immediately")
}

// TestWithInstanceMapperNonNilDoesNotPanic asserts that a non-nil mapper is accepted.
func TestWithInstanceMapperNonNilDoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		rest.WithInstanceMapper(func(engine.InstanceState) any {
			return map[string]string{"ok": "yes"}
		})
	}, "WithInstanceMapper with a valid fn must not panic")
}
