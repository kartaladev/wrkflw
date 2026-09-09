package model_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/kartaladev/wrkflw/definition/activity"
	_ "github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
)

// refusesShape is the one refusal contract for both codec tables: the error must
// carry ErrInvalidRule and the value must be left zero, so a refusal can never
// half-apply.
func refusesShape(t *testing.T, rule model.RuleSpec, err error) {
	t.Helper()
	require.Error(t, err)
	require.ErrorIs(t, err, model.ErrInvalidRule)
	assert.True(t, rule.IsZero(), "a refused rule must leave the value zero")
}

// TestRuleSpecUnmarshalJSON pins the two shapes a reserved `rule` may take and
// refuses every other JSON type. A RuleSpec is either a catalog NAME (a string,
// resolved like an action name) or an INLINE rule document (an object, carried
// verbatim and interpreted by nothing in wrkflw).
//
// Accepts and refuses sit in ONE table on purpose: an UnmarshalJSON that
// rejected everything would satisfy each "refused" row exactly as well as a
// correct one, so the string and object rows are the at-limit accepts that make
// the refusals mean something.
func TestRuleSpecUnmarshalJSON(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		json   string
		assert func(t *testing.T, rule model.RuleSpec, err error)
	}

	cases := []testCase{
		{
			name: "a string is a catalog name",
			json: `"pricing.v3"`,
			assert: func(t *testing.T, rule model.RuleSpec, err error) {
				require.NoError(t, err)
				assert.Equal(t, "pricing.v3", rule.Name)
				assert.Empty(t, rule.Inline)
				assert.False(t, rule.IsZero())
			},
		},
		{
			name: "an object is an inline document, carried verbatim",
			json: `{"stages":[{"when":"amount > 100","then":"review"}]}`,
			assert: func(t *testing.T, rule model.RuleSpec, err error) {
				require.NoError(t, err)
				assert.Empty(t, rule.Name)
				assert.JSONEq(t, `{"stages":[{"when":"amount > 100","then":"review"}]}`, string(rule.Inline))
				assert.False(t, rule.IsZero())
			},
		},
		{
			name: "an empty object is still an inline document",
			json: `{}`,
			assert: func(t *testing.T, rule model.RuleSpec, err error) {
				require.NoError(t, err)
				assert.JSONEq(t, `{}`, string(rule.Inline))
				assert.False(t, rule.IsZero(), "an authored empty document is not an absent rule")
			},
		},
		{name: "a number is refused", json: `42`, assert: refusesShape},
		{name: "a bool is refused", json: `true`, assert: refusesShape},
		{name: "an array is refused", json: `["pricing.v3"]`, assert: refusesShape},
		{name: "null is refused", json: `null`, assert: refusesShape},
		{
			// Fails CLOSED: a blank name would be indistinguishable from an absent
			// rule, so `rule: ""` would silently validate as "no rule" and the
			// author's mistake would never be reported.
			name: "a blank name is refused", json: `"   "`, assert: refusesShape,
		},
		{name: "an empty name is refused", json: `""`, assert: refusesShape},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var rule model.RuleSpec
			err := json.Unmarshal([]byte(tc.json), &rule)
			tc.assert(t, rule, err)
		})
	}
}

