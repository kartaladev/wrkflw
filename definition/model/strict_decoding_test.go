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
// silently turn empty input into a parse error.
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
			// change rather than incidental.
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
			// The five tags that were camelCase before the wire was renamed
			// to snake_case (8179c0b). A definition row written before that
			// commit carries them and no longer loads — the real migration
			// trigger, larger than the retired errorEndEvent kind.
			name: "a legacy camelCase blob no longer loads",
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

// TestStrictDecodingRejectsKindInappropriateFields REVERSES a decision this test
// previously pinned. It used to assert the opposite, on the rationale that
// "kind-appropriateness is model.Validate's concern, not the decoder's".
//
// That rationale does not survive: Validate structurally CANNOT see it. Validate
// receives []Node, and by the time it runs fromWire has already dropped the
// misplaced key — the decoder is the only place the original NodeWire and the
// reconstructed node coexist. The stated owner could not do the job, which is
// why it had never been done. See #183 and node_wire_keys.go.
//
// timer_duration is still a KNOWN field on the flat union, so strict decoding
// (KnownFields(true)) never saw it; what refuses it now is the kind-key gate,
// which measures per kind whether FromWire reads the field at all.
func TestStrictDecodingRejectsKindInappropriateFields(t *testing.T) {
	t.Parallel()

	yamlSrc := strings.Replace(validYAML,
		`    eligible_roles: ["manager"]`,
		"    eligible_roles: [\"manager\"]\n    timer_duration: \"PT5M\"", 1)
	// Guard the fixture, not just the assertion: if the replacement ever stops
	// matching (validYAML reindented, tag renamed), the test below would parse
	// plain validYAML and pass for the wrong reason.
	require.Contains(t, yamlSrc, "timer_duration", "fixture did not gain the kind-inappropriate field")

	_, err := model.ParseYAML(strings.NewReader(yamlSrc))
	require.ErrorIs(t, err, model.ErrKeyNotOnKind)
	assert.Contains(t, err.Error(), "timer_duration")

	// The at-limit control: the UNMUTATED fixture still parses and builds.
	// Without one, a gate that refused every definition would satisfy the
	// assertion above just as well. TestParseYAMLRejectsUnknownFields's "valid
	// definition still parses" case asserts the same thing for the same fixture;
	// this one is kept beside the refusal because a discrimination proof read at
	// a distance is one a reader has to go looking for.
	ld, err := model.ParseYAML(strings.NewReader(validYAML))
	require.NoError(t, err)
	def, err := ld.Build()
	require.NoError(t, err)
	assert.Equal(t, "approval-process", def.ID)
}

// allFieldsYAML exercises every yaml tag declared by nodeYAML, definitionYAML
// and the nested structs a definition can author inline. It is the
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
    message_name: order-received
    correlation_key: orderId
    message_start_singleton: true
    # validation lives on a kind that HAS a validation slot. It used to sit on
    # the "charge" serviceTask, which has none, so the descriptor was silently
    # discarded and this fixture exercised the tag without ever exercising the
    # feature. ErrKeyNotOnKind refuses that now (#183).
    validation:
      kind: expr
      schema: "true"
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
    cancel_action: void-authorisation
    recovery_flow: f_recover
  - id: approve
    kind: userTask
    eligible_roles: ["manager"]
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
    # eligible_privileges lives here rather than on "approve" because
    # model.Validate refuses a node declaring BOTH eligible_roles and
    # eligible_privileges (ErrRolesAndPrivileges): the authorizer resolves
    # identity first-applicable, so a node setting both would have one silently
    # ignored. This fixture must still exercise every declared tag AND Build, so
    # the two identity tags are split across the two userTask nodes.
    eligible_privileges: ["approve-invoice"]
    manual: true
    manual_immediate: true
  # compensate_ref and compensate_scope_local both belong to a
  # compensationThrowEvent, not to the "charge" serviceTask they used to sit on
  # (#183). They are split across TWO throws because model.Validate refuses a
  # single node declaring both (ErrScopeLocalWithCompensateRef): ScopeLocal
  # narrows only the scope-WIDE throw, so the engine would ignore it beside a
  # targeted CompensateRef. Same remedy, and same reason, as the
  # eligible_roles/eligible_privileges split above.
  - id: compensate_targeted
    kind: compensationThrowEvent
    compensate_ref: charge
  - id: compensate_scoped
    kind: compensationThrowEvent
    compensate_scope_local: true
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
    boundary_action: notify-declined
    boundary_error_expr: '_error == "CARD_DECLINED"'
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
    target: compensate_targeted
  - id: f4a
    source: compensate_targeted
    target: compensate_scoped
  - id: f4b
    source: compensate_scoped
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
// list mechanically is the point: a hand-copied list rots, and the
// over-strictness guard must enumerate every tag rather than a sample.
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

