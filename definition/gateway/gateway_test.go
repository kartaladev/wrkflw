package gateway_test

import (
	"encoding/json"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

func TestGatewayConstructors(t *testing.T) {
	cases := []struct {
		node model.Node
		kind model.NodeKind
	}{
		{gateway.NewExclusive("x", "XOR"), model.KindExclusiveGateway},
		{gateway.NewParallel("p"), model.KindParallelGateway},
		{gateway.NewInclusive("i"), model.KindInclusiveGateway},
		{gateway.NewEventBased("e"), model.KindEventBasedGateway},
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