// TestRuleSpecUnmarshalYAML is the YAML half of TestRuleSpecUnmarshalJSON. The
// same two shapes are accepted and the same four types refused, this time
// discriminated by YAML tag rather than by leading byte — so `rule: "true"` is a
// catalog name while `rule: true` is not.
func TestRuleSpecUnmarshalYAML(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		yaml   string
		assert func(t *testing.T, rule model.RuleSpec, err error)
	}

	cases := []testCase{
		{
			name: "a plain scalar is a catalog name",
			yaml: "pricing.v3",
			assert: func(t *testing.T, rule model.RuleSpec, err error) {
				require.NoError(t, err)
				assert.Equal(t, "pricing.v3", rule.Name)
				assert.Empty(t, rule.Inline)
			},
		},
		{
			name: "a quoted bool-looking scalar is still a name",
			yaml: `"true"`,
			assert: func(t *testing.T, rule model.RuleSpec, err error) {
				require.NoError(t, err)
				assert.Equal(t, "true", rule.Name)
			},
		},
		{
			name: "a mapping is an inline document",
			yaml: "stages:\n  - when: amount > 100\n    then: review\n",
			assert: func(t *testing.T, rule model.RuleSpec, err error) {
				require.NoError(t, err)
				assert.Empty(t, rule.Name)
				assert.JSONEq(t, `{"stages":[{"when":"amount > 100","then":"review"}]}`, string(rule.Inline))
			},
		},
		{name: "an int is refused", yaml: "42", assert: refusesShape},
		{name: "a bool is refused", yaml: "true", assert: refusesShape},
		{name: "a sequence is refused", yaml: "- pricing.v3", assert: refusesShape},
		{
			// MEASURED, not assumed: yaml.v3 handles a null node itself and never
			// calls UnmarshalYAML, exactly as encoding/json nils a *RuleSpec field
			// for "rule":null. So an explicit null means ABSENT in both formats.
			// That loses nothing — there is no rule in a null to lose — and
			// TestNodeRuleNullMeansAbsent pins it at the node level, where it is
			// the property that actually matters.
			name: "an explicit null decodes as absent, in both formats",
			yaml: "~",
			assert: func(t *testing.T, rule model.RuleSpec, err error) {
				require.NoError(t, err)
				assert.True(t, rule.IsZero())
			},
		},
		{name: "a blank name is refused", yaml: `"   "`, assert: refusesShape},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var rule model.RuleSpec
			err := yaml.Unmarshal([]byte(tc.yaml), &rule)
			tc.assert(t, rule, err)
		})
	}
}

// TestRuleSpecMarshalJSON pins the encode half: each accepted shape re-emits as
// the shape it was authored in, and a zero spec is refused rather than encoded as
// a lie. See NodeWire.Rule for why the field is a pointer.
func TestRuleSpecMarshalJSON(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		rule   model.RuleSpec
		assert func(t *testing.T, out []byte, err error)
	}

	cases := []testCase{
		{
			name: "a catalog name re-emits as a string",
			rule: model.RuleSpec{Name: "pricing.v3"},
			assert: func(t *testing.T, out []byte, err error) {
				require.NoError(t, err)
				assert.JSONEq(t, `"pricing.v3"`, string(out))
			},
		},
		{
			name: "an inline document re-emits as the object",
			rule: model.RuleSpec{Inline: json.RawMessage(`{"stages":[]}`)},
			assert: func(t *testing.T, out []byte, err error) {
				require.NoError(t, err)
				assert.JSONEq(t, `{"stages":[]}`, string(out))
			},
		},
		{
			name: "a zero RuleSpec is refused rather than emitted as a lie",
			rule: model.RuleSpec{},
			assert: func(t *testing.T, _ []byte, err error) {
				require.Error(t, err)
				require.ErrorIs(t, err, model.ErrInvalidRule)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := json.Marshal(tc.rule)
			tc.assert(t, out, err)
		})
	}
}

// TestBusinessRuleTaskRuleRoundTrip pins the reserved key through the PERSISTED
// seam: raw JSON into a ProcessDefinition and back out, which is what
// DefinitionStore does with a stored blob. That round-trip is the whole reason the
// key is reserved now, and this test is where it is guarded — `rule` cannot live in
// strict_decoding_test.go's allFieldsYAML fixture, because that fixture must Build
// cleanly and a rule is refused (see refusedYAMLTags).
//
// TestBusinessRuleTaskRuleOptions in definition/activity owns the other seam,
// option -> field; it does not re-assert these wire strings.
func TestBusinessRuleTaskRuleRoundTrip(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		json   string
		assert func(t *testing.T, def model.ProcessDefinition, reencoded string, err error)
	}

	const tmpl = `{"id":"d","version":1,"nodes":[{"id":"score","kind":"businessRuleTask","name":"Score"%s}],"flows":null}`

	cases := []testCase{
		{
			name: "a catalog name round-trips",
			json: `,"rule":"pricing.v3"`,
			assert: func(t *testing.T, def model.ProcessDefinition, reencoded string, err error) {
				require.NoError(t, err)
				assert.Contains(t, reencoded, `"rule":"pricing.v3"`)
			},
		},
		{
			name: "an inline document round-trips",
			json: `,"rule":{"stages":[]}`,
			assert: func(t *testing.T, def model.ProcessDefinition, reencoded string, err error) {
				require.NoError(t, err)
				assert.Contains(t, reencoded, `"rule":{"stages":[]}`)
			},
		},
		{
			name: "no rule emits no rule key",
			json: ``,
			assert: func(t *testing.T, def model.ProcessDefinition, reencoded string, err error) {
				require.NoError(t, err)
				assert.NotContains(t, reencoded, `"rule"`)
			},
		},
		{
			name: "a number rule is refused at decode",
			json: `,"rule":42`,
			assert: func(t *testing.T, _ model.ProcessDefinition, _ string, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, model.ErrInvalidRule)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			src := []byte(fmt.Sprintf(tmpl, tc.json))
			var def model.ProcessDefinition
			if err := json.Unmarshal(src, &def); err != nil {
				tc.assert(t, def, "", err)
				return
			}
			out, err := json.Marshal(def)
			require.NoError(t, err)
			tc.assert(t, def, string(out), nil)
		})
	}
}

