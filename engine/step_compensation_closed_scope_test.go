package engine

// step_compensation_closed_scope_test.go — telling a closed scope from an empty
// one.
//
// compensationRecordsForScope returned a bare []CompensationRecord, so its two
// distinct answers were indistinguishable to every caller:
//
//	"this scope is open and holds no records"   → nil
//	"this scope is not open at all"             → nil
//
// s.Scopes holds OPEN scopes only (closeScope prunes the entry, endInstance nils
// the slice), so the second answer is real. The item names the scope-wide
// compensation throw as the load-bearing consumer, on the grounds that it reads
// `len(records) == 0` and auto-advances.
//
// ⚠ THE FILED HARM WAS REFUTED BY EXECUTION. The item says the damage is that a
// throw naming a CLOSED scope auto-advances silently. It does not: drive()
// resolves defForScope(def, s, tok.ScopeID) for every active token before any
// strategy runs, and that hard-errors on a scope absent from s.Scopes, so the
// throw strategy is never reached. The conflation is real in the HELPER and is
// now expressed in its signature; the cited consumer cannot observe it. The
// second test below pins that upstream gate, so relaxing it re-opens the
// question loudly instead of silently.
//
// White-box: compensationRecordsForScope and drive are unexported.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
)

// closedScopeThrowDef is a bare scope-wide compensation throw with a successor,
// so resumeNode != "" and the strategy reaches the committing branch where the
// records lookup happens.
//
//	start → boom[CompensateThrow] → after → end
func closedScopeThrowDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-closed-scope-throw", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewCompensateThrow("boom"),
			event.NewIntermediateThrow("after"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "boom"},
			{ID: "f2", Source: "boom", Target: "after"},
			{ID: "f3", Source: "after", Target: "end"},
		},
	}
}

// TestCompensationRecordsForScopeReportsClosedScope pins the second return: a
// closed scope must be distinguishable from an open scope holding no records.
//
// What makes it fail: the helper returning only []CompensationRecord — the
// `ok` result does not exist, so the file does not compile (a valid RED), and
// once it does, collapsing `ok` to `len(records) > 0` makes the
// "open scope with no records" case report false.
func TestCompensationRecordsForScopeReportsClosedScope(t *testing.T) {
	t.Parallel()

	rec := CompensationRecord{NodeID: "alpha", Action: "undo-alpha"}

	type testCase struct {
		name   string
		state  *InstanceState
		scope  string
		assert func(t *testing.T, records []CompensationRecord, ok bool)
	}

	cases := []testCase{
		{
			name:  "root scope is always resolvable",
			state: &InstanceState{RootCompensations: []CompensationRecord{rec}},
			scope: "",
			assert: func(t *testing.T, records []CompensationRecord, ok bool) {
				assert.True(t, ok, "the root scope is implicit and always resolvable")
				assert.Len(t, records, 1)
			},
		},
		{
			name:  "an empty root scope is still resolvable",
			state: &InstanceState{},
			scope: "",
			assert: func(t *testing.T, records []CompensationRecord, ok bool) {
				assert.True(t, ok)
				assert.Empty(t, records)
			},
		},
		{
			name:  "an open scope holding records resolves",
			state: &InstanceState{Scopes: []Scope{{ID: "s1", Compensations: []CompensationRecord{rec}}}},
			scope: "s1",
			assert: func(t *testing.T, records []CompensationRecord, ok bool) {
				assert.True(t, ok)
				assert.Len(t, records, 1)
			},
		},
		{
			// The distinction the whole item is about: same nil records as the
			// case below, opposite meaning.
			name:  "an open scope holding NO records resolves",
			state: &InstanceState{Scopes: []Scope{{ID: "s1"}}},
			scope: "s1",
			assert: func(t *testing.T, records []CompensationRecord, ok bool) {
				assert.True(t, ok, "an open scope with no records is resolvable, not missing")
				assert.Empty(t, records)
			},
		},
		{
			name:  "a closed scope does not resolve",
			state: &InstanceState{Scopes: []Scope{{ID: "other"}}},
			scope: "s1",
			assert: func(t *testing.T, records []CompensationRecord, ok bool) {
				assert.False(t, ok, "a scope absent from s.Scopes is closed, not empty")
				assert.Empty(t, records)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			records, ok := compensationRecordsForScope(tc.state, tc.scope)
			tc.assert(t, records, ok)
		})
	}
}

// TestCompensationThrowInClosedScopeIsRefusedUpstream is the REACHABILITY
// probe, kept as a regression pin because it REFUTED the filed defect.
//
// The filed defect claims the harm is that "a compensation throw whose scope has
// already been closed auto-advances silently". Executed, that does not happen:
// drive() resolves defForScope(def, s, tok.ScopeID) for every active token
// BEFORE dispatching to any node strategy, and defForScope hard-errors on a
// scope absent from s.Scopes. The Step fails loudly; the throw strategy is never
// entered, so the conflation cannot be observed there at all.
//
// This pins the upstream gate. If it is ever relaxed — making a closed-scope
// token merely park instead of erroring — this test goes red and the
// compensation throw's `records, _ :=` becomes a live conflation that must then
// be handled.
func TestCompensationThrowInClosedScopeIsRefusedUpstream(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	def := closedScopeThrowDef()

	// A live token at the throw, naming a scope that is NOT in s.Scopes, with
	// archived records that a silent auto-advance would strand.
	s := &InstanceState{
		InstanceID: "i1",
		Status:     StatusRunning,
		Tokens: []Token{{
			ID: "t1", NodeID: "boom", ScopeID: "gone", State: TokenActive, EnteredAt: at,
		}},
		ArchivedCompensations: map[string][]CompensationRecord{
			"gone": {{NodeID: "alpha", Action: "undo-alpha"}},
		},
	}

	cmds, err := drive(t.Context(), def, s, at, stepPolicy{})

	require.Error(t, err, "a token naming a closed scope must fail the step, not reach a node strategy")
	assert.Contains(t, err.Error(), `defForScope: unknown scope "gone"`)
	assert.Empty(t, cmds, "no command may be emitted for a token whose scope cannot be resolved")

	// The token is untouched: it never entered the throw strategy, so it did not
	// auto-advance along f2.
	require.Len(t, s.Tokens, 1)
	assert.Equal(t, "boom", s.Tokens[0].NodeID,
		"the token must not have advanced past the throw")
}
