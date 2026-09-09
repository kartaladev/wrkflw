package kinds_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/kartaladev/wrkflw/definition/kinds"
	"github.com/kartaladev/wrkflw/definition/model"
)

// foreignNode is a counterfeit for an arbitrary kind: it satisfies model.Node by
// embedding model.Base and reports whatever kind it is told to. Used below to
// prove, one kind at a time, that the kind actually has a recorded type.
type foreignNode struct {
	model.Base

	kind model.NodeKind
}

func (f foreignNode) Kind() model.NodeKind { return f.kind }

// decodeNodeOfKind round-trips a minimal wire node of kind k through the public
// JSON decoder, which is the only exported path to a kind's FromWire. The
// returned node is exactly what that kind's registered FromWire produced, so its
// dynamic type is the one the registry recorded at init — if the two agree.
func decodeNodeOfKind(t *testing.T, k model.NodeKind) model.Node {
	t.Helper()

	kindJSON, err := json.Marshal(k)
	require.NoError(t, err, "kind %d has no registered name", int(k))

	raw := fmt.Sprintf(`{"id":"p","version":1,"nodes":[{"id":"n","kind":%s}]}`, kindJSON)

	var def model.ProcessDefinition
	require.NoError(t, json.Unmarshal([]byte(raw), &def), "decoding a bare %s node", k)
	require.Len(t, def.Nodes, 1)

	return def.Nodes[0]
}

// TestRecordedNodeTypeMatchesFromWire is the guard on the registry's type
// recording: for every registered kind, the concrete type recorded at
// registration must be the type that kind's FromWire actually returns.
//
// It asserts that from outside the model package, using only exported API, by
// pinning both sides against model.ErrForeignNodeType:
//
//   - accepts: the node this kind's FromWire produced must NOT trip the gate.
//     If the recorded type drifted from what FromWire returns, it would.
//   - rejects: a counterfeit reporting this kind MUST trip the gate. This is the
//     half that stops the test passing vacuously — without it, a kind that
//     recorded no type at all would sail through the "accepts" case, and an
//     empty nodeTypes map would score as a full pass.
//
// Neither half can be satisfied by accident: one requires the entry to exist,
// the other requires it to hold the right type.
func TestRecordedNodeTypeMatchesFromWire(t *testing.T) {
	t.Parallel()

	// Bound to the last kind constant, matching TestAllKindsRegistered, so a
	// newly-appended kind is covered without editing this test.
	for k := model.KindStartEvent; k <= model.KindCompensationThrowEvent; k++ {
		t.Run(k.String(), func(t *testing.T) {
			t.Parallel()

			genuine := decodeNodeOfKind(t, k)

			accepts := model.Validate(&model.ProcessDefinition{
				ID: "p", Version: 1, Nodes: []model.Node{genuine},
			})
			assert.NotErrorIs(t, accepts, model.ErrForeignNodeType,
				"kind %s records a type its own FromWire does not return (got %T)", k, genuine)

			rejects := model.Validate(&model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{foreignNode{Base: model.NewBase("n", "n"), kind: k}},
			})
			assert.ErrorIs(t, rejects, model.ErrForeignNodeType,
				"kind %s recorded no concrete type, so nothing guards it", k)
		})
	}
}