// TestNodeRuleNullMeansAbsent pins the one shape neither codec refuses: an
// explicit null `rule`. Both decoders handle null themselves — encoding/json
// nils the *RuleSpec field, yaml.v3 leaves the node undecoded — so a node
// written with `"rule":null` or `rule: ~` carries NO rule and validates exactly
// as one that omitted the key. Nothing is lost, and pinning it here means the
// behaviour is a decision on the record rather than an accident.
func TestNodeRuleNullMeansAbsent(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name  string
		parse func(t *testing.T) (model.ProcessDefinition, error)
	}

	cases := []testCase{
		{
			name: "JSON",
			parse: func(t *testing.T) (model.ProcessDefinition, error) {
				const src = `{"id":"d","version":1,"nodes":[{"id":"score","kind":"businessRuleTask","rule":null}],"flows":null}`
				var def model.ProcessDefinition
				err := json.Unmarshal([]byte(src), &def)
				return def, err
			},
		},
		{
			name: "YAML",
			parse: func(t *testing.T) (model.ProcessDefinition, error) {
				const src = "id: d\nversion: 1\nnodes:\n  - id: score\n    kind: businessRuleTask\n    rule: ~\n"
				ld, err := model.ParseYAML(strings.NewReader(src))
				if err != nil {
					return model.ProcessDefinition{}, err
				}
				def, err := ld.Build()
				if err != nil {
					return model.ProcessDefinition{}, err
				}
				return *def, nil
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def, err := tc.parse(t)
			// Build validates, so a YAML definition of one orphan node fails on
			// structure. Only the rule matters here: whatever else is reported, it
			// must not be ErrInvalidRule or ErrRuleNotSupported.
			assert.NotErrorIs(t, err, model.ErrInvalidRule)
			assert.NotErrorIs(t, err, model.ErrRuleNotSupported)
			if err == nil {
				require.Len(t, def.Nodes, 1)
				out, mErr := json.Marshal(def)
				require.NoError(t, mErr)
				assert.NotContains(t, string(out), `"rule"`, "a null rule must not be re-emitted")
			}
		})
	}
}

// TestRuleSpecCodecRejectsMalformedInput reaches the branches no DECODER can
// reach. Both `encoding/json` and yaml.v3 syntax-check a document before handing a
// value to an Unmarshaler, so within this package `UnmarshalJSON` never sees empty
// or malformed bytes. But it is exported behaviour on an exported type: a caller
// may invoke it directly with anything, and a nil-slice index would panic where an
// error is the right answer.
//
// The reviews that flagged those branches as dead were right that no decoder
// reaches them; the answer is to cover them, not to delete the guards and let an
// exported method panic. Every row below fails CLOSED.
func TestRuleSpecCodecRejectsMalformedInput(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		decode func(rule *model.RuleSpec) error
		reason string
	}

	cases := []testCase{
		{
			name:   "JSON: empty input does not panic",
			decode: func(rule *model.RuleSpec) error { return rule.UnmarshalJSON(nil) },
			reason: "nothing",
		},
		{
			name:   "JSON: an unterminated string",
			decode: func(rule *model.RuleSpec) error { return rule.UnmarshalJSON([]byte(`"abc`)) },
			reason: "unexpected end of JSON input",
		},
		{
			name:   "JSON: a truncated object",
			decode: func(rule *model.RuleSpec) error { return rule.UnmarshalJSON([]byte(`{"a":`)) },
			reason: "not valid JSON",
		},
		{
			name:   "JSON: an unrecognised token is not called a number",
			decode: func(rule *model.RuleSpec) error { return rule.UnmarshalJSON([]byte(`xyz`)) },
			reason: "an unrecognised value",
		},
		{
			// S4: the null arm matches the 4-byte token, not every n-initial byte.
			// It used to name `not`, `nan`, `nil`, `none` and `no` all "null", which
			// is confidently wrong and made the default arm unreachable, contradicting
			// that arm's own rationale.
			name:   "JSON: an n-initial token is not called null",
			decode: func(rule *model.RuleSpec) error { return rule.UnmarshalJSON([]byte(`not`)) },
			reason: "an unrecognised value",
		},
		{
			// A COMPLEX key (a sequence or mapping used as a key). An int key does
			// NOT reach here — yaml.v3 coerces `1:` to the string "1" — which is why
			// this row uses `? [a, b]` and why the field is map[string]any.
			name: "YAML: a complex mapping key",
			decode: func(rule *model.RuleSpec) error {
				return yaml.Unmarshal([]byte("? [a, b]\n: review\n"), rule)
			},
			reason: "unmarshal errors",
		},
		{
			name: "YAML: a nested non-string mapping key is not JSON-encodable",
			decode: func(rule *model.RuleSpec) error {
				return yaml.Unmarshal([]byte("stages:\n  1: review\n"), rule)
			},
			reason: "unsupported type",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var rule model.RuleSpec
			err := tc.decode(&rule)
			require.Error(t, err)
			require.ErrorIs(t, err, model.ErrInvalidRule)
			assert.True(t, rule.IsZero(), "a refused rule must leave the value zero")
			assert.ErrorContains(t, err, tc.reason, "the message must say what was wrong")
		})
	}
}

