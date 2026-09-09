package model_test

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	_ "github.com/kartaladev/wrkflw/definition/kinds"
	"github.com/kartaladev/wrkflw/definition/model"
)

// kindGateCase is one node shape, expressed once and driven through BOTH
// decoders. The JSON and YAML key sets are the same strings — nodeYAML's
// yaml:"…" tags mirror NodeWire's json:"…" tags — so a single map of node keys
// renders to either wire form, and a case that passes on one decoder and fails
// on the other is exactly the two-gates bug the single fromWire seam exists to
// prevent.
type kindGateCase struct {
	name string
	kind string
	keys map[string]any
	// yamlSkip marks a case whose keys have no YAML tag at all (the nested
	// trigger forms are JSON-only), so there is no YAML half to run.
	yamlSkip bool
	assert   func(t *testing.T, err error)
}

// refusedKey asserts the decoder refused the node with ErrKeyNotOnKind and that
// the diagnostic names the key, the kind and the node id — the three facts an
// author needs to find the line they mistyped.
func refusedKey(key, kind string) func(*testing.T, error) {
	return func(t *testing.T, err error) {
		t.Helper()
		require.ErrorIs(t, err, model.ErrKeyNotOnKind)
		msg := err.Error()
		assert.Contains(t, msg, key, "diagnostic must name the offending key")
		assert.Contains(t, msg, kind, "diagnostic must name the kind")
		assert.Contains(t, msg, "n1", "diagnostic must name the node id")
	}
}

func acceptedKey(t *testing.T, err error) {
	t.Helper()
	require.NoError(t, err)
}

// kindGateCases is the shared case list. The refusals are the six probes
// measured on the issue plus the three keys the repo's own allFieldsYAML
// fixture misplaced; the acceptances are the at-limit controls that stop a gate
// which simply refuses everything from passing.
func kindGateCases() []kindGateCase {
	return []kindGateCase{
		// --- the six measured silent drops (#183) ---
		{
			name:   "action on an exclusiveGateway",
			kind:   "exclusiveGateway",
			keys:   map[string]any{"action": "charge"},
			assert: refusedKey("action", "exclusiveGateway"),
		},
		{
			name:   "rule on a serviceTask",
			kind:   "serviceTask",
			keys:   map[string]any{"rule": "pricing"},
			assert: refusedKey("rule", "serviceTask"),
		},
		{
			name:   "rule on an exclusiveGateway",
			kind:   "exclusiveGateway",
			keys:   map[string]any{"rule": "pricing"},
			assert: refusedKey("rule", "exclusiveGateway"),
		},
		{
			name:   "outcomes on a parallelGateway",
			kind:   "parallelGateway",
			keys:   map[string]any{"outcomes": []any{"approve"}},
			assert: refusedKey("outcomes", "parallelGateway"),
		},
		{
			name:   "signal_name on a serviceTask",
			kind:   "serviceTask",
			keys:   map[string]any{"signal_name": "paid"},
			assert: refusedKey("signal_name", "serviceTask"),
		},
		{
			name:   "error_code on a userTask",
			kind:   "userTask",
			keys:   map[string]any{"error_code": "E_BOOM"},
			assert: refusedKey("error_code", "userTask"),
		},
		// --- the three acceptance rows the design stands or falls on ---
		{
			name:   "timer_duration on an intermediateCatchEvent is the legacy flat form and stays accepted",
			kind:   "intermediateCatchEvent",
			keys:   map[string]any{"timer_duration": "PT1H"},
			assert: acceptedKey,
		},
		{
			name:   "timer_duration on a userTask",
			kind:   "userTask",
			keys:   map[string]any{"timer_duration": "PT5M"},
			assert: refusedKey("timer_duration", "userTask"),
		},
		{
			name:   "compensate_ref on a serviceTask",
			kind:   "serviceTask",
			keys:   map[string]any{"compensate_ref": "refund-ref"},
			assert: refusedKey("compensate_ref", "serviceTask"),
		},
		{
			name:   "compensate_scope_local on a serviceTask",
			kind:   "serviceTask",
			keys:   map[string]any{"compensate_scope_local": true},
			assert: refusedKey("compensate_scope_local", "serviceTask"),
		},
		{
			// The fail-open instance: serviceTask has no validation slot, so this
			// descriptor is discarded and NOTHING validates.
			name:   "validation on a serviceTask",
			kind:   "serviceTask",
			keys:   map[string]any{"validation": map[string]any{"kind": "expr", "schema": "true"}},
			assert: refusedKey("validation", "serviceTask"),
		},
		// --- at-limit accepts: the legal key on the kind that carries it ---
		{
			name:   "action on a serviceTask",
			kind:   "serviceTask",
			keys:   map[string]any{"action": "charge"},
			assert: acceptedKey,
		},
		{
			name:   "rule on a businessRuleTask",
			kind:   "businessRuleTask",
			keys:   map[string]any{"rule": "pricing"},
			assert: acceptedKey,
		},
		{
			name:   "outcomes on a userTask",
			kind:   "userTask",
			keys:   map[string]any{"outcomes": []any{"approve", "reject"}},
			assert: acceptedKey,
		},
		{
			name:   "signal_name on an intermediateCatchEvent",
			kind:   "intermediateCatchEvent",
			keys:   map[string]any{"signal_name": "paid"},
			assert: acceptedKey,
		},
		{
			name:   "compensate_ref and compensate_scope_local on a compensationThrowEvent",
			kind:   "compensationThrowEvent",
			keys:   map[string]any{"compensate_ref": "refund-ref", "compensate_scope_local": true},
			assert: acceptedKey,
		},
		{
			name:   "validation on a userTask",
			kind:   "userTask",
			keys:   map[string]any{"validation": map[string]any{"kind": "expr", "schema": "true"}},
			assert: acceptedKey,
		},
		{
			// end_behavior/termination_* are a closed vocabulary; a probe value
			// outside it changes nothing, so a read-set derived with an arbitrary
			// string would conclude endEvent reads nothing and refuse this.
			name:   "end_behavior terminate with its termination keys on an endEvent",
			kind:   "endEvent",
			keys:   map[string]any{"end_behavior": "terminate", "termination_reason": "cancelled", "termination_outcome": "abort"},
			assert: acceptedKey,
		},
		{
			// error_code is CONDITIONAL: legal on an endEvent only alongside
			// end_behavior:"error". The gate is kind-level, so it must accept the
			// combination; the bare-error_code case belongs to #193.
			name:   "end_behavior error with error_code on an endEvent",
			kind:   "endEvent",
			keys:   map[string]any{"end_behavior": "error", "error_code": "E_BOOM"},
			assert: acceptedKey,
		},
		{
			name:   "error_code on a boundaryEvent",
			kind:   "boundaryEvent",
			keys:   map[string]any{"attached_to": "svc", "error_code": "E_BOOM"},
			assert: acceptedKey,
		},
		{
			name:     "timer_trigger on an intermediateCatchEvent",
			kind:     "intermediateCatchEvent",
			keys:     map[string]any{"timer_trigger": map[string]any{"kind": "expr", "expr": "PT1H"}},
			yamlSkip: true,
			assert:   acceptedKey,
		},
	}
}

