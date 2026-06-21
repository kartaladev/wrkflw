package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
)

// ---------------------------------------------------------------------------
// Scope helpers (openScope / tokensInScope / closeScope / scopeByID)
// Tested via the thin exported shims in export_test.go (package engine).
// We use the export_test.go pattern (idiomatic Go) because the helpers are
// unexported methods on *InstanceState; black-box tests cannot call them
// directly, but a same-package shim file reachable only during testing exposes
// them to the _test package without polluting the public API.
// ---------------------------------------------------------------------------

// TestOpenScopeReturnsSequentialIDs asserts that openScope assigns deterministic
// IDs using the ScopeSeq counter: "<instanceID>-s1", "<instanceID>-s2", …
func TestOpenScopeReturnsSequentialIDs(t *testing.T) {
	s := &engine.InstanceState{InstanceID: "inst-1"}

	id1 := engine.OpenScope(s, "node-A", "")
	id2 := engine.OpenScope(s, "node-B", id1)
	id3 := engine.OpenScope(s, "node-C", "")

	assert.Equal(t, "inst-1-s1", id1)
	assert.Equal(t, "inst-1-s2", id2)
	assert.Equal(t, "inst-1-s3", id3)
}

// TestOpenScopeAppendsToScopes asserts that each openScope call appends a new
// Scope with the correct NodeID and ParentID.
func TestOpenScopeAppendsToScopes(t *testing.T) {
	s := &engine.InstanceState{InstanceID: "proc-7"}

	parentID := engine.OpenScope(s, "sub-proc", "")
	childID := engine.OpenScope(s, "embedded", parentID)

	require.Len(t, s.Scopes, 2)

	parent := engine.ScopeByID(s, parentID)
	require.NotNil(t, parent)
	assert.Equal(t, "sub-proc", parent.NodeID)
	assert.Equal(t, "", parent.ParentID)

	child := engine.ScopeByID(s, childID)
	require.NotNil(t, child)
	assert.Equal(t, "embedded", child.NodeID)
	assert.Equal(t, parentID, child.ParentID)
}

// TestScopeByIDReturnsNilForMissing asserts scopeByID returns nil for an
// unknown scope ID.
func TestScopeByIDReturnsNilForMissing(t *testing.T) {
	s := &engine.InstanceState{InstanceID: "i1"}
	engine.OpenScope(s, "n1", "")

	assert.Nil(t, engine.ScopeByID(s, "no-such-scope"))
}

// TestTokensInScopeCountsCorrectly asserts tokensInScope counts only tokens
// whose ScopeID matches.
func TestTokensInScopeCountsCorrectly(t *testing.T) {
	s := &engine.InstanceState{InstanceID: "i2"}

	scopeA := engine.OpenScope(s, "nodeA", "")
	scopeB := engine.OpenScope(s, "nodeB", "")

	s.Tokens = []engine.Token{
		{ID: "t1", NodeID: "n1", ScopeID: scopeA},
		{ID: "t2", NodeID: "n2", ScopeID: scopeA},
		{ID: "t3", NodeID: "n3", ScopeID: scopeB},
		{ID: "t4", NodeID: "n4", ScopeID: ""},
	}

	assert.Equal(t, 2, engine.TokensInScope(s, scopeA))
	assert.Equal(t, 1, engine.TokensInScope(s, scopeB))
	assert.Equal(t, 0, engine.TokensInScope(s, "no-scope"))
}

// TestTokensInScopeZeroForEmpty asserts tokensInScope returns 0 when no tokens exist.
func TestTokensInScopeZeroForEmpty(t *testing.T) {
	s := &engine.InstanceState{InstanceID: "i3"}
	scopeID := engine.OpenScope(s, "n1", "")

	assert.Equal(t, 0, engine.TokensInScope(s, scopeID))
}

// TestCloseScopeRemovesScope asserts closeScope removes the named scope from
// s.Scopes and leaves other scopes intact.
func TestCloseScopeRemovesScope(t *testing.T) {
	s := &engine.InstanceState{InstanceID: "i4"}

	id1 := engine.OpenScope(s, "n1", "")
	id2 := engine.OpenScope(s, "n2", id1)
	id3 := engine.OpenScope(s, "n3", "")

	require.Len(t, s.Scopes, 3)

	engine.CloseScope(s, id2)

	require.Len(t, s.Scopes, 2)
	assert.Nil(t, engine.ScopeByID(s, id2))
	assert.NotNil(t, engine.ScopeByID(s, id1))
	assert.NotNil(t, engine.ScopeByID(s, id3))
}

