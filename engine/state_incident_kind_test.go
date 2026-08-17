package engine_test

// state_incident_kind_test.go — the IncidentKind enum itself: every kind must be
// distinct, must carry its own String() case, and must be APPENDED.
//
// This deserves a file of its own because IncidentKind is persisted as a plain
// integer. A kind inserted anywhere but the end of the iota block silently
// re-labels every stored incident row, and a missing String() case ships as
// "IncidentKind(unknown)" in operator-facing logs without failing anything.

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/engine"
)

// TestIncidentCompensationFailedDistinctAndStringable is the ADR-0179 counterpart
// of TestTimerCompensationStallDistinctAndStringable in command_test.go, and it
// exists for the same reason: nothing else in the package enumerates the
// IncidentKind constants, so neither a colliding value nor a missing String()
// case would be caught anywhere.
func TestIncidentCompensationFailedDistinctAndStringable(t *testing.T) {
	t.Parallel()

	for _, other := range []engine.IncidentKind{
		engine.IncidentAction,
		engine.IncidentCompensationStall,
	} {
		assert.NotEqual(t, other, engine.IncidentCompensationFailed,
			"IncidentCompensationFailed collides with %v", other)
	}
	assert.Equal(t, "IncidentCompensationFailed", engine.IncidentCompensationFailed.String())
	assert.Equal(t, engine.IncidentCompensationStall+1, engine.IncidentCompensationFailed,
		"IncidentCompensationFailed must be APPENDED after IncidentCompensationStall: "+
			"IncidentKind is persisted as a plain integer, so shifting an existing "+
			"constant's value re-labels every stored incident row")
}