// kindGateDoc renders one case as the single definition document both decoders
// accept. definitionYAML declares the same top-level tags as definitionWire, so
// one shape serves both and the two halves cannot drift into testing different
// fixtures.
func kindGateDoc(tc kindGateCase) map[string]any {
	node := map[string]any{"id": "n1", "kind": tc.kind}
	maps.Copy(node, tc.keys)
	return map[string]any{
		"id": "d", "version": 1,
		"nodes": []any{node},
		"flows": []any{},
	}
}

// TestKindGateRefusesAKeyTheKindCannotCarry drives every case through BOTH
// decoders. Both end in fromWire, so this is what proves the ONE gate covers
// them rather than two gates that can drift apart.
func TestKindGateRefusesAKeyTheKindCannotCarry(t *testing.T) {
	t.Parallel()

	decoders := []struct {
		name string
		// skipYAMLOnly marks the decoder that cannot express a JSON-only key.
		jsonOnly bool
		run      func(t *testing.T, tc kindGateCase) error
	}{
		{
			name:     "json",
			jsonOnly: true,
			run: func(t *testing.T, tc kindGateCase) error {
				raw, err := json.Marshal(kindGateDoc(tc))
				require.NoError(t, err, "fixture must marshal")
				requireFixtureCarriesKeys(t, string(raw), tc)

				var def model.ProcessDefinition
				return json.Unmarshal(raw, &def)
			},
		},
		{
			name: "yaml",
			run: func(t *testing.T, tc kindGateCase) error {
				raw, err := yaml.Marshal(kindGateDoc(tc))
				require.NoError(t, err, "fixture must marshal")
				requireFixtureCarriesKeys(t, string(raw), tc)

				_, err = model.ParseYAML(strings.NewReader(string(raw)))
				return err
			},
		},
	}

	for _, dec := range decoders {
		for _, tc := range kindGateCases() {
			if tc.yamlSkip && !dec.jsonOnly {
				continue
			}
			t.Run(dec.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				tc.assert(t, dec.run(t, tc))
			})
		}
	}
}

