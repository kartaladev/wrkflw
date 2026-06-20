// Package authz_test exercises the public API of the authz package.
package authz_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/authz"
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
