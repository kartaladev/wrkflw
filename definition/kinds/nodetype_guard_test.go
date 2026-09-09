package kinds_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/kartaladev/wrkflw/definition/kinds"
	"github.com/kartaladev/wrkflw/definition/model"
)

const (
	// knownKindCount is the number of node kinds registered at the time of
	// writing (7 activity + 6 event + 4 gateway). It is a floor, not an
	// expectation: the walk covers whatever is registered, and this only catches
	// coverage going backwards.
	knownKindCount = 17
	// kindScanLimit stops the walk from running away if every probed kind somehow
	// resolves to a name.
	kindScanLimit = 1000
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

	// Walk the kinds by REGISTRATION rather than up to a named last constant.
	// `k <= model.KindCompensationThrowEvent` reads as if it tracks the end of the
	// iota block, but a kind appended AFTER that constant is greater than it and
	// is silently skipped — so the guard would fail open on exactly the kind it
	// was added to protect. This repo has already made that mistake once, with an
	// earlier KindEventBasedGateway bound (see kinds_test.go). NodeKind.String
	// falls back to "NodeKind(n)" for a kind with no registered name, which marks
	// the end of the contiguous registered run.
	var covered int
	for k := model.KindStartEvent; int(k) < kindScanLimit; k++ {
		if strings.Contains(k.String(), "NodeKind(") {
			break
		}
		covered++

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

	// The walk above extends itself to new kinds; this line is what makes a kind
	// DISAPPEARING from coverage loud rather than silent.
	assert.GreaterOrEqual(t, covered, knownKindCount,
		"expected at least the %d known node kinds to be covered, walked %d — "+
			"a kind stopped being registered, or the contiguous run was broken by a "+
			"gap in the iota block", knownKindCount, covered)
}
