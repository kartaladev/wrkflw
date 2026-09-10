package model_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
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
//
// Each decoder returns the definition as well as the error, so an accepting row
// can assert what was accepted. An accept that checks only NoError is an
// at-limit pin that does not pin: it would stay green if the value it accepted
// were quietly collapsed on the way through, which is the exact failure this
// gate exists to stop.
type endOutcomeDecoder struct {
	name   string
	decode func(t *testing.T, outcome string) (*model.ProcessDefinition, error)
}

var endOutcomeDecoders = []endOutcomeDecoder{
	{
		name: "json",
		decode: func(t *testing.T, outcome string) (*model.ProcessDefinition, error) {
			t.Helper()
			raw := `{"id":"p","version":1,"nodes":[` +
				`{"id":"s","kind":"startEvent"},` +
				`{"id":"e","kind":"endEvent","end_behavior":"terminate","termination_outcome":` +
				strconv.Quote(outcome) + `}],` +
				`"flows":[{"id":"f1","source":"s","target":"e"}]}`
			var def model.ProcessDefinition
			if err := json.Unmarshal([]byte(raw), &def); err != nil {
				return nil, err
			}
			return &def, nil
		},
	},
	{
		name: "yaml",
		decode: func(t *testing.T, outcome string) (*model.ProcessDefinition, error) {
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
				return nil, err
			}
			return ld.Build()
		},
	},
}

// declaredOutcomeNames enumerates every name event.TerminationOutcome.String()
// produces for a DECLARED member, deriving where to stop from the type instead
// of from a literal bound.
//
// TerminationOutcome is a contiguous iota enum, so member k sits at ordinal k
// and the first ordinal past the last member falls through String()'s default
// arm to the ZERO member's name. Scanning until that repeat therefore
// enumerates the members exactly and stays correct when a member is added —
// where a hard-coded bound would silently stop covering the type it is meant to
// track. The cap is not that bound: it is a liveness guard that FAILS rather
// than truncating, so a String() that never falls back cannot turn this into an
// infinite loop or, worse, into a quiet undercount.
func declaredOutcomeNames(t *testing.T) map[string]event.TerminationOutcome {
	t.Helper()

	const cap = 1024
	zero := event.TerminationOutcome(0).String()
	names := map[string]event.TerminationOutcome{}
	for i := range cap {
		o := event.TerminationOutcome(i)
		if i > 0 && o.String() == zero {
			require.NotEmpty(t, names, "the scan enumerated nothing")
			return names
		}
		names[o.String()] = o
	}
	t.Fatalf("TerminationOutcome.String() never fell back to %q within %d ordinals, so the "+
		"declared members cannot be enumerated this way any more", zero, cap)
	return nil
}