// TestCloseScopeIsNoOpForMissing asserts closeScope is a no-op when the given
// scope ID does not exist.
func TestCloseScopeIsNoOpForMissing(t *testing.T) {
	s := &engine.InstanceState{InstanceID: "i5"}
	engine.OpenScope(s, "n1", "")
	require.Len(t, s.Scopes, 1)

	engine.CloseScope(s, "does-not-exist")

	assert.Len(t, s.Scopes, 1)
}

// ---------------------------------------------------------------------------
// cloneState: Scopes deep-copy
// ---------------------------------------------------------------------------

// TestCloneStateDeepCopiesScopes asserts that cloneState copies the Scopes slice
// independently: mutating the clone's Scopes slice does not affect the original.
func TestCloneStateDeepCopiesScopes(t *testing.T) {
	s := &engine.InstanceState{InstanceID: "i6"}
	id1 := engine.OpenScope(s, "n1", "")
	engine.OpenScope(s, "n2", id1)

	original := s.Clone()
	cloned := original.Clone()

	// Mutate clone's Scopes slice (append).
	cloned.Scopes = append(cloned.Scopes, engine.Scope{ID: "extra", NodeID: "nx"})

	assert.Len(t, original.Scopes, 2, "appending to clone must not grow original")
}

// TestCloneStateDeepCopiesCompensations asserts that mutating a clone's
// Scope.Compensations slice does not affect the corresponding scope in the
// original.
func TestCloneStateDeepCopiesCompensations(t *testing.T) {
	s := &engine.InstanceState{InstanceID: "i7"}
	scopeID := engine.OpenScope(s, "n1", "")

	// Add a compensation record to the scope.
	scope := engine.ScopeByID(s, scopeID)
	require.NotNil(t, scope)
	scope.Compensations = []engine.CompensationRecord{
		{NodeID: "taskA", Action: "undoTaskA"},
	}

	original := s.Clone()
	cloned := original.Clone()

	// Append to the clone's Compensations; the original must be unaffected.
	cloned.Scopes[0].Compensations = append(
		cloned.Scopes[0].Compensations,
		engine.CompensationRecord{NodeID: "taskB", Action: "undoTaskB"},
	)

	assert.Len(t, original.Scopes[0].Compensations, 1,
		"appending to clone's Compensations must not affect original")
}

// TestScopeSeqCarriedByClone asserts ScopeSeq is a scalar field and is
// correctly copied by Clone (structural copy).
func TestScopeSeqCarriedByClone(t *testing.T) {
	s := &engine.InstanceState{InstanceID: "i8"}
	engine.OpenScope(s, "n1", "")
	engine.OpenScope(s, "n2", "")

	assert.Equal(t, 2, s.ScopeSeq)

	cloned := s.Clone()
	assert.Equal(t, 2, cloned.ScopeSeq)
}

// ---------------------------------------------------------------------------
// cloneState: Incidents deep-copy + Token retry fields
// ---------------------------------------------------------------------------

// TestCloneStateDeepCopiesIncidents asserts that cloneState (via Clone) produces
// an independently allocated Incidents slice, and that the new Token retry fields
// (RetryAttempts, RetryStartedAt) are carried through without aliasing.
func TestCloneStateDeepCopiesIncidents(t *testing.T) {
	st := engine.InstanceState{
		Incidents: []engine.Incident{
			{ID: "p-in0", TokenID: "p-t1", NodeID: "task", Error: "boom", Attempts: 3},
		},
		Tokens: []engine.Token{
			{ID: "p-t1", NodeID: "task", State: engine.TokenIncident, RetryAttempts: 3},
		},
	}

	clone := st.Clone()

	// Mutate the clone; the original must be unaffected.
	clone.Incidents[0].Error = "mutated"
	clone.Tokens[0].RetryAttempts = 99

	assert.Equal(t, "boom", st.Incidents[0].Error,
		"Incidents slice aliased — cloneState must deep-copy it")
	assert.Equal(t, 3, st.Tokens[0].RetryAttempts,
		"Token.RetryAttempts aliased — element copy must carry scalar fields")
}

