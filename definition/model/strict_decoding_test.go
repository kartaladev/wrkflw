package model_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/kartaladev/wrkflw/definition/model"
)

const validYAML = `
id: approval-process
version: 1
nodes:
  - id: start
    kind: startEvent
  - id: approve
    kind: userTask
    eligible_roles: ["manager"]
  - id: end
    kind: endEvent
flows:
  - { id: f1, source: start, target: approve }
  - { id: f2, source: approve, target: end }
`

func TestParseYAMLRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		yaml   string
		assert func(t *testing.T, ld model.DefinitionLoader, err error)
	}

	cases := []testCase{
		{
			name: "unknown key at top level",
			yaml: validYAML + "bogus_top_level: 42\n",
			assert: func(t *testing.T, _ model.DefinitionLoader, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "bogus_top_level")
			},
		},
		{
			name: "misspelled eligible_roles on a node",
			yaml: strings.Replace(validYAML, "eligible_roles", "eligable_roles", 1),
			assert: func(t *testing.T, _ model.DefinitionLoader, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "eligable_roles")
			},
		},
		{
			name: "unknown key inside a flow",
			yaml: strings.Replace(validYAML,
				"{ id: f1, source: start, target: approve }",
				"{ id: f1, source: start, target: approve, bogus_flow_key: 1 }", 1),
			assert: func(t *testing.T, _ model.DefinitionLoader, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "bogus_flow_key")
			},
		},
		{
			name: "every unknown key is reported",
			yaml: strings.Replace(validYAML, "eligible_roles", "eligable_roles", 1) +
				"bogus_top_level: 42\n",
			assert: func(t *testing.T, _ model.DefinitionLoader, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "eligable_roles")
				assert.Contains(t, err.Error(), "bogus_top_level")
			},
		},
		{
			name: "valid definition still parses",
			yaml: validYAML,
			assert: func(t *testing.T, ld model.DefinitionLoader, err error) {
				require.NoError(t, err)
				require.NotNil(t, ld)
				def, buildErr := ld.Build()
				require.NoError(t, buildErr)
				assert.Equal(t, "approval-process", def.ID)
				assert.Len(t, def.Nodes, 3)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ld, err := model.ParseYAML(strings.NewReader(tc.yaml))
			tc.assert(t, ld, err)
		})
	}
}

// TestParseYAMLEmptyDocumentIsNotAnError is a regression guard for ParseYAML's
// io.EOF branch, not a RED-first case: yaml.Decoder reports io.EOF where
// yaml.Unmarshal reported nil, so without that branch a strict decoder would
// silently turn empty input into a parse error (ADR-0167 D3a).
func TestParseYAMLEmptyDocumentIsNotAnError(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		yaml   string
		assert func(t *testing.T, ld model.DefinitionLoader, err error)
	}

	cases := []testCase{
		{
			name: "empty string",
			yaml: "",
			assert: func(t *testing.T, ld model.DefinitionLoader, err error) {
				require.NoError(t, err)
				assert.NotNil(t, ld)
			},
		},
		{
			name: "newline only",
			yaml: "\n",
			assert: func(t *testing.T, ld model.DefinitionLoader, err error) {
				require.NoError(t, err)
				assert.NotNil(t, ld)
			},
		},
		{
			name: "comment only",
			yaml: "# nothing here\n",
			assert: func(t *testing.T, ld model.DefinitionLoader, err error) {
				require.NoError(t, err)
				assert.NotNil(t, ld)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ld, err := model.ParseYAML(strings.NewReader(tc.yaml))
			tc.assert(t, ld, err)
		})
	}
}

func TestProcessDefinitionUnmarshalJSONRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	const validJSON = `{"id":"p","version":1,` +
		`"nodes":[{"id":"a","kind":"userTask","eligible_roles":["manager"]}],"flows":[]}`

	type testCase struct {
		name   string
		json   string
		assert func(t *testing.T, def model.ProcessDefinition, err error)
	}

	cases := []testCase{
		{
			name: "unknown key at top level",
			json: `{"id":"p","version":1,"nodes":[],"flows":[],"bogus_top":9}`,
			assert: func(t *testing.T, _ model.ProcessDefinition, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "bogus_top")
			},
		},
		{
			name: "unknown key inside a node",
			json: `{"id":"p","version":1,` +
				`"nodes":[{"id":"a","kind":"userTask","bogus_node_key":1}],"flows":[]}`,
			assert: func(t *testing.T, _ model.ProcessDefinition, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "bogus_node_key")
			},
		},
		{
			name: "misspelled eligible_roles is rejected, not silently dropped",
			json: `{"id":"p","version":1,` +
				`"nodes":[{"id":"a","kind":"userTask","eligable_roles":["manager"]}],"flows":[]}`,
			assert: func(t *testing.T, _ model.ProcessDefinition, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "eligable_roles")
			},
		},
		{
			// json.Unmarshal validates the whole input; Decoder.Decode reads one
			// value and stops. Without an explicit trailing-token check the
			// strictness swap would LOOSEN this, so the guard is part of the
			// change rather than incidental (ADR-0167).
			name: "trailing data after the definition is rejected",
			json: `{"id":"p","version":1,"nodes":[],"flows":[]} trailing garbage`,
			assert: func(t *testing.T, _ model.ProcessDefinition, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "trailing data")
				// Corrupt trailing bytes and a legitimate second value are
				// different debugging problems, so the syntax error naming the
				// offending character is kept rather than collapsed away. This
				// mirrors decodeCursorInto in runtime/kernel/cursorcodec.go,
				// the established strict-decoding path in this repo.
				var syn *json.SyntaxError
				assert.ErrorAs(t, err, &syn, "the underlying syntax error must survive wrapping")
			},
		},
		{
			name: "a second JSON value after the definition is rejected",
			json: `{"id":"p","version":1,"nodes":[],"flows":[]} {"id":"q"}`,
			assert: func(t *testing.T, _ model.ProcessDefinition, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "trailing data")
			},
		},
		{
			// Trailing whitespace is legal JSON framing — json.Marshal output
			// written to a file or column routinely gains one. Rejecting it
			// would make engine-written definitions unloadable.
			name: "trailing whitespace is legal framing",
			json: validJSON + "\n\t ",
			assert: func(t *testing.T, def model.ProcessDefinition, err error) {
				require.NoError(t, err)
				assert.Equal(t, "p", def.ID)
			},
		},
		{
			name: "valid definition still decodes",
			json: validJSON,
			assert: func(t *testing.T, def model.ProcessDefinition, err error) {
				require.NoError(t, err)
				assert.Equal(t, "p", def.ID)
				require.Len(t, def.Nodes, 1)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var def model.ProcessDefinition
			err := def.UnmarshalJSON([]byte(tc.json))
			tc.assert(t, def, err)
		})
	}
}

// TestDefinitionStorePathIsStrict exercises the decode route the persistence
// layer actually uses. Every case above calls UnmarshalJSON DIRECTLY, which is
// not what DefinitionStore.GetDefinition/Lookup do — they call json.Unmarshal,
// and encoding/json validates the whole input and hands the custom unmarshaler
// exactly one already-checked value. So the direct-call cases alone never
// established that the STORE path rejects unknown fields; this does.
func TestDefinitionStorePathIsStrict(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		json   string
		assert func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name: "unknown node field is rejected through json.Unmarshal",
			json: `{"id":"p","version":1,"flows":[],` +
				`"nodes":[{"id":"a","kind":"userTask","eligable_roles":["manager"]}]}`,
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "eligable_roles")
			},
		},
		{
			// The five tags that were camelCase before ADR-0144 renamed the wire
			// to snake_case (8179c0b). A definition row written before that
			// commit carries them and no longer loads — the real migration
			// trigger, larger than the retired errorEndEvent kind.
			name: "a pre-ADR-0144 camelCase blob no longer loads",
			json: `{"id":"p","version":1,"flows":[],` +
				`"nodes":[{"id":"a","kind":"serviceTask","compensateAction":"refund"}]}`,
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "compensateAction")
			},
		},
		{
			name: "a definition this engine wrote still loads",
			json: `{"id":"p","version":1,"flows":[],` +
				`"nodes":[{"id":"a","kind":"serviceTask","compensate_action":"refund"}]}`,
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var def model.ProcessDefinition
			tc.assert(t, json.Unmarshal([]byte(tc.json), &def))
		})
	}
}

