package kinds_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	_ "github.com/zakyalvan/krtlwrkflw/definition/kinds"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

// TestAllKindsRegistered asserts every node kind resolves once the kinds bundle
// is imported — guarding against a leaf package forgetting to register a kind.
func TestAllKindsRegistered(t *testing.T) {
	for k := model.KindStartEvent; k <= model.KindEventBasedGateway; k++ {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(k); err != nil {
			t.Fatalf("kind %d not encodable (unregistered name?): %v", int(k), err)
		}
		if k.String() == "" || bytes.Contains([]byte(k.String()), []byte("NodeKind(")) {
			t.Errorf("kind %d has no registered name: %q", int(k), k.String())
		}
	}
}

// TestGoldenRoundTrip decodes a pre-relocation golden definition (covering every
// option-bearing kind) and asserts it re-encodes byte-identically — the wire
// format is frozen by the relocation.
func TestGoldenRoundTrip(t *testing.T) {
	orig, err := os.ReadFile("../testdata/golden_definition.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var def model.ProcessDefinition
	if err := json.Unmarshal(orig, &def); err != nil {
		t.Fatalf("unmarshal golden: %v", err)
	}
	out, err := json.MarshalIndent(def, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(orig), bytes.TrimSpace(out)) {
		t.Fatalf("golden round-trip not byte-identical:\n--- want ---\n%s\n--- got ---\n%s", orig, out)
	}
}