// TestRuleSpecYAMLInlineConversionIsLossy enforces the LIMIT documented on
// RuleSpec.Inline instead of leaving it as prose. A YAML-authored inline document
// is decoded into map[string]any and re-encoded as JSON, so yaml.v3's scalar
// resolution is applied on the way through: the document that reaches the rule
// engine is not byte-identical to the one that was typed, and in two rows below it
// is not information-preserving either.
//
// An earlier version of that doc comment claimed key order was the only casualty
// and the CONTENT was untouched. A security review measured otherwise. Pinning the
// transformations here means the claim cannot drift back to a falsehood, and means
// a yaml.v3 upgrade that changes any of them fails the build rather than silently
// changing what a rule means.
//
// This is a documented limit, NOT a deferred defect: wrkflw never interprets the
// document, so it cannot warn; changing yaml.v3's resolution is out of scope and
// would itself be a wire-format change. The honest move is to say what it does.
func TestRuleSpecYAMLInlineConversionIsLossy(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		yaml       string
		wantInline string
		why        string
	}

	cases := []testCase{
		{
			name:       "a null key is dropped entirely",
			yaml:       "~: v\n",
			wantInline: `{}`,
			why:        "INFORMATION LOST: the entry disappears with no diagnostic",
		},
		{
			name:       "an integer too wide for float64 loses precision",
			yaml:       "x: 123456789012345678901234567890\n",
			wantInline: `{"x":1.2345678901234568e+29}`,
			why:        "INFORMATION LOST: yaml.v3 resolves it to float64",
		},
		{
			name:       "a date becomes an RFC3339 string",
			yaml:       "x: 2020-01-01\n",
			wantInline: `{"x":"2020-01-01T00:00:00Z"}`,
			why:        "reshaped, not lost",
		},
		{
			name:       "binary is base64-decoded to raw bytes",
			yaml:       "x: !!binary aGVsbG8=\n",
			wantInline: `{"x":"hello"}`,
			why:        "reshaped, not lost",
		},
		{
			name:       "an octal literal becomes its decimal value",
			yaml:       "x: 0o17\n",
			wantInline: `{"x":15}`,
			why:        "reshaped, not lost",
		},
		{
			name:       "a non-string key is stringified",
			yaml:       "1: one\n",
			wantInline: `{"1":"one"}`,
			why:        "reshaped; this is also why the field decodes into map[string]any",
		},
		{
			name:       "keys are re-ordered into JSON canonical order",
			yaml:       "z: 1\na: 2\n",
			wantInline: `{"a":2,"z":1}`,
			why:        "order lost; nothing downstream depends on it",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var rule model.RuleSpec
			require.NoError(t, yaml.Unmarshal([]byte(tc.yaml), &rule))
			assert.Equal(t, tc.wantInline, string(rule.Inline), tc.why)
		})
	}
}
