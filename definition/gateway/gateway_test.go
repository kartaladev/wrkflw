package gateway_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
)

func TestGatewayConstructors(t *testing.T) {
	cases := []struct {
		node model.Node
		kind model.NodeKind
	}{
		{gateway.NewExclusive("x", gateway.WithName("XOR")), model.KindExclusiveGateway},
		{gateway.NewParallel("p"), model.KindParallelGateway},
		{gateway.NewInclusive("i"), model.KindInclusiveGateway},
		{gateway.NewEventBased("e"), model.KindEventBasedGateway},
	}
	for _, c := range cases {
		if c.node.Kind() != c.kind {
			t.Errorf("Kind() = %v, want %v", c.node.Kind(), c.kind)
		}
	}
	if n := gateway.NewExclusive("x", gateway.WithName("XOR")); n.ID() != "x" || n.Name() != "XOR" {
		t.Fatalf("id/name = %q/%q", n.ID(), n.Name())
	}
	if n := gateway.NewParallel("p"); n.Name() != "" {
		t.Fatalf("optional name should default empty, got %q", n.Name())
	}
}

// TestGatewayOptions covers the functional-options constructors: WithName sets
// the semantic name, WithLabel sets the human display label (overriding the
// Name fallback), and a bare id with no options remains valid with empty
// name/label — preserving source compatibility for the 100+ id-only call
// sites across the repo.
func TestGatewayOptions(t *testing.T) {
	cases := []struct {
		name   string
		node   func() model.Node
		assert func(t *testing.T, n model.Node)
	}{
		{
			name: "name and label both set",
			node: func() model.Node {
				return gateway.NewExclusive("x", gateway.WithName("Decision"), gateway.WithLabel("Approve?"))
			},
			assert: func(t *testing.T, n model.Node) {
				require.Equal(t, "Decision", n.Name())
				require.Equal(t, "Approve?", n.Label())
			},
		},
		{
			name: "unset label falls back to name",
			node: func() model.Node { return gateway.NewParallel("fork", gateway.WithName("Fork")) },
			assert: func(t *testing.T, n model.Node) {
				require.Equal(t, "Fork", n.Label())
			},
		},
		{
			name: "bare id valid, empty name",
			node: func() model.Node { return gateway.NewInclusive("i") },
			assert: func(t *testing.T, n model.Node) {
				require.Equal(t, "", n.Name())
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t, tc.node())
		})
	}
}

func TestGatewayRoundTrip(t *testing.T) {
	def := &model.ProcessDefinition{
		ID: "g", Version: 1,
		Nodes: []model.Node{
			gateway.NewExclusive("x"), gateway.NewParallel("p"),
			gateway.NewInclusive("i"), gateway.NewEventBased("e"),
		},
	}
	data, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	var got model.ProcessDefinition
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	for i, n := range got.Nodes {
		if n.Kind() != def.Nodes[i].Kind() || n.ID() != def.Nodes[i].ID() {
			t.Errorf("node %d = %v/%q, want %v/%q", i, n.Kind(), n.ID(), def.Nodes[i].Kind(), def.Nodes[i].ID())
		}
	}
}
