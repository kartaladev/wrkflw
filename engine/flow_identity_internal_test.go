package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
)

// TestFlowRefAccessorsMatchModelOrder pins that the index-carrying accessors are
// drop-in equivalents of model.ProcessDefinition's own — same flows, same order.
//
// Branch declaration order is documented, load-bearing behaviour in this engine:
// forkParallel places tokens in definition order of outgoing flows, an exclusive
// gateway takes the first flow whose condition holds, and moveAlongSingleFlow
// always takes out[0]. Replacing def.Outgoing with outgoingFlows at those sites
// would silently reroute processes if the order differed, and no assertion in the
// suite would name the cause. This is a floor under that substitution rather than
// a test of the helpers' own logic.
//
// The fixture is deliberately adversarial about order: the Flows slice is NOT
// grouped by source or target, it holds blank IDs, duplicate blank IDs, and two
// edges identical in every authored field — exactly the shapes the identity has
// to survive.
func TestFlowRefAccessorsMatchModelOrder(t *testing.T) {
	t.Parallel()

	def := &model.ProcessDefinition{
		ID: "order", Version: 1,
		Flows: []flow.SequenceFlow{
			{ID: "z", Source: "a", Target: "j"},
			{Source: "b", Target: "j"},
			{ID: "y", Source: "j", Target: "k"},
			{Source: "a", Target: "j"},
			{ID: "x", Source: "b", Target: "m"},
			{Source: "a", Target: "j"},
		},
	}

	type testCase struct {
		name string
		got  func() []flowRef
		want func() []flow.SequenceFlow
	}

	cases := []testCase{
		{
			name: "outgoing of a node with several edges, some blank and identical",
			got:  func() []flowRef { return outgoingFlows(def, "a") },
			want: func() []flow.SequenceFlow { return def.Outgoing("a") },
		},
		{
			name: "incoming of a join whose edges are interleaved in the Flows slice",
			got:  func() []flowRef { return incomingFlows(def, "j") },
			want: func() []flow.SequenceFlow { return def.Incoming("j") },
		},
		{
			name: "outgoing of a node with exactly one edge",
			got:  func() []flowRef { return outgoingFlows(def, "j") },
			want: func() []flow.SequenceFlow { return def.Outgoing("j") },
		},
		{
			name: "a node with no edges at all yields nothing from either",
			got:  func() []flowRef { return outgoingFlows(def, "nosuch") },
			want: func() []flow.SequenceFlow { return def.Outgoing("nosuch") },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, want := tc.got(), tc.want()
			require.Len(t, got, len(want))
			for i := range want {
				assert.Equal(t, want[i], got[i].Flow, "flow at position %d differs", i)
				assert.Equal(t, want[i], def.Flows[got[i].Index],
					"Index %d must address this very flow in def.Flows", got[i].Index)
			}
		})
	}
}

// TestFlowIdentityIsUniquePerEdge pins the property the whole per-flow join
// accounting rests on: distinct edges get distinct identities, INCLUDING edges
// that are identical in every field a definition author can write.
//
// This is the assertion an endpoint-derived scheme fails. It is stated over the
// definition rather than over a run so that it cannot be satisfied by a lucky
// execution order.
func TestFlowIdentityIsUniquePerEdge(t *testing.T) {
	t.Parallel()

	def := &model.ProcessDefinition{
		ID: "identity", Version: 1,
		Flows: []flow.SequenceFlow{
			{ID: "named", Source: "s", Target: "j"},
			{Source: "s", Target: "j"},                             // blank
			{Source: "s", Target: "j"},                             // blank twin, identical in every field
			{ID: "", Source: "s", Target: "j", Condition: "x > 1"}, // blank, differs only by condition
		},
	}

	seen := make(map[string]int, len(def.Flows))
	for _, ref := range incomingFlows(def, "j") {
		id := ref.Identity()
		if prev, dup := seen[id]; dup {
			t.Fatalf("flows at positions %d and %d share the identity %q — the identity is not a key",
				prev, ref.Index, id)
		}
		seen[id] = ref.Index
	}
	assert.Len(t, seen, 4, "all four edges into j must be separately identifiable")

	// The authored ID alone is NOT a key, which is why the index leads. Stated as
	// an assertion so the premise is pinned rather than asserted in a comment.
	authored := make(map[string]bool, len(def.Flows))
	for _, f := range def.Incoming("j") {
		authored[f.ID] = true
	}
	assert.Len(t, authored, 2,
		"four distinct edges collapse to two authored IDs: model.Validate permits any number of blank ones")
}
