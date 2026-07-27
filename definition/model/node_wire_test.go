package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNodeWire_CompletionActionRoundTrip(t *testing.T) {
	w := NodeWire{ID: "u1", Kind: KindUserTask, CompletionAction: "recordApproval"}
	got := w.Activity()
	if got.CompletionAction != "recordApproval" {
		t.Fatalf("Activity() dropped CompletionAction: %q", got.CompletionAction)
	}
	var back NodeWire
	back.PutActivity(got)
	if back.CompletionAction != "recordApproval" {
		t.Fatalf("PutActivity() dropped CompletionAction: %q", back.CompletionAction)
	}
}

// TestDefinitionWireScopedActions covers the marshal-only `scoped_actions` key
// (ADR-0144): it carries the definition-scoped action NAMES so a rendered
// definition is self-describing. It is derived state — the catalog itself is
// never serialized — so unmarshalling tolerates and drops it.
func TestDefinitionWireScopedActions(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		def    ProcessDefinition
		assert func(t *testing.T, encoded map[string]any, decoded ProcessDefinition)
	}

	cases := []testCase{
		{
			name: "scoped action names are marshalled",
			def:  ProcessDefinition{ID: "d", Version: 1, scopedNames: []string{"alpha", "zeta"}},
			assert: func(t *testing.T, encoded map[string]any, decoded ProcessDefinition) {
				assert.Equal(t, []any{"alpha", "zeta"}, encoded["scoped_actions"])
				// Derived, marshal-only: the catalog cannot be reconstructed from names.
				assert.Nil(t, decoded.ScopedCatalog())
				assert.Nil(t, decoded.ScopedActionNames())
			},
		},
		{
			name: "no scoped actions omits the key",
			def:  ProcessDefinition{ID: "d", Version: 1},
			assert: func(t *testing.T, encoded map[string]any, _ ProcessDefinition) {
				assert.NotContains(t, encoded, "scoped_actions")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			raw, err := json.Marshal(tc.def)
			require.NoError(t, err)

			var encoded map[string]any
			require.NoError(t, json.Unmarshal(raw, &encoded))

			var decoded ProcessDefinition
			require.NoError(t, json.Unmarshal(raw, &decoded))

			tc.assert(t, encoded, decoded)
		})
	}
}
