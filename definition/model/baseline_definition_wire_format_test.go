package model_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
)

// TestBaselineNodeKindDecodeNamesTheRefusedValue characterizes the
// definition-wire-format scenario "An unknown or retired kind name is refused"
// (openspec/specs/definition-wire-format/spec.md): decoding a kind name outside
// the registered set fails, and the error names the refused value.
func TestBaselineNodeKindDecodeNamesTheRefusedValue(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		wire   string
		assert func(t *testing.T, err error)
	}

	refusedNaming := func(value string) func(t *testing.T, err error) {
		return func(t *testing.T, err error) {
			require.Error(t, err, "decoding %q must fail", value)
			assert.Contains(t, err.Error(), value, "the error must name the refused value")
		}
	}

	cases := []testCase{
		{name: "unknown name", wire: "notANodeKind", assert: refusedNaming("notANodeKind")},
		{name: "retired terminate end name", wire: "terminateEndEvent", assert: refusedNaming("terminateEndEvent")},
		{name: "retired error end name", wire: "errorEndEvent", assert: refusedNaming("errorEndEvent")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got model.NodeKind
			err := json.Unmarshal([]byte(`"`+tc.wire+`"`), &got)
			tc.assert(t, err)
		})
	}
}

// TestBaselineParseYAMLRefusesAnUnknownKindName characterizes the
// definition-wire-format scenario "YAML refuses an unknown kind name": ParseYAML
// itself fails, and the error names the unknown kind.
func TestBaselineParseYAMLRefusesAnUnknownKindName(t *testing.T) {
	t.Parallel()

	const doc = `
id: p
version: 1
nodes:
  - id: s
    kind: startEvent
  - id: x
    kind: notANodeKind
  - id: e
    kind: endEvent
flows:
  - id: f1
    source: s
    target: x
  - id: f2
    source: x
    target: e
`
	_, err := model.ParseYAML(strings.NewReader(doc))
	require.Error(t, err, "ParseYAML must refuse an unknown kind name")
	assert.Contains(t, err.Error(), "notANodeKind", "the error must name the unknown kind")
}
