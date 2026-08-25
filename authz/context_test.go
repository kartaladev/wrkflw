package authz_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
)

func TestActorContextRoundTrip(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		ctx    func(t *testing.T) context.Context
		assert func(t *testing.T, got authz.Actor, ok bool)
	}{
		"bare context carries no actor": {
			ctx: func(t *testing.T) context.Context { return t.Context() },
			assert: func(t *testing.T, got authz.Actor, ok bool) {
				assert.False(t, ok, "a bare context must not yield an actor")
				assert.Equal(t, authz.Actor{}, got)
			},
		},
		"actor round-trips whole": {
			ctx: func(t *testing.T) context.Context {
				return authz.ContextWithActor(t.Context(), authz.Actor{
					ID:         "alice",
					Roles:      []string{"manager"},
					Attributes: map[string]any{"dept": "finance"},
				})
			},
			assert: func(t *testing.T, got authz.Actor, ok bool) {
				require.True(t, ok)
				assert.Equal(t, "alice", got.ID)
				assert.Equal(t, []string{"manager"}, got.Roles)
				assert.Equal(t, "finance", got.Attributes["dept"])
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, ok := authz.ActorFromContext(tc.ctx(t))
			tc.assert(t, got, ok)
		})
	}
}

// TestContextWithActorClonesOnTheWayIn pins that a caller mutating the actor it
// handed over cannot change what the engine later reads.
//
// FAILS WITHOUT THE IN CLONE: ContextWithActor storing `a` verbatim shares the
// Roles backing array, so roles[0] = "admin" becomes visible.
func TestContextWithActorClonesOnTheWayIn(t *testing.T) {
	t.Parallel()

	roles := []string{"manager"}
	ctx := authz.ContextWithActor(t.Context(), authz.Actor{ID: "alice", Roles: roles})

	roles[0] = "admin"

	got, ok := authz.ActorFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, []string{"manager"}, got.Roles, "top-level Roles must be cloned on the way IN")
}

// TestActorFromContextClonesOnTheWayOut pins the OTHER direction, which two
// earlier revisions of this test could not detect: mutating what you GOT must not
// change what the NEXT caller gets.
//
// ⚠ A test that mutates the CALLER's slice can only ever exercise the IN clone.
// This one needs a second read. FAILS WITHOUT THE OUT CLONE.
func TestActorFromContextClonesOnTheWayOut(t *testing.T) {
	t.Parallel()

	ctx := authz.ContextWithActor(t.Context(), authz.Actor{
		ID: "alice", Roles: []string{"manager"},
	})

	first, ok := authz.ActorFromContext(ctx)
	require.True(t, ok)
	first.Roles[0] = "admin" // mutate what we were handed

	second, ok := authz.ActorFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, []string{"manager"}, second.Roles, "each read must get its own copy")
}

// TestActorContextCloneDepth pins what the seam ACTUALLY guarantees. Actor.Clone
// clones Attributes ONE LEVEL DEEP by its own godoc, so a nested value stays
// shared. ADR-0189 states this rather than claiming full isolation — and the
// transport seam deep-copies separately, which is what makes the per-request
// marshal safe.
func TestActorContextCloneDepth(t *testing.T) {
	t.Parallel()

	nested := map[string]any{"team": "finance"}
	ctx := authz.ContextWithActor(t.Context(), authz.Actor{
		ID: "alice", Attributes: map[string]any{"profile": nested},
	})

	nested["team"] = "hacked" // nested values ARE shared — that is the contract

	got, ok := authz.ActorFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, "hacked", got.Attributes["profile"].(map[string]any)["team"],
		"nested attribute values are SHARED — one-level clone, per Actor.Clone's godoc")
}
