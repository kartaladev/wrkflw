package model

import (
	"encoding/json"
	"os"
	"testing"
)

// These benchmarks are committed so the figures quoted for the kind-key gate can
// be REGENERATED rather than taken on trust. A number in a review or a PR body
// that no committed harness produces is not evidence, and the per-node cost in
// particular is not a single number: it is proportional to how many keys the
// kind REFUSES, so the kind has to be named for the figure to mean anything.
//
// Run: go test ./definition/model/ -run XXX -bench 'Benchmark(CheckNodeKeys|GoldenDecode)' -benchtime=2000x -count=5

// benchCheckNodeKeys measures the gate alone for one kind. serviceTask refuses 28
// of the 41 gated keys and exclusiveGateway refuses all 41, which is the whole
// spread — a gateway is the worst case precisely because it reads nothing.
func benchCheckNodeKeys(b *testing.B, kind NodeKind, w NodeWire) {
	b.Helper()
	s, ok := specFor(kind)
	if !ok {
		b.Skipf("kind %v is not registered in this binary", kind)
	}
	// Warm the per-kind derivation so the loop measures the steady state, not the
	// one-time probe.
	if err := checkNodeKeys(w, s); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := checkNodeKeys(w, s); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCheckNodeKeysServiceTask(b *testing.B) {
	benchCheckNodeKeys(b, KindServiceTask, NodeWire{ID: "n", Kind: KindServiceTask, Action: "charge"})
}

func BenchmarkCheckNodeKeysExclusiveGateway(b *testing.B) {
	benchCheckNodeKeys(b, KindExclusiveGateway, NodeWire{ID: "g", Kind: KindExclusiveGateway})
}

// BenchmarkGoldenDecode is the denominator: a full 20-node decode, so the gate's
// share of a real definition load is readable rather than asserted.
func BenchmarkGoldenDecode(b *testing.B) {
	raw, err := os.ReadFile("../testdata/golden_definition.json")
	if err != nil {
		b.Fatal(err)
	}
	var warm ProcessDefinition
	if err := json.Unmarshal(raw, &warm); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var def ProcessDefinition
		if err := json.Unmarshal(raw, &def); err != nil {
			b.Fatal(err)
		}
	}
}
