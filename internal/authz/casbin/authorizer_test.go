package casbin_test

import (
	"testing"

	casbinv2 "github.com/casbin/casbin/v2"
	casbinmodel "github.com/casbin/casbin/v2/model"
	stringadapter "github.com/casbin/casbin/v2/persist/string-adapter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	casbinauthz "github.com/kartaladev/wrkflw/internal/authz/casbin"
)

const testModel = `
[request_definition]
r = sub, obj, act
[policy_definition]
p = sub, obj, act
[role_definition]
g = _, _
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = g(r.sub, p.sub) && (r.obj == p.obj || p.obj == "*") && (r.act == p.act || p.act == "*")
`

// alice is a manager; manager inherits employee; manager may approve anything.
const testPolicy = `
p, manager, approve, *
p, employee, doc, read
g, alice, manager
g, manager, employee
`

func newEnforcer(t *testing.T) *casbinv2.SyncedEnforcer {
	t.Helper()
	m, err := casbinmodel.NewModelFromString(testModel)
	require.NoError(t, err)
	e, err := casbinv2.NewSyncedEnforcer(m, stringadapter.NewAdapter(testPolicy))
	require.NoError(t, err)
	return e
}

func TestAuthorizer_Authorize(t *testing.T) {
	alice := authz.Actor{ID: "alice", Roles: []string{"manager"}, Attributes: map[string]any{"region": "EU"}}
	bob := authz.Actor{ID: "bob", Roles: []string{"employee"}}

	cases := map[string]struct {
		spec   authz.AuthzSpec
		actor  authz.Actor
		vars   map[string]any
		assert func(t *testing.T, err error)
	}{
		"role hierarchy: manager satisfies employee": {
			spec:   authz.AuthzSpec{Roles: []string{"employee"}},
			actor:  alice,
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		"role deny: employee does not satisfy manager": {
			spec:  authz.AuthzSpec{Roles: []string{"manager"}},
			actor: bob,
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
		"empty roles: no restriction": {
			spec:   authz.AuthzSpec{},
			actor:  bob,
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		"privilege allow: manager may approve": {
			spec:   authz.AuthzSpec{Privileges: []string{"approve"}},
			actor:  alice,
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		"privilege deny: employee may not approve": {
			spec:  authz.AuthzSpec{Privileges: []string{"approve"}},
			actor: bob,
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
		"two-token privilege allow: employee may doc read": {
			spec:   authz.AuthzSpec{Privileges: []string{"doc read"}},
			actor:  bob,
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		"two-token privilege deny: employee may not doc write": {
			spec:  authz.AuthzSpec{Privileges: []string{"doc write"}},
			actor: bob,
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
		"attribute allow over actor": {
			spec:   authz.AuthzSpec{Attribute: `actor.Attributes["region"] == "EU"`},
			actor:  alice,
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		"attribute deny over vars": {
			spec:  authz.AuthzSpec{Attribute: `vars["region"] == "EU"`},
			actor: alice,
			vars:  map[string]any{"region": "US"},
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
		"attribute allow over vars": {
			spec:   authz.AuthzSpec{Attribute: `vars["region"] == "EU"`},
			actor:  alice,
			vars:   map[string]any{"region": "EU"},
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		"combined role+privilege+attribute allow": {
			spec: authz.AuthzSpec{
				Roles:      []string{"employee"},
				Privileges: []string{"approve"},
				Attribute:  `vars["region"] == "EU"`,
			},
			actor:  alice,
			vars:   map[string]any{"region": "EU"},
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		// ⚠ This row asserted the OPPOSITE until #69: that a malformed predicate
		// maps to ErrNotAuthorized. It was changed deliberately, not to make a
		// new test pass.
		//
		// ErrNotAuthorized classifies 403, and that arm renders the whole
		// wrapped chain into the client's body — while every evaluator error
		// embeds the expression source verbatim, caret diagram included. So the
		// old contract handed a denied caller the deployment's authorization
		// rule. It was also wrong on its own terms: a predicate that will not
		// compile has decided nothing, so calling it a denial claims a fact the
		// code does not have.
		//
		// It still fails CLOSED — the error is non-nil and every caller treats
		// any error from Authorize as a refusal — it simply classifies 500 now,
		// so the detail reaches operators through logs instead of the response.
		"malformed attribute predicate fails closed without claiming a denial": {
			spec:  authz.AuthzSpec{Attribute: `this is not (valid expr`},
			actor: alice,
			assert: func(t *testing.T, err error) {
				require.Error(t, err, "an unevaluable predicate must still refuse")
				assert.NotErrorIs(t, err, authz.ErrNotAuthorized,
					"a broken policy is not an authorization decision, and 403 renders "+
						"the chain — which carries the expression source")
				assert.NotContains(t, err.Error(), "not authorized",
					"must not read as a denial to a consumer matching on text either")
			},
		},
		"empty privilege token is skipped, no match denies": {
			spec:  authz.AuthzSpec{Privileges: []string{""}},
			actor: alice,
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			a := casbinauthz.New(newEnforcer(t))
			err := a.Authorize(t.Context(), tc.spec, tc.actor, tc.vars)
			tc.assert(t, err)
		})
	}
}

func TestAuthorizer_ImplementsPort(t *testing.T) {
	var _ authz.Authorizer = casbinauthz.New(newEnforcer(t))
	// Real denial case: verify that a denial from Authorize returns ErrNotAuthorized.
	a := casbinauthz.New(newEnforcer(t))
	bob := authz.Actor{ID: "bob", Roles: []string{"employee"}}
	err := a.Authorize(t.Context(), authz.AuthzSpec{Roles: []string{"manager"}}, bob, nil)
	assert.ErrorIs(t, err, authz.ErrNotAuthorized)
}
