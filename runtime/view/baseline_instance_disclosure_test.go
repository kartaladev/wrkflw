package view_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/runtime/view"
)

// TestBaseline_PublicState_ActorsRestoresCandidateIDs characterizes the
// {actors} / candidates row of openspec/specs/instance-disclosure "Categories
// are independent": projected under DiscloseActors alone, each candidate
// actor ID is restored while the completion note stays empty.
func TestBaseline_PublicState_ActorsRestoresCandidateIDs(t *testing.T) {
	t.Parallel()

	st := stateWithSecretEverywhere(t)
	// A second candidate so "each" is more than the fixture's single one.
	st.Tasks[0].Candidates = append(st.Tasks[0].Candidates, authz.Actor{ID: "cand-2"})

	got := view.PublicState(st, authz.NewDisclosureSet(authz.DiscloseActors))

	require.Len(t, got.Tasks, 1)
	ids := make([]string, 0, len(got.Tasks[0].Candidates))
	for _, c := range got.Tasks[0].Candidates {
		ids = append(ids, c.ID)
	}
	assert.Equal(t, []string{secret, "cand-2"}, ids, "every candidate actor ID is restored")
	require.NotNil(t, got.Tasks[0].Completion)
	assert.Empty(t, got.Tasks[0].Completion.Note, "DiscloseActors must not restore the note")
}