// TestTerminationOutcomeVocabularyAtDecode pins the value gate on the one key
// it covers. The accepting rows are not padding: the gate's whole job is to
// separate the two exact names from everything else, and a gate that refused
// every termination_outcome would satisfy the refusing rows alone.
func TestTerminationOutcomeVocabularyAtDecode(t *testing.T) {
	t.Parallel()

	// acceptedAs asserts not merely that the value decoded but that it decoded
	// to the outcome it names. Applied to both decoders, it is what stops a YAML
	// row passing on NoError alone.
	acceptedAs := func(want event.TerminationOutcome) func(*testing.T, *model.ProcessDefinition, error) {
		return func(t *testing.T, def *model.ProcessDefinition, err error) {
			t.Helper()
			require.NoError(t, err)
			require.NotNil(t, def)
			require.Len(t, def.Nodes, 2)
			end, ok := def.Nodes[1].(event.EndEvent)
			require.True(t, ok, "the second node must be the end event")
			assert.Equal(t, event.EndTerminate, end.Behavior)
			assert.Equal(t, want, end.Outcome, "the accepted value must survive the decode as itself")
		}
	}
	refused := func(t *testing.T, _ *model.ProcessDefinition, err error) {
		t.Helper()
		require.ErrorIs(t, err, model.ErrInvalidTerminationOutcome)
	}

	type testCase struct {
		name    string
		outcome string
		assert  func(t *testing.T, def *model.ProcessDefinition, err error)
	}

	cases := []testCase{
		{name: "abort", outcome: "abort", assert: acceptedAs(event.OutcomeAbort)},
		{name: "complete", outcome: "complete", assert: acceptedAs(event.OutcomeComplete)},
		{
			// The wire cannot tell an authored empty value from an absent key,
			// and it does not have to: an unauthored outcome is legal.
			name: "empty is the unauthored outcome", outcome: "", assert: acceptedAs(event.OutcomeComplete),
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
			assert: func(t *testing.T, _ *model.ProcessDefinition, err error) {
				t.Helper()
				require.ErrorIs(t, err, model.ErrInvalidTerminationOutcome)
				msg := err.Error()
				assert.Contains(t, msg, `"e"`, "the diagnostic must name the offending node")
				assert.Contains(t, msg, `"abbort"`, "the diagnostic must quote the value as authored")
				assert.Contains(t, msg, "abort", "the diagnostic must show what was allowed")
				assert.Contains(t, msg, "complete", "the diagnostic must show what was allowed")
				assert.Contains(t, msg, "end_behavior", "the diagnostic must name the key the outcome depends on")
				assert.Contains(t, msg, "terminate", "the diagnostic must name the value that key must take")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, d := range endOutcomeDecoders {
				t.Run(d.name, func(t *testing.T) {
					t.Parallel()

					def, err := d.decode(t, tc.outcome)
					tc.assert(t, def, err)
				})
			}
		})
	}
}

// TestTerminationOutcomeVocabularyMatchesTheType is the drift pin the gate's
// comment promises: SET EQUALITY between the names spelled by hand in
// node_wire_vocabulary.go and the image of event.TerminationOutcome.String().
//
// It is the only assertion that catches drift in BOTH directions at once, and
// it can only live here. definition/model cannot import definition/event (event
// imports model, and the dependency runs one way), so the gate's set is
// unexported and the type is out of reach from inside the package; this test
// sees the set through export_test.go and the type through a normal import.
//
// ElementsMatch rather than Equal: the gate's order decides only how the
// vocabulary is rendered in a diagnostic, not what it accepts, so a reordering
// is not drift and must not fail here.
func TestTerminationOutcomeVocabularyMatchesTheType(t *testing.T) {
	t.Parallel()

	names := declaredOutcomeNames(t)
	assert.ElementsMatch(t, model.TerminationOutcomes, slices.Collect(maps.Keys(names)),
		"the gate's hand-written vocabulary has drifted from the type that owns the names")
}