// refusedYAMLTags are the yaml tags nodeYAML declares in order to REFUSE a key
// rather than to accept one. Both are authorable — yaml.v3 sees them as known
// fields, which is the whole point: deleting them would make KnownFields(true)
// report "field label not found" and name the wrong key — but no definition
// carrying either one ever loads. They therefore CANNOT appear in
// allFieldsYAML, which must parse and Build cleanly.
//
// This is not an exemption list. TestRefusedYAMLTagsAreActuallyRefused exercises
// each entry from the other side, asserting three things: that the tag is really
// declared (a stale entry excuses nothing), that the entry names a NON-NIL
// sentinel, and that a definition carrying the key really fails with it. A tag
// parked here to dodge the fixture requirement fails that test, so the
// exhaustiveness this file provides for every other tag is not weakened — the
// domain stays total, split into accepted and refused halves.
//
// The non-nil check is load-bearing and was added after review: errors.Is(nil,
// nil) is true, so an entry written `err: nil` satisfied require.ErrorIs while
// being refused by nothing at all, and the skip below had already excused it from
// allFieldsYAML. An earlier version of THIS comment asserted that could not
// happen. It could, for exactly that one value.
var refusedYAMLTags = map[string]struct {
	// fields is the node-level YAML that authors the key (4-space indented).
	fields string
	// err is the sentinel a definition carrying it must fail with. It also fixes
	// WHERE the refusal happens, so no separate field records that: a retired key
	// dies in the decoder (ErrRetiredLabelKey), a reserved one in Validate
	// (ErrRuleNotSupported). The assertion below runs both stages and requires the
	// sentinel from whichever one reports.
	err error
}{
	"label": {fields: "    label: Kick off", err: model.ErrRetiredLabelKey},
	"rule":  {fields: "    rule: pricing.v3", err: model.ErrRuleNotSupported},
}

// Note on what excluding `rule` from allFieldsYAML costs. The reserved key's whole
// justification is that it round-trips through the persisted format, so the
// adapter needs no format change — and allFieldsYAML is the fixture that guards
// exactly that (TestPersistedDefinitionRoundTripsThroughStrictJSON). It cannot
// carry `rule`, because that fixture must Build cleanly and a rule is refused. The
// round-trip coverage lives in TestBusinessRuleTaskRuleRoundTrip instead, which
// exercises the same persisted-blob seam (raw JSON into ProcessDefinition and back
// out) for both authored shapes. Named here so the gap is a recorded hand-off
// rather than an invisible one.

// refusedTagYAML is the smallest definition that can carry an arbitrary node
// key, so a failure names the key under test rather than a structural defect.
// The node is a businessRuleTask because `rule` is only meaningful there.
const refusedTagYAML = `
id: refused
version: 1
nodes:
  - id: s
    kind: startEvent
  - id: n
    kind: businessRuleTask
    name: Score
%s
  - id: e
    kind: endEvent
flows:
  - { id: f1, source: s, target: n }
  - { id: f2, source: n, target: e }
`

// TestRefusedYAMLTagsAreActuallyRefused is the second half of the
// declared-tag domain: every tag in refusedYAMLTags must really be refused, and
// really be a declared tag. The control row — the same fixture with no extra
// key — is the at-limit ACCEPT: without it, a definition that refused every
// businessRuleTask would satisfy both refusal rows just as well.
func TestRefusedYAMLTagsAreActuallyRefused(t *testing.T) {
	t.Parallel()

	declared := make(map[string]bool)
	for _, tag := range declaredYAMLTags(t, "yaml.go", "nodeYAML", "definitionYAML") {
		declared[tag] = true
	}

	// The control: nothing added, so the fixture itself must be valid.
	t.Run("control: the bare fixture is valid", func(t *testing.T) {
		t.Parallel()
		ld, err := model.ParseYAML(strings.NewReader(fmt.Sprintf(refusedTagYAML, "")))
		require.NoError(t, err)
		_, err = ld.Build()
		require.NoError(t, err)
	})

	for tag, refusal := range refusedYAMLTags {
		t.Run(tag, func(t *testing.T) {
			t.Parallel()

			require.True(t, declared[tag],
				"refusedYAMLTags names %q, which nodeYAML/definitionYAML do not declare — a stale entry excuses nothing", tag)

			// errors.Is(nil, nil) is true, so without this a tag parked with a nil
			// sentinel would satisfy the ErrorIs below while being refused by
			// NOTHING — and the exhaustiveness skip above would already have
			// excused it from allFieldsYAML. That is the fail-open this guard
			// exists to prevent, so the ledger's own entry is checked first.
			require.Error(t, refusal.err,
				"refusedYAMLTags[%q] names no sentinel; a nil one would exempt the tag from allFieldsYAML while refusing nothing", tag)

			ld, err := model.ParseYAML(strings.NewReader(fmt.Sprintf(refusedTagYAML, refusal.fields)))
			if err == nil {
				_, err = ld.Build()
			}
			require.ErrorIs(t, err, refusal.err)
		})
	}
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
		if _, refused := refusedYAMLTags[tag]; refused {
			// Exercised by TestRefusedYAMLTagsAreActuallyRefused instead: this
			// fixture must parse AND Build, which a refused key by definition
			// prevents. That test also proves the refusal fires, so the tag is
			// not merely skipped here.
			continue
		}
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
// UnmarshalJSON. This is a DATA migration, not only a source one:
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

// TestProcessDefinitionUnmarshalJSONEmptyInputIsNotEOF pins a regression the
// adversarial review caught: swapping json.Unmarshal for a Decoder made empty
// input return a bare io.EOF, so errors.Is(err, io.EOF) flipped false -> true.
// A caller treating io.EOF as "clean end of stream" would silently skip a
// corrupt or empty definition instead of failing. Baseline returned a
// *json.SyntaxError ("unexpected end of JSON input"); each decoder keeps its
// existing error shape, so the EOF identity must not leak out.
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
