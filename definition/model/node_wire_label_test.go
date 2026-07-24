package model_test

import (
	"encoding/json"
	"testing"

	// Registers the userTask kind so fromWire can resolve "kind":"userTask" in
	// the raw JSON below. This test operates purely at the wire level (JSON
	// strings decoded via model.ProcessDefinition), so activity's constructors
	// are never referenced directly — the import exists only for its init()
	// side effect.
	_ "github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/stretchr/testify/require"
)

// TestNodeWire_LabelRawRoundTrip guards the raw (not-baked) node label through
// the JSON wire: an explicit label must be restored by fromWire and re-emitted
// raw by toWire, while an unset label stays omitted from the JSON (Label()
// resolves the Name fallback only in-memory, never gets baked into the wire).
func TestNodeWire_LabelRawRoundTrip(t *testing.T) {
	// Explicit label: MUST be restored by fromWire, then written raw by toWire.
	const withLabel = `{"id":"d1","version":1,"nodes":[{"id":"u","kind":"userTask","name":"sem","label":"Human"}]}`
	var def1 model.ProcessDefinition
	require.NoError(t, json.Unmarshal([]byte(withLabel), &def1))
	require.Equal(t, "Human", def1.Nodes[0].Label()) // guards the fromWire label restore

	data1, err := json.Marshal(def1)
	require.NoError(t, err)
	require.Contains(t, string(data1), `"label":"Human"`) // guards the toWire raw write

	// Unset label: omitted from JSON, Label() resolves to name (fallback).
	const noLabel = `{"id":"d2","version":1,"nodes":[{"id":"u2","kind":"userTask","name":"sem2"}]}`
	var def2 model.ProcessDefinition
	require.NoError(t, json.Unmarshal([]byte(noLabel), &def2))
	require.Equal(t, "sem2", def2.Nodes[0].Label()) // fallback to name after reload

	data2, err := json.Marshal(def2)
	require.NoError(t, err)
	require.NotContains(t, string(data2), `"label"`) // raw-not-baked: unset stays omitted
}
