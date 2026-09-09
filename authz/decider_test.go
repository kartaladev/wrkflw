package authz_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
)

// No ctx modifier in these tables: none of the three deciders in this package
// reads its context — each discards it in its signature — so there is no
// cancellation path to exercise. A decider that performs I/O (a directory
// lookup, a remote policy fetch) must add one.

// TestPrivilegeDecider tables the privilege rule over
// (spec, actor, operation) → decision.
//
// Every Deny row is paired with an at-limit Allow row on purpose. This is
// authorization: a decider that returns Deny unconditionally satisfies every
// "refused" assertion, so the Allow and NotApplicable rows are what give the
// Deny rows their meaning.
func TestPrivilegeDecider(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		request authz.Request
		assert  func(t *testing.T, d authz.Decision, err error)
	}

	allow := func(t *testing.T, d authz.Decision, err error) {
		t.Helper()
		require.NoError(t, err)
		assert.Equal(t, authz.Allow, d)
	}
	deny := func(t *testing.T, d authz.Decision, err error) {
		t.Helper()
		require.NoError(t, err)
		assert.Equal(t, authz.Deny, d)
	}
	abstain := func(t *testing.T, d authz.Decision, err error) {
		t.Helper()
		require.NoError(t, err)
		assert.Equal(t, authz.NotApplicable, d)
	}

	cases := []testCase{
		{
			// ⚠ THE ROW THAT CATCHES #107's BUG CLASS, and the one most likely
			// to be waved through as trivial. A decider that reads the wrong
			// field — Actor.Privileges instead of Spec.Privileges, say —
			// abstains when it should decide, Composite falls through to "empty
			// spec allows", and the request is ALLOWED. Every "an authorized
			// actor is allowed" test still passes. That is exactly how the
			// original allow-all defect survived being closed as completed.
			//
			// NotApplicable and Allow are indistinguishable at the Composite
			// boundary, so this must be asserted HERE, against the constant, and
			// cannot be delegated to a Composite-level test.
			name: "nil spec privileges abstain even when the actor holds some",
			request: authz.Request{
				Operation: authz.OpClaim,
				Spec:      authz.AuthzSpec{},
				Actor:     authz.Actor{ID: "u1", Privileges: []string{"x"}},
			},
			assert: abstain,
		},
		{
			name: "empty (non-nil) spec privileges abstain even when the actor holds some",
			request: authz.Request{
				Operation: authz.OpClaim,
				Spec:      authz.AuthzSpec{Privileges: []string{}},
				Actor:     authz.Actor{ID: "u1", Privileges: []string{"x"}},
			},
			assert: abstain,
		},
		{
			name: "a spec expressing identity by role abstains here",
			request: authz.Request{
				Operation: authz.OpClaim,
				Spec:      authz.AuthzSpec{Roles: []string{"admin"}},
				Actor:     authz.Actor{ID: "u1", Privileges: []string{"finance-task claim"}},
			},
			assert: abstain,
		},
		{
			name: "exact match allows",
			request: authz.Request{
				Operation: authz.OpClaim,
				Spec:      authz.AuthzSpec{Privileges: []string{"finance-task claim"}},
				Actor:     authz.Actor{ID: "u1", Privileges: []string{"finance-task claim"}},
			},
			assert: allow,
		},
		{
			name: "any-of: one of several actor privileges matches",
			request: authz.Request{
				Operation: authz.OpComplete,
				Spec:      authz.AuthzSpec{Privileges: []string{"a", "b"}},
				Actor:     authz.Actor{ID: "u1", Privileges: []string{"c", "b"}},
			},
			assert: allow,
		},
		{
			name: "actor holds no matching privilege denies",
			request: authz.Request{
				Operation: authz.OpClaim,
				Spec:      authz.AuthzSpec{Privileges: []string{"finance-task claim"}},
				Actor:     authz.Actor{ID: "u1", Privileges: []string{"finance-task read"}},
			},
			assert: deny,
		},
		{
			name: "actor with no privileges at all denies",
			request: authz.Request{
				Operation: authz.OpClaim,
				Spec:      authz.AuthzSpec{Privileges: []string{"finance-task claim"}},
				Actor:     authz.Actor{ID: "u1"},
			},
			assert: deny,
		},
		{
			name: "matching is exact: no wildcard grammar",
			request: authz.Request{
				Operation: authz.OpClaim,
				Spec:      authz.AuthzSpec{Privileges: []string{"finance-task claim"}},
				Actor:     authz.Actor{ID: "u1", Privileges: []string{"finance-task *"}},
			},
			assert: deny,
		},
		{
			name: "matching is exact: no prefix hierarchy",
			request: authz.Request{
				Operation: authz.OpClaim,
				Spec:      authz.AuthzSpec{Privileges: []string{"a b"}},
				Actor:     authz.Actor{ID: "u1", Privileges: []string{"a"}},
			},
			assert: deny,
		},
		{
			name: "a role is not a privilege",
			request: authz.Request{
				Operation: authz.OpClaim,
				Spec:      authz.AuthzSpec{Privileges: []string{"finance-task claim"}},
				Actor:     authz.Actor{ID: "u1", Roles: []string{"finance-task claim"}},
			},
			assert: deny,
		},
		{
			name: "the decision does not depend on the operation",
			request: authz.Request{
				Operation: authz.OpReassign,
				Spec:      authz.AuthzSpec{Privileges: []string{"p"}},
				Actor:     authz.Actor{ID: "u1", Privileges: []string{"p"}},
				Task:      authz.TaskView{Claimed: true, ClaimantID: "someone-else"},
			},
			assert: allow,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, err := authz.PrivilegeDecider{}.Decide(t.Context(), tc.request)
			tc.assert(t, d, err)
		})
	}
}

