package engine

// state_test.go — white-box tests for InstanceState and compensationCursor.
// Uses package engine (not engine_test) to access unexported types.

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// cloneState: compensationCursor outcome fields
// ---------------------------------------------------------------------------

// TestCloneStateCarriesCompensatingOutcomeFields asserts that cloneState copies
// compensationCursor.FinalStatus and FinalErr (scalar fields; struct-value copy
// covers them) and that mutating the clone's cursor does not affect the original.
func TestCloneStateCarriesCompensatingOutcomeFields(t *testing.T) {
	st := InstanceState{
		InstanceID: "cs-outcome-1",
		Compensating: compensationCursor{
			ScopeID:      "",
			ToNode:       "",
			NextIndex:    2,
			ActiveCmdID:  "cmd-1",
			FinalStatus:  StatusFailed,
			FinalErr:     "x",
		},
	}

	clone := cloneState(st)

	// Clone must carry the new fields.
	assert.Equal(t, StatusFailed, clone.Compensating.FinalStatus,
		"cloneState must copy FinalStatus from the source cursor")
	assert.Equal(t, "x", clone.Compensating.FinalErr,
		"cloneState must copy FinalErr from the source cursor")

	// Mutating the clone's cursor must not affect the original (they are independent
	// value copies — no aliasing on scalar fields).
	clone.Compensating.FinalStatus = StatusTerminated
	clone.Compensating.FinalErr = "mutated"

	assert.Equal(t, StatusFailed, st.Compensating.FinalStatus,
		"mutating clone.Compensating.FinalStatus must not affect original")
	assert.Equal(t, "x", st.Compensating.FinalErr,
		"mutating clone.Compensating.FinalErr must not affect original")
}
