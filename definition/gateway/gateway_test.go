package gateway_test

import (
	"encoding/json"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
)

func TestGatewayConstructors(t *testing.T) {
	cases := []struct {
		node definition.Node
		kind definition.NodeKind
	}{
		{gateway.NewExclusive("x", "XOR"), definition.KindExclusiveGateway},
		{gateway.NewParallel("p"), definition.KindParallelGateway},
		{gateway.NewInclusive("i"), definition.KindInclusiveGateway},
		{gateway.NewEventBased("e"), definition.KindEventBasedGateway},
	}
	for _, c := range cases {
		if c.node.Kind() != c.kind {
			t.Errorf("Kind() = %v, want %v", c.node.Kind(), c.kind)
		}
	}
	if n := gateway.NewExclusive("x", "XOR"); n.ID() != "x" || n.Name() != "XOR" {
		t.Fatalf("id/name = %q/%q", n.ID(), n.Name())
	}
	if n := gateway.NewParallel("p"); n.Name() != "" {
		t.Fatalf("optional name should default empty, got %q", n.Name())
	}
}

func TestGatewayRoundTrip(t *testing.T) {
	def := &definition.ProcessDefinition{
		ID: "g", Version: 1,
		Nodes: []definition.Node{
			gateway.NewExclusive("x"), gateway.NewParallel("p"),
			gateway.NewInclusive("i"), gateway.NewEventBased("e"),
		},
	}
	data, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	var got definition.ProcessDefinition
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	for i, n := range got.Nodes {
		if n.Kind() != def.Nodes[i].Kind() || n.ID() != def.Nodes[i].ID() {
			t.Errorf("node %d = %v/%q, want %v/%q", i, n.Kind(), n.ID(), def.Nodes[i].Kind(), def.Nodes[i].ID())
		}
	}
}