func TestStrictDecodingDoesNotRejectKindInappropriateFields(t *testing.T) {
	t.Parallel()

	// timer_duration belongs to timer nodes, but NodeWire/nodeYAML are flat
	// unions over all node kinds, so it is a KNOWN field and survives strict
	// decoding on a userTask. ADR-0167 records this limitation deliberately:
	// kind-appropriateness is model.Validate's concern, not the decoder's.
	yamlSrc := strings.Replace(validYAML,
		`    eligible_roles: ["manager"]`,
		"    eligible_roles: [\"manager\"]\n    timer_duration: \"PT5M\"", 1)
	// Guard the fixture, not just the assertion: if the replacement ever stops
	// matching (validYAML reindented, tag renamed), the test below would parse
	// plain validYAML and pass for the wrong reason.
	require.Contains(t, yamlSrc, "timer_duration", "fixture did not gain the kind-inappropriate field")

	ld, err := model.ParseYAML(strings.NewReader(yamlSrc))
	require.NoError(t, err)
	def, err := ld.Build()
	require.NoError(t, err)
	assert.Equal(t, "approval-process", def.ID)
}

// allFieldsYAML exercises every yaml tag declared by nodeYAML, definitionYAML
// and the nested structs a definition can author inline. It is the ADR-0167
// over-strictness guard: strictness makes every yaml:"…" tag load-bearing, so a
// missing or misspelled tag turns a legitimate definition into a hard parse
// error. TestAllDeclaredYAMLTagsParseUnderStrictDecoding proves this fixture
// stays exhaustive by deriving the tag list from the source.
const allFieldsYAML = `
id: all-fields
version: 1
cancel_actions: ["abort-everything"]
nodes:
  - id: start
    kind: startEvent
    name: Start
    label: Kick off
    message_name: order-received
    correlation_key: orderId
    message_start_singleton: true
  - id: charge
    kind: serviceTask
    name: Charge card
    action: charge-card
    retry_policy:
      max_attempts: 3
      initial_interval: 1s
      backoff_coef: 2.0
      max_interval: 10s
      max_elapsed: 1m
      non_retryable_errors: ["invalid-card"]
    compensate_action: refund-card
    compensate_ref: refund-ref
    compensate_scope_local: true
    cancel_action: void-authorisation
    recovery_flow: f_recover
    validation:
      kind: expr
      schema: "true"
  - id: approve
    kind: userTask
    eligible_roles: ["manager"]
    eligible_privileges: ["approve-invoice"]
    eligible_expr: "true"
    outcomes: ["approved", "rejected"]
    expose_outcome: true
    outcome_variable: decision
    completion_action: notify-finance
    deadline_duration: PT3H
    deadline_flow: f_deadline
    deadline_action: escalate
    wait_every: PT1H
    wait_action: send-reminder
  - id: sign
    kind: userTask
    manual: true
    manual_immediate: true
  - id: route
    kind: exclusiveGateway
  - id: wait
    kind: intermediateCatchEvent
    timer_duration: PT5M
  - id: await_signal
    kind: intermediateCatchEvent
    signal_name: approval-granted
  - id: err_boundary
    kind: boundaryEvent
    attached_to: charge
    error_code: CARD_DECLINED
  - id: nudge_boundary
    kind: boundaryEvent
    attached_to: approve
    non_interrupting: true
    timer_duration: PT30M
  - id: inner
    kind: subProcess
    subprocess:
      id: inner-def
      version: 1
      nodes:
        - id: inner_start
          kind: startEvent
        - id: inner_end
          kind: endEvent
      flows:
        - id: inner_f1
          source: inner_start
          target: inner_end
  - id: delegate
    kind: callActivity
    def_ref: other-process
  - id: finish
    kind: endEvent
    end_behavior: terminate
    termination_reason: manual override
    termination_outcome: complete
flows:
  - id: f1
    source: start
    target: charge
  - id: f2
    source: charge
    target: approve
  - id: f_recover
    source: charge
    target: finish
  - id: f3
    source: approve
    target: sign
  - id: f_deadline
    source: approve
    target: finish
  - id: f4
    source: sign
    target: route
  - id: f5
    source: route
    target: wait
    condition: "true"
  - id: f6
    source: route
    target: delegate
    is_default: true
  - id: f7
    source: wait
    target: await_signal
  - id: f8
    source: await_signal
    target: inner
  - id: f9
    source: inner
    target: finish
  - id: f10
    source: delegate
    target: finish
  - id: f_err
    source: err_boundary
    target: finish
  - id: f_nudge
    source: nudge_boundary
    target: finish
`

