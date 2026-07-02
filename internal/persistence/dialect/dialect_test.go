// Package dialect_test verifies the dialect package's public API.
package dialect_test

import (
	"errors"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
)

func TestErrUnsupportedIsSentinel(t *testing.T) {
	if dialect.ErrUnsupported == nil {
		t.Fatal("ErrUnsupported must be a non-nil sentinel")
	}
	wrapped := errors.Join(errors.New("ctx"), dialect.ErrUnsupported)
	if !errors.Is(wrapped, dialect.ErrUnsupported) {
		t.Fatal("ErrUnsupported must be matchable via errors.Is")
	}
}
