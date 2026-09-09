package model_test

import (
	"encoding/json"
	"testing"

	// Registers the userTask/subProcess kinds so fromWire can resolve the "kind"
	// discriminators in the raw JSON below. This test operates purely at the wire
	// level (JSON strings decoded via model.ProcessDefinition), so activity's
	// constructors are never referenced directly — the import exists only for its
	// init() side effect.
	_ "github.com/kartaladev/wrkflw/definition/activity"
	_ "github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertRetiredLabelKey is the one message contract for the retired "label" key,
// shared by the JSON table here and the YAML table in yaml_test.go. Both formats
// must name the RETIRED key and its REPLACEMENT, so the contract is asserted in
// one place rather than copied per format.
func assertRetiredLabelKey(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err, "a node carrying the retired `label` key must be refused")
	require.ErrorIs(t, err, model.ErrRetiredLabelKey)
	assert.ErrorContains(t, err, "label", "the message must name the retired key")
	assert.ErrorContains(t, err, "name", "the message must name the replacement key")
}

// TestNodeWireRetiredLabelKey pins the retirement of the node wire key "label".
// "name" is now the display string, so a definition carrying "label" must be
// refused with a message that names the REPLACEMENT key — the migration hint a
// bare `json: unknown field "label"` cannot give. Modelled on
// TestRetiredErrorEndWireName, which asserts the retired name is IN the message
// rather than merely that an error occurred.
//
// The table deliberately pairs refusals with an at-limit ACCEPT (a node with
// "name" and no "label" still decodes) and with a non-label unknown key (which
// must keep naming ITSELF, not "label"), so the check cannot pass by refusing
// everything.
func TestNodeWireRetiredLabelKey(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		json   string
		assert func(t *testing.T, def model.ProcessDefinition, err error)
	}

	refusesNamingBothKeys := func(t *testing.T, _ model.ProcessDefinition, err error) {
		assertRetiredLabelKey(t, err)
	}

	cases := []testCase{
		{
			name:   `label on a node is refused, naming "name"`,
			json:   `{"id":"d1","version":1,"nodes":[{"id":"u","kind":"userTask","name":"sem","label":"Human"}]}`,
			assert: refusesNamingBothKeys,
		},
		{
			name:   `label as the only naming key is refused`,
			json:   `{"id":"d1","version":1,"nodes":[{"id":"u","kind":"userTask","label":"Human"}]}`,
			assert: refusesNamingBothKeys,
		},
		{
			name:   `an empty label value is still the retired key`,
			json:   `{"id":"d1","version":1,"nodes":[{"id":"u","kind":"userTask","name":"sem","label":""}]}`,
			assert: refusesNamingBothKeys,
		},
		{
			name:   `a null label value is still the retired key`,
			json:   `{"id":"d1","version":1,"nodes":[{"id":"u","kind":"userTask","name":"sem","label":null}]}`,
			assert: refusesNamingBothKeys,
		},
		{
			name:   `label inside a nested subprocess node is refused`,
			json:   `{"id":"d1","version":1,"nodes":[{"id":"p","kind":"subProcess","name":"Packing","subprocess":{"id":"sub","version":1,"nodes":[{"id":"u","kind":"userTask","label":"Human"}],"flows":[]}}]}`,
			assert: refusesNamingBothKeys,
		},
		{
			// The at-limit ACCEPT. A bound that refused every node would satisfy
			// every "refused" row above just as well as a correct one.
			name: `name with no label decodes, and Name is the display string`,
			json: `{"id":"d2","version":1,"nodes":[{"id":"u2","kind":"userTask","name":"Approve the request"}]}`,
			assert: func(t *testing.T, def model.ProcessDefinition, err error) {
				require.NoError(t, err)
				require.Len(t, def.Nodes, 1)
				assert.Equal(t, "Approve the request", def.Nodes[0].Name())
			},
		},
		{
			// Discriminates the retirement from the decoder's pre-existing
			// strictness: an unrelated unknown key must keep naming ITSELF.
			name: `an unrelated unknown key still names that key, not label`,
			json: `{"id":"d3","version":1,"nodes":[{"id":"u3","kind":"userTask","caption":"Human"}]}`,
			assert: func(t *testing.T, _ model.ProcessDefinition, err error) {
				require.Error(t, err)
				assert.NotErrorIs(t, err, model.ErrRetiredLabelKey)
				assert.ErrorContains(t, err, "caption")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var def model.ProcessDefinition
			err := json.Unmarshal([]byte(tc.json), &def)
			tc.assert(t, def, err)
		})
	}
}

// TestNodeWireNeverEmitsLabel pins the encode half: no marshalled definition may
// carry the retired key.
//
// Be precise about its strength. The invariant is now held by the TYPE — the
// detection field lives on the unexported nodeWireIn, so no leaf ToWire can reach
// it — which makes this closer to a backstop than to a falsifiable assertion, and
// it earns its place for the second thing it checks: that encoding through
// nodeWireIn still emits NodeWire's fields and nothing else. That claim is what
// keeps the byte-identical golden round-trip true, and it is falsifiable.
func TestNodeWireNeverEmitsLabel(t *testing.T) {
	t.Parallel()

	const src = `{"id":"d","version":1,"nodes":[{"id":"u","kind":"userTask","name":"Approve"}]}`
	var def model.ProcessDefinition
	require.NoError(t, json.Unmarshal([]byte(src), &def))

	out, err := json.Marshal(def)
	require.NoError(t, err)
	assert.NotContains(t, string(out), `"label"`)
	assert.Contains(t, string(out), `"name":"Approve"`)
}