// declaredYAMLTags extracts the yaml tag names declared by the named structs in
// a Go source file. It reads the source rather than reflecting because nodeYAML
// and definitionYAML are unexported and this is a black-box test. Deriving the
// list mechanically is the point: a hand-copied list rots, and ADR-0167 requires
// the over-strictness guard to enumerate every tag rather than a sample.
func declaredYAMLTags(t *testing.T, file string, structs ...string) []string {
	t.Helper()

	src, err := os.ReadFile(file)
	require.NoError(t, err)

	// Capture the whole tag name up to the option comma or the closing quote.
	// A `[a-z_]+` class silently TRUNCATES anything else — a field tagged
	// `yaml:"schemaV2"` was captured as "schema", which the fixture already
	// contained, so the guard passed while leaving the real tag unguarded.
	// Verified by adding that exact field: EXIT=0 before this fix, RED after.
	tagRe := regexp.MustCompile(`yaml:"([^",]+)`)
	var out []string
	for _, name := range structs {
		block := regexp.MustCompile(`(?s)\ntype ` + name + ` struct \{(.*?)\n\}`).FindSubmatch(src)
		require.NotNil(t, block, "struct %s not found in %s", name, file)

		// Scan FIELDS, not just tags. A field with no yaml tag is invisible to
		// tagRe, yet yaml.v3 still makes its lowercased Go name an authorable
		// key — so strict decoding turns it load-bearing while this guard stays
		// green. Executed: adding an untagged `Priority int` to nodeYAML makes
		// `priority: 5` parse cleanly and leaves this test at EXIT=0. An earlier
		// revision claimed such a field would "fail loudly"; it did not.
		for _, fm := range regexp.MustCompile(`(?m)^\t([A-Z]\w*) .*$`).FindAllSubmatch(block[1], -1) {
			require.Contains(t, string(fm[0]), `yaml:"`,
				"%s.%s declares no yaml tag; yaml.v3 would expose it as a lowercased key that this guard cannot see",
				name, fm[1])
		}

		for _, m := range tagRe.FindAllSubmatch(block[1], -1) {
			// `yaml:"-"` means "never serialised", so it is not an authorable
			// key and must not be demanded of the fixture.
			if tag := string(m[1]); tag != "-" {
				out = append(out, tag)
			}
		}
	}
	require.NotEmpty(t, out, "no yaml tags derived from %s", file)
	return out
}

