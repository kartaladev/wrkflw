package engine_test

// baseline_operator_incidents_test.go — characterization tests for rows of the
// operator-incidents capability spec (openspec/specs/operator-incidents) that no
// earlier test asserted. Both reuse nilSubprocessDef (step_silent_park_test.go):
// a sub-process carrying no nested definition, which strands its token as an
// IncidentDefinitionDefect.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
)

// strandedSubprocess starts nilSubprocessDef and returns the state holding the
// definition-defect incident it raises.
func strandedSubprocess(t *testing.T, at time.Time) engine.InstanceState {
	t.Helper()

	r, err := engine.Step(t.Context(), nilSubprocessDef(),
		engine.InstanceState{InstanceID: "baseline-nil-sp"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r.State.Tokens, 1, "fixture: exactly one stranded token")
	require.Len(t, r.State.Incidents, 1, "fixture: exactly one incident")
	require.Equal(t, engine.IncidentDefinitionDefect, r.State.Incidents[0].Kind,
		"fixture: the incident is a definition defect")
	require.Equal(t, engine.StatusRunning, r.State.Status,
		"fixture: NOT terminal, or ErrInstanceTerminal answers first")

	return r.State
}

// TestBaselineStrandedSubprocessIncidentNamesItsToken covers the "naming that
// token" clause of "Each stuck shape raises a definition-defect incident" for
// the sub-process row: TestNilSubprocessRaisesIncident checks the node, kind and
// reason but never compares the incident's TokenID with the stranded token.
func TestBaselineStrandedSubprocessIncidentNamesItsToken(t *testing.T) {
	t.Parallel()

	st := strandedSubprocess(t, time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC))

	tok := st.Tokens[0]
	inc := st.Incidents[0]
	require.NotEmpty(t, tok.ID, "fixture: the stranded token has an identity to name")
	assert.Equal(t, engine.TokenIncident, tok.State)
	assert.Equal(t, "sp", tok.NodeID)
	assert.Equal(t, tok.ID, inc.TokenID, "the incident must name the stranded token")
	assert.Equal(t, "sp", inc.NodeID, "the incident must name the stranded node")
}

// TestBaselineResolveIncidentRefusesADefinitionDefect covers the
// IncidentDefinitionDefect row of "Non-action incidents are refused and kept":
// ResolveIncident fails with ErrIncidentNotResolvable and the incident is still
// there to be named afterwards. Step is pure, so "kept" is shown the way
// TestCompensationFailedIncidentIsNotResolvable shows it: naming the same
// incident on the same state a second time is refused again, not a no-op.
func TestBaselineResolveIncidentRefusesADefinitionDefect(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	def := nilSubprocessDef()
	st := strandedSubprocess(t, at)
	incID := st.Incidents[0].ID

	res, err := engine.Step(t.Context(), def, st,
		engine.NewResolveIncident(at.Add(time.Minute), incID, 1), engine.StepOptions{})
	require.ErrorIs(t, err, engine.ErrIncidentNotResolvable,
		"a definition-defect incident is not resolvable through ResolveIncident")
	assert.Empty(t, res.Commands, "a refused resolve re-invokes nothing")

	again, err := engine.Step(t.Context(), def, st,
		engine.NewResolveIncident(at.Add(2*time.Minute), incID, 1), engine.StepOptions{})
	require.ErrorIs(t, err, engine.ErrIncidentNotResolvable,
		"the incident must still be there to refuse a second time")
	assert.Empty(t, again.Commands)
	require.Len(t, st.Incidents, 1, "the caller's state still holds the incident")
	assert.Equal(t, incID, st.Incidents[0].ID)
}
