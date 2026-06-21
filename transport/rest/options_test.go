package rest_test

import (
	"testing"

	rest "github.com/zakyalvan/krtlwrkflw/transport/rest"
)

// TestWithInstanceMapperOption is a compile-time + nil check for WithInstanceMapper.
// Functional behaviour is tested in TestHandlerWithInstanceMapper.
func TestWithInstanceMapperOption(t *testing.T) {
	opt := rest.WithInstanceMapper(nil)
	if opt == nil {
		t.Fatal("WithInstanceMapper must return a non-nil Option")
	}
}