func TestAllDeclaredYAMLTagsParseUnderStrictDecoding(t *testing.T) {
	t.Parallel()

	tags := declaredYAMLTags(t, "yaml.go", "nodeYAML", "definitionYAML")
	tags = append(tags, declaredYAMLTags(t, "retry.go", "RetryPolicy")...)
	tags = append(tags, declaredYAMLTags(t, "validate/validate.go", "ValidationDescriptor")...)
	tags = append(tags, declaredYAMLTags(t, "../flow/flow.go", "SequenceFlow")...)

	// The anchored regexp matters: an unanchored search for "name" would also
	// match "signal_name", quietly excusing a tag the fixture never exercises.
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		if seen[tag] {
			continue
		}
		seen[tag] = true
		assert.Regexp(t, `(?m)^\s*-?\s*`+tag+`:`, allFieldsYAML,
			"declared yaml tag %q is not exercised by allFieldsYAML — strictness makes it load-bearing", tag)
	}

	ld, err := model.ParseYAML(strings.NewReader(allFieldsYAML))
	require.NoError(t, err)
	def, err := ld.Build()
	require.NoError(t, err)
	assert.Equal(t, "all-fields", def.ID)
}

// TestPersistedDefinitionRoundTripsThroughStrictJSON is the container-free proxy
// for DefinitionStore.GetDefinition/Lookup, which json.Unmarshal stored blobs
// straight into a ProcessDefinition and therefore run through the newly-strict
// UnmarshalJSON. ADR-0167 makes this a DATA migration, not only a source one:
// the change is affordable because marshal and unmarshal are symmetric through
// the same definitionWire, and this test is what holds that symmetry true.
// Regression guard, not a RED-first case.
func TestPersistedDefinitionRoundTripsThroughStrictJSON(t *testing.T) {
	t.Parallel()

	ld, err := model.ParseYAML(strings.NewReader(allFieldsYAML))
	require.NoError(t, err)
	// A definition-scoped action makes MarshalJSON emit "scoped_actions", the
	// one MARSHAL-ONLY key in definitionWire. Strict decoding is exactly what
	// turns a marshal-only key into an unloadable row, so the round trip must
	// carry one rather than exercising a symmetric best case.
	ld.RegisterActionFunc("charge-card", func(context.Context, map[string]any) (map[string]any, error) {
		return nil, nil
	})
	built, err := ld.Build()
	require.NoError(t, err)
	require.NotEmpty(t, built.Nodes)
	require.NotEmpty(t, built.ScopedActionNames(), "fixture must exercise the marshal-only scoped_actions key")

	blob, err := json.Marshal(built)
	require.NoError(t, err)
	require.Contains(t, string(blob), `"scoped_actions"`)

	var reloaded model.ProcessDefinition
	require.NoError(t, reloaded.UnmarshalJSON(blob),
		"a definition this engine wrote must still load after strict decoding")

	assert.Equal(t, built.ID, reloaded.ID)
	assert.Equal(t, built.Version, reloaded.Version)
	require.Len(t, reloaded.Nodes, len(built.Nodes))
	for i, want := range built.Nodes {
		assert.Equal(t, want.Kind(), reloaded.Nodes[i].Kind(),
			"node %d (%s) changed kind across the round trip", i, want.ID())
		assert.Equal(t, want.ID(), reloaded.Nodes[i].ID())
	}
	assert.Equal(t, built.Flows, reloaded.Flows)
}

