package engine_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/engine"
)

// scopeTreeState returns a state whose scope tree is
//
//	root("") ─┬─ s1 ─── s2 ─── s3
//	          └─ s4
//
// with one token in each scope plus one at the root, built through OpenScope so
// the parent-before-child slice order openScope guarantees is real rather than
// hand-arranged.
func scopeTreeState(t *testing.T) *engine.InstanceState {
	t.Helper()
	s := &engine.InstanceState{InstanceID: "i1"}
	s1 := engine.OpenScope(s, "sub1", "")
	s2 := engine.OpenScope(s, "sub2", s1)
	s3 := engine.OpenScope(s, "sub3", s2)
	s4 := engine.OpenScope(s, "sub4", "")
	s.Tokens = []engine.Token{
		{ID: "t-root", NodeID: "n0", ScopeID: ""},
		{ID: "t1", NodeID: "n1", ScopeID: s1},
		{ID: "t2", NodeID: "n2", ScopeID: s2},
		{ID: "t3", NodeID: "n3", ScopeID: s3},
		{ID: "t4", NodeID: "n4", ScopeID: s4},
	}
	return s
}

func TestDescendantScopeIDs(t *testing.T) {
	t.Parallel()

	s := scopeTreeState(t)
	s1, s2, s3, s4 := s.Scopes[0].ID, s.Scopes[1].ID, s.Scopes[2].ID, s.Scopes[3].ID

	cases := []struct {
		name   string
		scope  string
		assert func(t *testing.T, got map[string]bool)
	}{
		{
			name:  "a mid-tree scope collects itself and its transitive children",
			scope: s1,
			assert: func(t *testing.T, got map[string]bool) {
				assert.True(t, got[s1])
				assert.True(t, got[s2], "child")
				assert.True(t, got[s3], "grandchild — the level closeScope's callers miss")
				assert.False(t, got[s4], "a sibling subtree is untouched")
			},
		},
		{
			name:  "a leaf collects only itself",
			scope: s3,
			assert: func(t *testing.T, got map[string]bool) {
				assert.True(t, got[s3])
				assert.False(t, got[s1])
				assert.False(t, got[s2])
			},
		},
		{
			name:  "the root collects every scope in the instance",
			scope: "",
			assert: func(t *testing.T, got map[string]bool) {
				assert.True(t, got[""], "the root itself, which has no Scope entry")
				assert.True(t, got[s1])
				assert.True(t, got[s2])
				assert.True(t, got[s3])
				assert.True(t, got[s4])
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, engine.DescendantScopeIDs(scopeTreeState(t), tc.scope))
		})
	}
}

func TestTokensInScopeSubtree(t *testing.T) {
	t.Parallel()

	s := scopeTreeState(t)
	s1, s3 := s.Scopes[0].ID, s.Scopes[2].ID

	cases := []struct {
		name   string
		call   func(s *engine.InstanceState, scopeID string) int
		scope  string
		assert func(t *testing.T, got int)
	}{
		{
			name:   "s1 plus its child and grandchild",
			call:   engine.TokensInScopeSubtree,
			scope:  s1,
			assert: func(t *testing.T, got int) { assert.Equal(t, 3, got) },
		},
		{
			name:   "a leaf counts only itself",
			call:   engine.TokensInScopeSubtree,
			scope:  s3,
			assert: func(t *testing.T, got int) { assert.Equal(t, 1, got) },
		},
		{
			name:   "the root counts everything",
			call:   engine.TokensInScopeSubtree,
			scope:  "",
			assert: func(t *testing.T, got int) { assert.Equal(t, 5, got) },
		},
		{
			name:  "contrast: the exact-match count still sees only the immediate scope",
			call:  engine.TokensInScope,
			scope: s1,
			assert: func(t *testing.T, got int) {
				assert.Equal(t, 1, got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, tc.call(s, tc.scope))
		})
	}
}

func TestHasChildScopeWithTokens(t *testing.T) {
	t.Parallel()

	s := scopeTreeState(t)
	s1, s2, s3 := s.Scopes[0].ID, s.Scopes[1].ID, s.Scopes[2].ID

	cases := []struct {
		name   string
		parent string
		except string
		setup  func(s *engine.InstanceState)
		assert func(t *testing.T, got bool)
	}{
		{
			name:   "a child holding a token is seen",
			parent: s1, except: "",
			assert: func(t *testing.T, got bool) { assert.True(t, got) },
		},
		{
			// ⚠ THIS is the row that pins the except filter. It must be a case where
			// the excepted child is the ONLY candidate: s1's sole child is s2, so
			// dropping `sc.ID != exceptID` from the implementation flips this to
			// true. The sibling-style case below cannot do that job — with
			// parent "" and except s1, s4 still holds a token, so the row returns
			// true whether or not the filter works. (Added 2026-08-03 after the
			// original case was flagged as mutation-weak.)
			name:   "the excepted child is not counted when it is the only candidate",
			parent: s1, except: s2,
			assert: func(t *testing.T, got bool) {
				assert.False(t, got, "s2 is excepted and s1 has no other child")
			},
		},
		{
			name:   "a non-excepted sibling still counts",
			parent: "", except: s1,
			assert: func(t *testing.T, got bool) {
				assert.True(t, got, "s4 is still a root child holding a token")
			},
		},
		{
			name:   "a leaf has no children",
			parent: s3, except: "",
			assert: func(t *testing.T, got bool) { assert.False(t, got) },
		},
		{
			name:   "a child whose OWN token is gone but whose GRANDCHILD holds one still counts",
			parent: s1, except: "",
			setup: func(st *engine.InstanceState) {
				// drop s2's own token, keep s3's
				kept := st.Tokens[:0]
				for _, tok := range st.Tokens {
					if tok.ScopeID != s2 {
						kept = append(kept, tok)
					}
				}
				st.Tokens = kept
			},
			assert: func(t *testing.T, got bool) {
				assert.True(t, got, "subtree, not exact match — this is the whole point")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := scopeTreeState(t)
			if tc.setup != nil {
				tc.setup(st)
			}
			tc.assert(t, engine.HasChildScopeWithTokens(st, tc.parent, tc.except))
		})
	}
}

func TestCloseScopeDescendantsKeepsTheScopeItself(t *testing.T) {
	t.Parallel()

	s := scopeTreeState(t)
	s1, s4 := s.Scopes[0].ID, s.Scopes[3].ID

	engine.CloseScopeDescendants(s, s1)

	ids := make([]string, 0, len(s.Scopes))
	for _, sc := range s.Scopes {
		ids = append(ids, sc.ID)
	}
	assert.Equal(t, []string{s1, s4}, ids,
		"s1 survives so the drain code can still detect its children; s2 and s3 do not")
}

// TestCloseScopeStillGuardsUnknownScope pins the asymmetry that makes this
// delivery safe: descendantScopeIDs has NO existence guard (scopeByID("") is
// always nil because the root scope is implicit, so guarding would make every
// root-level teardown a silent no-op), while closeScope keeps its own — without
// it, closeScope("") would become an instance-wide scope wipe.
func TestCloseScopeStillGuardsUnknownScope(t *testing.T) {
	t.Parallel()

	s := scopeTreeState(t)
	before := len(s.Scopes)

	engine.CloseScope(s, "")
	assert.Len(t, s.Scopes, before, `closeScope("") must remain a no-op`)

	engine.CloseScope(s, "no-such-scope")
	assert.Len(t, s.Scopes, before, "an unknown scope is a no-op")
}