// TestRoleDecider tables the role rule. It mirrors TestPrivilegeDecider because
// the two rules are deliberately identical in shape over different namespaces;
// the "a privilege is not a role" row is what pins that they stay separate.
func TestRoleDecider(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		request authz.Request
		assert  func(t *testing.T, d authz.Decision, err error)
	}

	allow := func(t *testing.T, d authz.Decision, err error) {
		t.Helper()
		require.NoError(t, err, "the role rule can never fail to evaluate")
		assert.Equal(t, authz.Allow, d)
	}
	deny := func(t *testing.T, d authz.Decision, err error) {
		t.Helper()
		require.NoError(t, err, "the role rule can never fail to evaluate")
		assert.Equal(t, authz.Deny, d)
	}
	abstain := func(t *testing.T, d authz.Decision, err error) {
		t.Helper()
		require.NoError(t, err, "the role rule can never fail to evaluate")
		assert.Equal(t, authz.NotApplicable, d)
	}

	cases := []testCase{
		{
			// The #107 catch row for roles — see the equivalent in
			// TestPrivilegeDecider for why abstaining wrongly reads as ALLOW.
			name: "nil spec roles abstain even when the actor holds some",
			request: authz.Request{
				Spec:  authz.AuthzSpec{},
				Actor: authz.Actor{ID: "u1", Roles: []string{"admin"}},
			},
			assert: abstain,
		},
		{
			name: "empty (non-nil) spec roles abstain even when the actor holds some",
			request: authz.Request{
				Spec:  authz.AuthzSpec{Roles: []string{}},
				Actor: authz.Actor{ID: "u1", Roles: []string{"admin"}},
			},
			assert: abstain,
		},
		{
			name: "a spec expressing identity by privilege abstains here",
			request: authz.Request{
				Spec:  authz.AuthzSpec{Privileges: []string{"p"}},
				Actor: authz.Actor{ID: "u1", Roles: []string{"admin"}},
			},
			assert: abstain,
		},
		{
			name: "exact match allows",
			request: authz.Request{
				Spec:  authz.AuthzSpec{Roles: []string{"admin", "editor"}},
				Actor: authz.Actor{ID: "u1", Roles: []string{"editor"}},
			},
			assert: allow,
		},
		{
			name: "actor holds no matching role denies",
			request: authz.Request{
				Spec:  authz.AuthzSpec{Roles: []string{"admin"}},
				Actor: authz.Actor{ID: "u1", Roles: []string{"viewer"}},
			},
			assert: deny,
		},
		{
			name: "actor with no roles at all denies",
			request: authz.Request{
				Spec:  authz.AuthzSpec{Roles: []string{"admin"}},
				Actor: authz.Actor{ID: "u1"},
			},
			assert: deny,
		},
		{
			name: "no role inheritance: a parent role does not imply a child",
			request: authz.Request{
				Spec:  authz.AuthzSpec{Roles: []string{"finance-clerk"}},
				Actor: authz.Actor{ID: "u1", Roles: []string{"finance"}},
			},
			assert: deny,
		},
		{
			name: "matching is exact: no prefix hierarchy",
			request: authz.Request{
				Spec:  authz.AuthzSpec{Roles: []string{"a b"}},
				Actor: authz.Actor{ID: "u1", Roles: []string{"a"}},
			},
			assert: deny,
		},
		{
			name: "a privilege is not a role",
			request: authz.Request{
				Spec:  authz.AuthzSpec{Roles: []string{"admin"}},
				Actor: authz.Actor{ID: "u1", Privileges: []string{"admin"}},
			},
			assert: deny,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, err := authz.RoleDecider{}.Decide(t.Context(), tc.request)
			tc.assert(t, d, err)
		})
	}
}