// TestREADMEYAMLBlocksParseUnderStrictDecoding keeps the published quickstart
// honest. ADR-0167's audit found README.md was a LIVE instance of the bug this
// ADR fixes: it prescribed camelCase keys that no struct tag declares, so the
// documented definition silently yielded an allow-all task, a compensation
// action that never ran and a nil retry policy — while
// examples/readme_quickstart/main.go used the correct snake_case tags and the
// two drifted apart unnoticed. Under strict decoding that drift is no longer
// silent, and this test is what makes it impossible to reintroduce.
//
// ⚠ Constraint this imposes on README.md: every ```yaml / ```yml block must be a
// COMPLETE, buildable definition, because each one is parsed AND built here. An
// illustrative fragment belongs in a fence with no language tag, or in prose.
func TestREADMEYAMLBlocksParseUnderStrictDecoding(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("../../README.md")
	require.NoError(t, err)

	// Accept ```yml as well as ```yaml, and allow the fence to be indented — the
	// original pattern matched only unindented ```yaml, so a future README edit
	// using either other form would silently escape this guard.
	blocks := regexp.MustCompile("(?s)```ya?ml[ \t]*\r?\n(.*?)```").FindAllStringSubmatch(string(src), -1)
	require.NotEmpty(t, blocks, "no yaml blocks found in README.md — has the fence style changed?")

	for i, b := range blocks {
		t.Run(fmt.Sprintf("block_%d", i+1), func(t *testing.T) {
			t.Parallel()
			ld, parseErr := model.ParseYAML(strings.NewReader(b[1]))
			require.NoError(t, parseErr, "README yaml block %d does not parse:\n%s", i+1, b[1])
			_, buildErr := ld.Build()
			require.NoError(t, buildErr, "README yaml block %d does not build:\n%s", i+1, b[1])
		})
	}
}

// TestProcessDefinitionUnmarshalJSONEmptyInputIsNotEOF pins a regression the
// adversarial review caught: swapping json.Unmarshal for a Decoder made empty
// input return a bare io.EOF, so errors.Is(err, io.EOF) flipped false -> true.
// A caller treating io.EOF as "clean end of stream" would silently skip a
// corrupt or empty definition instead of failing. Baseline returned a
// *json.SyntaxError ("unexpected end of JSON input"); ADR-0167 D3 keeps each
// decoder's existing error shape, so the EOF identity must not leak out.
func TestProcessDefinitionUnmarshalJSONEmptyInputIsNotEOF(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"", "   \n\t "} {
		t.Run(fmt.Sprintf("input_%q", in), func(t *testing.T) {
			t.Parallel()
			var def model.ProcessDefinition
			err := def.UnmarshalJSON([]byte(in))
			require.Error(t, err)
			assert.False(t, errors.Is(err, io.EOF),
				"empty input must not report io.EOF — a stream caller reads that as a clean end")
		})
	}
}

// TestNestedSubprocessDecodingIsStrict pins that strictness reaches INTO nested
// subprocess definitions on both decoders. A subprocess is the recursive case —
// YAML nests a *definitionYAML, JSON nests a *ProcessDefinition that re-enters
// UnmarshalJSON — so it is exactly where a strictness hole would hide while every
// top-level test stayed green. Regression guard, not a RED-first case.
func TestNestedSubprocessDecodingIsStrict(t *testing.T) {
	t.Parallel()

	const outerYAML = `
id: outer
version: 1
nodes:
  - id: s
    kind: startEvent
  - id: sub
    kind: subProcess
    subprocess:
      id: inner
      version: 1
      nodes:
        - id: is
          kind: startEvent
        - id: ie
          kind: endEvent
      flows:
        - { id: if1, source: is, target: ie }
  - id: e
    kind: endEvent
flows:
  - { id: f1, source: s, target: sub }
  - { id: f2, source: sub, target: e }
`

	t.Run("yaml unknown key on the nested definition", func(t *testing.T) {
		t.Parallel()
		src := strings.Replace(outerYAML, "      id: inner\n", "      id: inner\n      bogus_inner_key: 1\n", 1)
		require.Contains(t, src, "bogus_inner_key", "fixture did not gain the unknown key")
		_, err := model.ParseYAML(strings.NewReader(src))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bogus_inner_key")
	})

	t.Run("yaml unknown key on a nested node", func(t *testing.T) {
		t.Parallel()
		src := strings.Replace(outerYAML, "          kind: endEvent\n", "          kind: endEvent\n          bogus_nested_node: 1\n", 1)
		require.Contains(t, src, "bogus_nested_node", "fixture did not gain the unknown key")
		_, err := model.ParseYAML(strings.NewReader(src))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bogus_nested_node")
	})

	t.Run("json unknown key on the nested definition", func(t *testing.T) {
		t.Parallel()
		const nested = `{"id":"outer","version":1,"flows":[],"nodes":[` +
			`{"id":"sub","kind":"subProcess","subprocess":` +
			`{"id":"inner","version":1,"nodes":[],"flows":[],"bogus_inner_key":1}}]}`
		var def model.ProcessDefinition
		err := def.UnmarshalJSON([]byte(nested))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bogus_inner_key")
	})

	t.Run("the clean nested definition still parses", func(t *testing.T) {
		t.Parallel()
		ld, err := model.ParseYAML(strings.NewReader(outerYAML))
		require.NoError(t, err)
		def, err := ld.Build()
		require.NoError(t, err)
		assert.Equal(t, "outer", def.ID)
	})
}