// requireFixtureCarriesKeys guards the fixture, not just the assertion: a key
// that never reached the document would make every refusal case pass for the
// wrong reason.
func requireFixtureCarriesKeys(t *testing.T, doc string, tc kindGateCase) {
	t.Helper()
	for k := range tc.keys {
		require.Contains(t, doc, k, "fixture lost the key under test")
	}
}

// TestKindGateKeepsTheLegacyTimerRelocation is the discrimination proof behind
// the accept row above: timer_duration on an intermediateCatchEvent is not
// merely tolerated, it is READ — ReadTrigger relocates it into the canonical
// nested TimerTrigger. A gate comparing the round trip field-by-field would see
// timer_duration vanish and refuse it; the information was moved, not lost.
func TestKindGateKeepsTheLegacyTimerRelocation(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"id":"d","version":1,"nodes":[{"id":"n1","kind":"intermediateCatchEvent","timer_duration":"PT1H"}],"flows":[]}`)

	var def model.ProcessDefinition
	require.NoError(t, json.Unmarshal(raw, &def))
	require.Len(t, def.Nodes, 1)

	back, err := json.Marshal(def)
	require.NoError(t, err)
	assert.Contains(t, string(back), `"timer_trigger"`, "the legacy flat form must normalise into the nested trigger")
	assert.NotContains(t, string(back), `"timer_duration"`, "ToWire never writes the legacy flat form")

	// And the normalised output must itself survive the gate — otherwise the
	// library would refuse what it just produced.
	var again model.ProcessDefinition
	require.NoError(t, json.Unmarshal(back, &again))
}

// TestKindGateReachesIntoANestedSubprocess is the depth half of the safety
// argument. TestNoWireOutputIsRefusedByTheKindGate fills Subprocess from the
// generic pointer probe — a pointer to an EMPTY definition — so it never
// exercises a nested definition with nodes in it, and its doc comment should not
// be read as depth coverage.
//
// Depth is covered instead by recursion: ProcessDefinition.Subprocess is a
// *ProcessDefinition with its own UnmarshalJSON on the JSON side, and
// coreFromYAML re-enters fromNodeYAML on the YAML side. Both therefore re-run
// the gate at every level. This pins that in both directions — a legal inner
// node survives, a misplaced key on an inner node is refused — because a gate
// that only reached the top level would pass every other test in this file.
func TestKindGateReachesIntoANestedSubprocess(t *testing.T) {
	t.Parallel()

	inner := func(extra string) string {
		return `{"id":"outer","version":1,"nodes":[{"id":"sub","kind":"subProcess","subprocess":{` +
			`"id":"inner","version":1,"nodes":[{"id":"i_start","kind":"startEvent"},` +
			`{"id":"i_end","kind":"endEvent"` + extra + `}],` +
			`"flows":[{"id":"if1","source":"i_start","target":"i_end"}]}}],"flows":[]}`
	}

	t.Run("a legal inner node survives", func(t *testing.T) {
		t.Parallel()

		var def model.ProcessDefinition
		require.NoError(t, json.Unmarshal([]byte(inner(`,"end_behavior":"terminate"`)), &def))
		require.Len(t, def.Nodes, 1)
	})

	t.Run("a misplaced key on an inner node is refused", func(t *testing.T) {
		t.Parallel()

		var def model.ProcessDefinition
		err := json.Unmarshal([]byte(inner(`,"action":"charge"`)), &def)
		require.ErrorIs(t, err, model.ErrKeyNotOnKind)
		// The diagnostic names the INNER node, which is the only way an author
		// finds the line: "i_end", not the enclosing "sub".
		assert.Contains(t, err.Error(), "i_end")
		assert.Contains(t, err.Error(), "action")
	})

	t.Run("YAML reaches the same depth", func(t *testing.T) {
		t.Parallel()

		src := `
id: outer
version: 1
nodes:
  - id: sub
    kind: subProcess
    subprocess:
      id: inner
      version: 1
      nodes:
        - id: i_start
          kind: startEvent
        - id: i_end
          kind: endEvent
          action: charge
      flows:
        - id: if1
          source: i_start
          target: i_end
flows: []
`
		_, err := model.ParseYAML(strings.NewReader(src))
		require.ErrorIs(t, err, model.ErrKeyNotOnKind)
		assert.Contains(t, err.Error(), "i_end")
	})
}
