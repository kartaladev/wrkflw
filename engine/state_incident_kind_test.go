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

// TestIncidentKindOrdinalsAndNames pins every IncidentKind to its exact ordinal
// and its String() output. It is the counterpart of
// TestTimerCompensationStallDistinctAndStringable in command_test.go, and it
// exists for the same reason: nothing else in the package enumerates the
// IncidentKind constants, so neither a colliding value nor a missing String()
// case would be caught anywhere.
//
// Asserting the exact ordinal rather than "is distinct from its siblings" is
// deliberate and is the stronger statement the persisted-integer hazard calls
// for. Distinctness follows from distinct ordinals, and a kind INSERTED rather
// than appended shifts every kind after it — which a pairwise-distinctness
// check would happily accept while every stored incident row silently changed
// meaning. A row here that has to be edited to make the suite pass is the
// signal that stored data just moved under the reader.
func TestIncidentKindOrdinalsAndNames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		kind   engine.IncidentKind
		assert func(t *testing.T, kind engine.IncidentKind)
	}{
		{
			name: "IncidentAction is the zero value",
			kind: engine.IncidentAction,
			assert: func(t *testing.T, kind engine.IncidentKind) {
				assert.Equal(t, 0, int(kind),
					"IncidentAction must stay 0: it is what every incident "+
						"persisted before IncidentKind existed decodes as")
				assert.Equal(t, "IncidentAction", kind.String())
			},
		},
		{
			name: "IncidentCompensationStall is 1",
			kind: engine.IncidentCompensationStall,
			assert: func(t *testing.T, kind engine.IncidentKind) {
				assert.Equal(t, 1, int(kind))
				assert.Equal(t, "IncidentCompensationStall", kind.String())
			},
		},
		{
			name: "IncidentCompensationFailed is 2",
			kind: engine.IncidentCompensationFailed,
			assert: func(t *testing.T, kind engine.IncidentKind) {
				assert.Equal(t, 2, int(kind))
				assert.Equal(t, "IncidentCompensationFailed", kind.String())
			},
		},
		{
			name: "IncidentDefinitionDefect is 3",
			kind: engine.IncidentDefinitionDefect,
			assert: func(t *testing.T, kind engine.IncidentKind) {
				assert.Equal(t, 3, int(kind))
				assert.Equal(t, "IncidentDefinitionDefect", kind.String())
			},
		},
		{
			name: "an unknown ordinal is labelled, not blank",
			kind: engine.IncidentKind(99),
			assert: func(t *testing.T, kind engine.IncidentKind) {
				assert.Equal(t, "IncidentKind(unknown)", kind.String(),
					"an old build decoding a newer snapshot's kind must say so "+
						"rather than render an empty string")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, tc.kind)
		})
	}
}