// ---------------------------------------------------------------------------
// Sub-instance command: StartSubInstance
// ---------------------------------------------------------------------------

// TestStartSubInstanceSatisfiesCommand asserts StartSubInstance implements
// engine.Command (compile-time + runtime checks).
func TestStartSubInstanceSatisfiesCommand(t *testing.T) {
	cmd := engine.StartSubInstance{
		CommandID: "cmd-1",
		DefRef:    "sub-proc-def",
		Input:     map[string]any{"key": "val"},
	}

	var c engine.Command = cmd // compile-time assertion
	_ = c                      // suppress unused-var warning

	assert.Equal(t, "cmd-1", cmd.CommandID)
	assert.Equal(t, "sub-proc-def", cmd.DefRef)
	assert.Equal(t, map[string]any{"key": "val"}, cmd.Input)
}

// TestStartSubInstanceFieldsRoundTrip asserts all fields survive a round-trip.
func TestStartSubInstanceFieldsRoundTrip(t *testing.T) {
	cases := []struct {
		name      string
		commandID string
		defRef    string
		input     map[string]any
	}{
		{"no-input", "c1", "def-A", nil},
		{"with-input", "c2", "def-B", map[string]any{"x": 42, "y": "hello"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cmd := engine.StartSubInstance{
				CommandID: tc.commandID,
				DefRef:    tc.defRef,
				Input:     tc.input,
			}
			assert.Equal(t, tc.commandID, cmd.CommandID)
			assert.Equal(t, tc.defRef, cmd.DefRef)
			assert.Equal(t, tc.input, cmd.Input)
		})
	}
}

// ---------------------------------------------------------------------------
// Sub-instance triggers: SubInstanceCompleted / SubInstanceFailed
// ---------------------------------------------------------------------------

// TestSubInstanceTriggersCarryOccurredAt asserts both triggers satisfy
// engine.Trigger and that constructors stamp OccurredAt correctly.
func TestSubInstanceTriggersCarryOccurredAt(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	trs := []engine.Trigger{
		engine.NewSubInstanceCompleted(at, "cmd-1", map[string]any{"res": 1}),
		engine.NewSubInstanceFailed(at, "cmd-1", "child failed"),
	}
	for _, tr := range trs {
		assert.Equal(t, at, tr.OccurredAt())
	}
}

// TestSubInstanceCompletedFields asserts NewSubInstanceCompleted stores fields correctly.
func TestSubInstanceCompletedFields(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	output := map[string]any{"result": "ok", "count": 3}

	sc := engine.NewSubInstanceCompleted(at, "cmd-42", output)
	assert.Equal(t, at, sc.OccurredAt())
	assert.Equal(t, "cmd-42", sc.CommandID)
	assert.Equal(t, output, sc.Output)
}

// TestSubInstanceFailedFields asserts NewSubInstanceFailed stores fields correctly.
func TestSubInstanceFailedFields(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	sf := engine.NewSubInstanceFailed(at, "cmd-7", "timeout exceeded")
	assert.Equal(t, at, sf.OccurredAt())
	assert.Equal(t, "cmd-7", sf.CommandID)
	assert.Equal(t, "timeout exceeded", sf.Err)
}

// TestSubInstanceTriggerFieldsRoundTrip exercises multiple values via table.
func TestSubInstanceTriggerFieldsRoundTrip(t *testing.T) {
	at := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

	t.Run("SubInstanceCompleted", func(t *testing.T) {
		cases := []struct {
			commandID string
			output    map[string]any
		}{
			{"c1", nil},
			{"c2", map[string]any{"a": 1}},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.commandID, func(t *testing.T) {
				sc := engine.NewSubInstanceCompleted(at, tc.commandID, tc.output)
				assert.Equal(t, tc.commandID, sc.CommandID)
				assert.Equal(t, tc.output, sc.Output)
			})
		}
	})

	t.Run("SubInstanceFailed", func(t *testing.T) {
		cases := []struct {
			commandID string
			errMsg    string
		}{
			{"c3", ""},
			{"c4", "some error"},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.commandID, func(t *testing.T) {
				sf := engine.NewSubInstanceFailed(at, tc.commandID, tc.errMsg)
				assert.Equal(t, tc.commandID, sf.CommandID)
				assert.Equal(t, tc.errMsg, sf.Err)
			})
		}
	})
}