// TestParseYAMLRejectsExtraDocuments closes the YAML mirror of the trailing-data
// hole the JSON side already guards. yaml.Decoder.Decode consumes ONE document,
// so KnownFields(true) never sees documents 2..n: everything after a `---` was
// silently discarded, unknown fields included.
//
// Found by the adversarial security review, and it is not cosmetic — it is a
// live instance of the bypass this ADR claims to close. A file whose second
// document declares eligible_roles parses clean and builds a task with NO roles,
// so a human reviewing the file sees an eligibility rule the engine never
// applies (empty AuthzSpec = allow-all).
func TestParseYAMLRejectsExtraDocuments(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		yaml   string
		assert func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name: "a second document is rejected, not silently dropped",
			yaml: validYAML + "---\n" + validYAML,
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "single YAML document")
			},
		},
		{
			name: "the eligibility-overlay bypass is refused",
			yaml: validYAML + "---\nid: overlay\nnodes:\n  - id: approve\n    eligible_roles: [\"manager\"]\n",
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
			},
		},
		{
			name: "a trailing document separator alone is still fine",
			yaml: validYAML + "---\n",
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "a leading document separator is still fine",
			yaml: "---\n" + validYAML,
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "single document unaffected",
			yaml: validYAML,
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := model.ParseYAML(strings.NewReader(tc.yaml))
			tc.assert(t, err)
		})
	}
}

// TestParseYAMLBoundsUnknownFieldErrorSize guards a cost strict decoding
// introduced: yaml.v3 emits one message per unknown key, so a definition made of
// unknown keys produced an error string ~2.4x the size of its own input
// (measured: 1.9 MB in -> 4.7 MB out), and that error is logged in full
// server-side. The baseline reported nothing at all, so this is new. The first
// few messages are what an author needs; the rest are a log-flooding vector.
func TestParseYAMLBoundsUnknownFieldErrorSize(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	b.WriteString("id: a\nversion: 1\nnodes: []\nflows: []\n")
	const unknownKeys = 500
	for i := range unknownKeys {
		fmt.Fprintf(&b, "unknown_key_%d: 1\n", i)
	}

	_, err := model.ParseYAML(strings.NewReader(b.String()))
	require.Error(t, err)

	msg := err.Error()
	assert.Contains(t, msg, "unknown_key_0", "the first offending field must still be named")
	assert.Contains(t, msg, "more field errors", "the tail must be summarised, not printed")
	assert.Less(t, len(msg), b.Len(),
		"a parse error must not be larger than the document that caused it (was %d for a %d-byte input)",
		len(msg), b.Len())

	// Truncating must not change the error's TYPE. A consumer building a
	// field-level 400 by enumerating te.Errors would otherwise get the full list
	// for a small file and nothing at all for a large one — the case where the
	// list matters most.
	var te *yaml.TypeError
	require.ErrorAs(t, err, &te, "the bounded error must still be a *yaml.TypeError")
	assert.LessOrEqual(t, len(te.Errors), 21, "bounded list plus one summary line")
}