// TestEveryTerminationOutcomeNameStaysDecodable carries each declared name all
// the way through the decoder to the reconstructed node.
//
// Set equality alone does not cover this. It compares two lists of strings; it
// cannot see that a name still reaches the leaf spec and still denotes the
// member it is the name of. This is the end of that chain, and the Contains
// checks are the known-positive control: without them, a scan that somehow saw
// nothing would report green over an empty set.
func TestEveryTerminationOutcomeNameStaysDecodable(t *testing.T) {
	t.Parallel()

	names := declaredOutcomeNames(t)
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

// TestTheKindGateRunsBeforeTheValueGate holds the one property the two gates'
// ordering decides, and it is the only thing in the tree that holds it:
// measured, swapping the two calls in fromWire leaves every other test green.
//
// The discriminating input has to make BOTH gates want to refuse. A serviceTask
// never reads termination_outcome, so the kind gate refuses it whatever its
// value; giving it a value outside the vocabulary is what makes the value gate
// want it too, and only then does the order decide which error the author sees.
// A serviceTask carrying the LEGAL "abort" would not discriminate — the value
// gate passes it through, so both orderings report ErrKeyNotOnKind — which is
// why that case is pinned below as the control rather than as the subject.
func TestTheKindGateRunsBeforeTheValueGate(t *testing.T) {
	t.Parallel()

	decode := func(t *testing.T, outcome string) error {
		t.Helper()
		raw := `{"id":"p","version":1,"nodes":[{"id":"t","kind":"serviceTask",` +
			`"termination_outcome":` + strconv.Quote(outcome) + `}],"flows":[]}`
		var def model.ProcessDefinition
		return json.Unmarshal([]byte(raw), &def)
	}

	t.Run("an out-of-vocabulary value on a kind that cannot carry the key reports the KEY", func(t *testing.T) {
		t.Parallel()

		err := decode(t, "Abort")
		require.ErrorIs(t, err, model.ErrKeyNotOnKind,
			"both gates have grounds here; the kind gate must run first, because the problem is the key")
		require.NotErrorIs(t, err, model.ErrInvalidTerminationOutcome,
			"telling this author their VALUE is wrong points them at the wrong half of the node")
	})

	t.Run("control: the legal value on the same kind cannot tell the orderings apart", func(t *testing.T) {
		t.Parallel()

		require.ErrorIs(t, decode(t, "abort"), model.ErrKeyNotOnKind,
			"the value gate accepts this, so either ordering reports the key; it is here to show "+
				"the row above is discriminating and this one is not")
	})
}

// TestTheDiagnosticsStatedPreconditionIsTrue pins the clause the refusal message
// adds beyond the vocabulary — that termination_outcome is read only alongside
// end_behavior "terminate".
//
// A diagnostic that instructs is a claim about behaviour, and this repository
// has paid for comments that claimed more than the code delivered. So both
// halves are asserted here: that the message states the precondition, and that
// the precondition is what the decoder actually does. If the combination gate
// is ever built (it is deliberately not built here), the third case below is
// the one that changes, and it will say so by failing.
func TestTheDiagnosticsStatedPreconditionIsTrue(t *testing.T) {
	t.Parallel()

	decode := func(t *testing.T, node string) (*model.ProcessDefinition, error) {
		t.Helper()
		raw := `{"id":"p","version":1,"nodes":[` + node + `],"flows":[]}`
		var def model.ProcessDefinition
		if err := json.Unmarshal([]byte(raw), &def); err != nil {
			return nil, err
		}
		return &def, nil
	}
	remarshal := func(t *testing.T, def *model.ProcessDefinition) string {
		t.Helper()
		b, err := json.Marshal(def)
		require.NoError(t, err)
		return string(b)
	}

	t.Run("the message names the precondition", func(t *testing.T) {
		t.Parallel()

		_, err := decode(t, `{"id":"e","kind":"endEvent","termination_outcome":"Abort"}`)
		require.ErrorIs(t, err, model.ErrInvalidTerminationOutcome)
		assert.Contains(t, err.Error(), `end_behavior "terminate"`,
			"without this clause the message's only instruction is the vocabulary, and obeying "+
				"the vocabulary alone lands the author in the silent drop pinned below")
	})

	t.Run("with the precondition the value survives", func(t *testing.T) {
		t.Parallel()

		def, err := decode(t, `{"id":"e","kind":"endEvent","end_behavior":"terminate","termination_outcome":"abort"}`)
		require.NoError(t, err)
		assert.Contains(t, remarshal(t, def), `"termination_outcome":"abort"`,
			"the authored value must reach the stored definition")
	})

	t.Run("without it the value is accepted and dropped", func(t *testing.T) {
		t.Parallel()

		def, err := decode(t, `{"id":"e","kind":"endEvent","termination_outcome":"abort"}`)
		require.NoError(t, err,
			"this collapse belongs to the combination family and is deliberately NOT refused here")
		assert.NotContains(t, remarshal(t, def), "termination_outcome",
			"the drop is what the message's precondition clause exists to warn about; if this "+
				"ever starts surviving, the clause is stale and must be rewritten")
	})
}
