package model_test

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
)

// decodeEndOutcome decodes a minimal start→terminate-end definition whose end
// node carries the given termination_outcome, once per decoder, and returns
// what each refused. Both decoders funnel through fromWire, so running both is
// how the "one gate, two decoders" claim in node_wire_keys.go is checked rather
// than assumed.
type endOutcomeDecoder struct {
	name   string
	decode func(t *testing.T, outcome string) error
}

var endOutcomeDecoders = []endOutcomeDecoder{
	{
		name: "json",
		decode: func(t *testing.T, outcome string) error {
			t.Helper()
			raw := `{"id":"p","version":1,"nodes":[` +
				`{"id":"s","kind":"startEvent"},` +
				`{"id":"e","kind":"endEvent","end_behavior":"terminate","termination_outcome":` +
				strconv.Quote(outcome) + `}],` +
				`"flows":[{"id":"f1","source":"s","target":"e"}]}`
			var def model.ProcessDefinition
			return json.Unmarshal([]byte(raw), &def)
		},
	},
	{
		name: "yaml",
		decode: func(t *testing.T, outcome string) error {
			t.Helper()
			const tmpl = `id: p
version: 1
nodes:
  - id: s
    kind: startEvent
  - id: e
    kind: endEvent
    end_behavior: terminate
    termination_outcome: %s
flows:
  - { id: f1, source: s, target: e }
`
			ld, err := model.ParseYAML(strings.NewReader(fmt.Sprintf(tmpl, strconv.Quote(outcome))))
			if err != nil {
				return err
			}
			_, err = ld.Build()
			return err
		},
	},
}

// TestTerminationOutcomeVocabularyAtDecode pins the value gate on the one key
// it covers. The accepting rows are not padding: the gate's whole job is to
// separate the two exact names from everything else, and a gate that refused
// every termination_outcome would satisfy the refusing rows alone.
func TestTerminationOutcomeVocabularyAtDecode(t *testing.T) {
	t.Parallel()

	accepted := func(t *testing.T, err error) {
		t.Helper()
		require.NoError(t, err)
	}
	refused := func(t *testing.T, err error) {
		t.Helper()
		require.ErrorIs(t, err, model.ErrInvalidTerminationOutcome)
	}

	type testCase struct {
		name    string
		outcome string
		assert  func(t *testing.T, err error)
	}

	cases := []testCase{
		{name: "abort", outcome: "abort", assert: accepted},
		{name: "complete", outcome: "complete", assert: accepted},
		{
			// The wire cannot tell an authored empty value from an absent key,
			// and it does not have to: an unauthored outcome is legal.
			name: "empty is the unauthored outcome", outcome: "", assert: accepted,
		},
		{name: "wrong case", outcome: "Abort", assert: refused},
		{name: "shouted", outcome: "COMPLETE", assert: refused},
		{name: "transposed letters", outcome: "abrot", assert: refused},
		{name: "trailing space", outcome: "abort ", assert: refused},
		{
			name: "the diagnostic names the node, the value and the vocabulary",
			// Nothing about this value is special; the row exists because a
			// sentinel alone does not tell an author WHICH node to fix, and the
			// author of a near-miss is by definition looking at the wrong thing.
			outcome: "abbort",
			assert: func(t *testing.T, err error) {
				t.Helper()
				require.ErrorIs(t, err, model.ErrInvalidTerminationOutcome)
				msg := err.Error()
				assert.Contains(t, msg, `"e"`, "the diagnostic must name the offending node")
				assert.Contains(t, msg, `"abbort"`, "the diagnostic must quote the value as authored")
				assert.Contains(t, msg, "abort", "the diagnostic must show what was allowed")
				assert.Contains(t, msg, "complete", "the diagnostic must show what was allowed")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, d := range endOutcomeDecoders {
				t.Run(d.name, func(t *testing.T) {
					t.Parallel()

					tc.assert(t, d.decode(t, tc.outcome))
				})
			}
		})
	}
}

// TestEveryTerminationOutcomeNameStaysDecodable is the drift pin on the gate's
// hand-written vocabulary.
//
// definition/model cannot import definition/event — event imports model, and
// the dependency runs one way only — so the two names the gate accepts are
// spelled there rather than read off the type that owns them. This test is what
// makes that spelling safe: it runs from the external test package, which CAN
// see both, and fails if a name the type produces ever stops decoding.
//
// The scan runs past the declared members on purpose. TerminationOutcome's
// String has a default arm returning "complete", so an out-of-range value can
// only yield a name already in the set and cannot produce a false failure —
// while a newly ADDED member with its own case yields a new name and does. The
// Contains check below is the known-positive control: without it, a scan that
// somehow saw nothing would report green over an empty set.
func TestEveryTerminationOutcomeNameStaysDecodable(t *testing.T) {
	t.Parallel()

	// Lowest ordinal wins: every out-of-range value shares "complete" with
	// OutcomeComplete itself, and it is the declared member the name denotes.
	names := map[string]event.TerminationOutcome{}
	for i := range 8 {
		o := event.TerminationOutcome(i)
		if _, seen := names[o.String()]; !seen {
			names[o.String()] = o
		}
	}
	require.Contains(t, names, "abort", "the scan must reach the declared members")
	require.Contains(t, names, "complete", "the scan must reach the declared members")

	for name, want := range names {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			raw := `{"id":"p","version":1,"nodes":[` +
				`{"id":"s","kind":"startEvent"},` +
				`{"id":"e","kind":"endEvent","end_behavior":"terminate","termination_outcome":` +
				strconv.Quote(name) + `}],` +
				`"flows":[{"id":"f1","source":"s","target":"e"}]}`
			var def model.ProcessDefinition
			require.NoError(t, json.Unmarshal([]byte(raw), &def),
				"a name the outcome type writes must decode; the gate's vocabulary has drifted from the type")

			end, ok := def.Nodes[1].(event.EndEvent)
			require.True(t, ok)
			assert.Equal(t, want, end.Outcome, "the decoded outcome must be the one the name denotes")
		})
	}
}
