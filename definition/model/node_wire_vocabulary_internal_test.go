package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file is INTERNAL (package model) because terminationOutcomes is
// unexported and the property below is about that variable, not about anything
// a consumer can reach. It cannot import definition/event — that would be an
// import cycle — but it does not need to: the external test files in this same
// binary blank-import definition/kinds, so the real endEvent spec is registered
// by the time this runs, and requireRealKindsRegistered is the control that
// says so.
//
// It is the second half of a pair, and neither half is sufficient alone:
//
//   - TestEveryTerminationOutcomeNameStaysDecodable (external) runs
//     TYPE → GATE. It fails if the outcome type gains a name the gate refuses.
//   - This one runs GATE → SPEC. It fails if the gate gains a name the spec
//     does not actually understand, which the external direction cannot see
//     because it never enumerates what the gate accepts.

// TestEveryAcceptedTerminationOutcomeIsOneTheSpecReads pins each accepted name
// against the leaf spec that consumes it: decoding the name and re-projecting
// the node must reproduce the SAME name.
//
// Reproduction is the discriminating property, not mere acceptance. A name the
// spec does not recognise still decodes — that is the whole defect this gate
// exists for — but it collapses to OutcomeComplete and re-projects as
// "complete", so it comes back changed. Asserting acceptance alone would pass
// for any string at all.
func TestEveryAcceptedTerminationOutcomeIsOneTheSpecReads(t *testing.T) {
	t.Parallel()

	requireRealKindsRegistered(t)
	spec, ok := specFor(KindEndEvent)
	require.True(t, ok, "the endEvent spec must be registered")

	require.NotEmpty(t, terminationOutcomes, "an empty vocabulary would pass every assertion below")
	for _, name := range terminationOutcomes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			w := NodeWire{ID: "e", Kind: KindEndEvent, EndBehavior: "terminate", TerminationOutcome: name}
			require.NoError(t, checkTerminationOutcome(w), "the gate must accept its own vocabulary")

			back := toWire(spec.FromWire(Base{id: "e"}, w))
			assert.Equal(t, name, back.TerminationOutcome,
				"the spec does not read this name back as itself, so the gate is accepting a value it collapses")
		})
	}
}
