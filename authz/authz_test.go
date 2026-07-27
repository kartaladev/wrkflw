// Package authz_test exercises the public API of the authz package.
package authz_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/stretchr/testify/require"
)

func TestAllowAll(t *testing.T) {
	t.Parallel()

	a := authz.AllowAll{}
	spec := authz.AuthzSpec{Roles: []string{"admin"}}
	actor := authz.Actor{ID: "u1", Roles: []string{"viewer"}}
	err := a.Authorize(t.Context(), spec, actor, nil)
	require.NoError(t, err)
}

func TestRoleAuthorizer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		spec   authz.AuthzSpec
		actor  authz.Actor
		assert func(t *testing.T, err error)
	}{
		{
			name:  "empty spec roles always authorized",
			spec:  authz.AuthzSpec{},
			actor: authz.Actor{ID: "u1", Roles: []string{"viewer"}},
			assert: func(t *testing.T, err error) {
				t.Helper()
				require.NoError(t, err)
			},
		},
		{
			name:  "actor has matching role",
			spec:  authz.AuthzSpec{Roles: []string{"admin", "editor"}},
			actor: authz.Actor{ID: "u2", Roles: []string{"editor"}},
			assert: func(t *testing.T, err error) {
				t.Helper()
				require.NoError(t, err)
			},
		},
		{
			name:  "actor has no matching role",
			spec:  authz.AuthzSpec{Roles: []string{"admin"}},
			actor: authz.Actor{ID: "u3", Roles: []string{"viewer"}},
			assert: func(t *testing.T, err error) {
				t.Helper()
				require.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
		{
			name:  "actor has no roles at all",
			spec:  authz.AuthzSpec{Roles: []string{"admin"}},
			actor: authz.Actor{ID: "u4"},
			assert: func(t *testing.T, err error) {
				t.Helper()
				require.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ra := authz.RoleAuthorizer{}
			err := ra.Authorize(t.Context(), tc.spec, tc.actor, nil)
			tc.assert(t, err)
		})
	}
}

func TestRoleAuthorizer_AttributePredicate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		spec   authz.AuthzSpec
		actor  authz.Actor
		vars   map[string]any
		assert func(t *testing.T, err error)
	}{
		{
			name: "attribute predicate true allows",
			spec: authz.AuthzSpec{
				Roles:     []string{"approver"},
				Attribute: `actor.ID == "u5"`,
			},
			actor: authz.Actor{ID: "u5", Roles: []string{"approver"}},
			vars:  map[string]any{},
			assert: func(t *testing.T, err error) {
				t.Helper()
				require.NoError(t, err)
			},
		},
		{
			name: "attribute predicate false denies",
			spec: authz.AuthzSpec{
				Roles:     []string{"approver"},
				Attribute: `actor.ID == "u5"`,
			},
			actor: authz.Actor{ID: "u6", Roles: []string{"approver"}},
			vars:  map[string]any{},
			assert: func(t *testing.T, err error) {
				t.Helper()
				require.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
		{
			name: "attribute predicate uses vars",
			spec: authz.AuthzSpec{
				Roles:     []string{"approver"},
				Attribute: `vars["amount"] > 100`,
			},
			actor: authz.Actor{ID: "u7", Roles: []string{"approver"}},
			vars:  map[string]any{"amount": 200},
			assert: func(t *testing.T, err error) {
				t.Helper()
				require.NoError(t, err)
			},
		},
		{
			name: "malformed predicate wraps ErrNotAuthorized with eval error",
			spec: authz.AuthzSpec{
				Roles:     []string{"approver"},
				Attribute: `actor.ID ===`, // invalid syntax
			},
			actor: authz.Actor{ID: "u8", Roles: []string{"approver"}},
			vars:  map[string]any{},
			assert: func(t *testing.T, err error) {
				t.Helper()
				require.Error(t, err)
				require.True(t, errors.Is(err, authz.ErrNotAuthorized), "expected ErrNotAuthorized to be wrapped")
				require.NotEmpty(t, err.Error(), "error message should describe the predicate failure")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ra := authz.RoleAuthorizer{}
			err := ra.Authorize(t.Context(), tc.spec, tc.actor, tc.vars)
			tc.assert(t, err)
		})
	}
}

// TestActorClone verifies that Clone produces an independently allocated copy:
// mutating the clone's Roles or Attributes must not affect the original. Actors
// are stored in human-task audit records (candidates, claim, completion) that are
// deep-copied across engine and cache boundaries, so aliasing here would leak
// mutations between a cached value and its caller.
func TestActorClone(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		actor  authz.Actor
		assert func(t *testing.T, orig, clone authz.Actor)
	}

	cases := []testCase{
		{
			name:  "roles are independently allocated",
			actor: authz.Actor{ID: "u-jane", Roles: []string{"manager"}},
			assert: func(t *testing.T, orig, clone authz.Actor) {
				clone.Roles[0] = "mutated"
				require.Equal(t, "manager", orig.Roles[0])
				require.Equal(t, "u-jane", clone.ID)
			},
		},
		{
			name:  "attributes are independently allocated",
			actor: authz.Actor{ID: "u-jane", Attributes: map[string]any{"email": "jane@acme.com"}},
			assert: func(t *testing.T, orig, clone authz.Actor) {
				clone.Attributes["email"] = "mutated"
				require.Equal(t, "jane@acme.com", orig.Attributes["email"])
			},
		},
		{
			name:  "nil slices and maps stay nil",
			actor: authz.Actor{ID: "u-jane"},
			assert: func(t *testing.T, _, clone authz.Actor) {
				require.Nil(t, clone.Roles)
				require.Nil(t, clone.Attributes)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, tc.actor, tc.actor.Clone())
		})
	}
}

// TestActorJSONWireShape pins the actor's wire form to {id, roles, attributes}.
// The human-task audit renders actors by faithful passthrough (ADR-0147), so the
// actor type itself carries the wire contract rather than each view re-mapping it.
// Empty roles and attributes are omitted so an ID-only actor stays compact.
func TestActorJSONWireShape(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		actor  authz.Actor
		assert func(t *testing.T, got string)
	}

	cases := []testCase{
		{
			name: "full actor renders snake-case keys",
			actor: authz.Actor{
				ID:         "u-jane",
				Roles:      []string{"manager"},
				Attributes: map[string]any{"email": "jane@acme.com"},
			},
			assert: func(t *testing.T, got string) {
				require.JSONEq(t, `{"id":"u-jane","roles":["manager"],"attributes":{"email":"jane@acme.com"}}`, got)
			},
		},
		{
			name:  "id-only actor omits empty roles and attributes",
			actor: authz.Actor{ID: "u-jane"},
			assert: func(t *testing.T, got string) {
				require.JSONEq(t, `{"id":"u-jane"}`, got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, err := json.Marshal(tc.actor)
			require.NoError(t, err)
			tc.assert(t, string(b))

			// Journalled actors predate these tags and were written with Go's
			// default PascalCase keys; case-insensitive matching must still decode
			// them, so replay of an existing journal is unaffected.
			var legacy authz.Actor
			require.NoError(t, json.Unmarshal([]byte(`{"ID":"u-legacy","Roles":["r"]}`), &legacy))
			require.Equal(t, "u-legacy", legacy.ID)
			require.Equal(t, []string{"r"}, legacy.Roles)
		})
	}
}