// TestAttributeDecider tables the predicate rule, whose discriminating pair is
// not allow-vs-deny but DENY-vs-ERROR.
//
// A predicate evaluating to false is a denial. A predicate that cannot be
// evaluated has decided nothing, and must return an error that does NOT satisfy
// errors.Is(err, ErrNotAuthorized) — because ErrNotAuthorized classifies 403,
// and that arm renders the whole error chain, including the predicate source,
// into the client's response body. See the AttributeDecider doc comment and
// TestRoleAuthorizer_PredicateFailureIsNotADenial.
func TestAttributeDecider(t *testing.T) {
	t.Parallel()

	// Deliberately recognisable: if an assertion below is weakened, this string
	// turning up where it should not is the tell.
	const secretPredicate = `actor.Attributes["internal_clearance_tier"]`

	type testCase struct {
		name    string
		request authz.Request
		assert  func(t *testing.T, d authz.Decision, err error)
	}

	cases := []testCase{
		{
			name:    "empty predicate abstains",
			request: authz.Request{Spec: authz.AuthzSpec{}, Actor: authz.Actor{ID: "u1"}},
			assert: func(t *testing.T, d authz.Decision, err error) {
				require.NoError(t, err)
				assert.Equal(t, authz.NotApplicable, d)
			},
		},
		{
			name: "predicate true allows",
			request: authz.Request{
				Spec:  authz.AuthzSpec{Attribute: `actor.ID == "u1"`},
				Actor: authz.Actor{ID: "u1"},
			},
			assert: func(t *testing.T, d authz.Decision, err error) {
				require.NoError(t, err)
				assert.Equal(t, authz.Allow, d)
			},
		},
		{
			name: "predicate false denies",
			request: authz.Request{
				Spec:  authz.AuthzSpec{Attribute: `actor.ID == "u1"`},
				Actor: authz.Actor{ID: "u2"},
			},
			assert: func(t *testing.T, d authz.Decision, err error) {
				require.NoError(t, err)
				assert.Equal(t, authz.Deny, d)
			},
		},
		{
			name: "the predicate can read process variables",
			request: authz.Request{
				Spec:  authz.AuthzSpec{Attribute: `vars["amount"] > 100`},
				Actor: authz.Actor{ID: "u1"},
				Vars:  map[string]any{"amount": 200},
			},
			assert: func(t *testing.T, d authz.Decision, err error) {
				require.NoError(t, err)
				assert.Equal(t, authz.Allow, d)
			},
		},
		{
			name: "the predicate can read actor privileges",
			request: authz.Request{
				Spec:  authz.AuthzSpec{Attribute: `"p" in actor.Privileges`},
				Actor: authz.Actor{ID: "u1", Privileges: []string{"p"}},
			},
			assert: func(t *testing.T, d authz.Decision, err error) {
				require.NoError(t, err)
				assert.Equal(t, authz.Allow, d)
			},
		},
		{
			name: "a predicate that does not compile errors and is not a denial",
			request: authz.Request{
				Spec:  authz.AuthzSpec{Attribute: `actor.ID ===== `},
				Actor: authz.Actor{ID: "u1"},
			},
			assert: func(t *testing.T, d authz.Decision, err error) {
				require.Error(t, err, "an unevaluable predicate must still fail closed")
				assert.NotErrorIs(t, err, authz.ErrNotAuthorized,
					"a broken policy has decided nothing; 403 renders the chain to the client")
				assert.Equal(t, authz.NotApplicable, d,
					"the Decision returned beside an error must be the abstaining zero value, "+
						"never Allow")
			},
		},
		{
			name: "a predicate that is not a bool errors and is not a denial",
			request: authz.Request{
				Spec:  authz.AuthzSpec{Attribute: secretPredicate},
				Actor: authz.Actor{ID: "u1", Attributes: map[string]any{"internal_clearance_tier": "SECRET-3"}},
			},
			assert: func(t *testing.T, d authz.Decision, err error) {
				require.Error(t, err)
				assert.NotErrorIs(t, err, authz.ErrNotAuthorized)
				assert.Equal(t, authz.NotApplicable, d)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, err := authz.AttributeDecider{}.Decide(t.Context(), tc.request)
			tc.assert(t, d, err)

			// Applies to EVERY row: an error classified as a denial reaches the
			// 403 arm, which renders err.Error() to the client, so no such error
			// may ever carry the policy expression.
			if err != nil && errors.Is(err, authz.ErrNotAuthorized) {
				assert.NotContains(t, err.Error(), secretPredicate)
			}
		})
	}
}
