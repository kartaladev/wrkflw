package persistence_test

import (
	"testing"

	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/service"
)

// TestRelaySatisfiesDeadLetterAdmin is a compile-time guard that the persistence
// Relay façade satisfies the service.DeadLetterAdmin seam, so consumers can pass
// their relay straight to a transport's WithDeadLetterAdmin option with no adapter.
func TestRelaySatisfiesDeadLetterAdmin(t *testing.T) {
	t.Parallel()
	var _ service.DeadLetterAdmin = (persistence.Relay)(nil)
}
