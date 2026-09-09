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

// TestDeciderSpecCoverageDeclarations asserts what each decider DECLARES it
// reads, directly, against the exact field constants.
//
// ⚠ Why this is asserted at the declaration and not inferred through
// [authz.Composite]: a decider that lies about its coverage is invisible
// end-to-end. Make RoleDecider claim FieldPrivileges as well — a plausible
// copy-paste — and the whole package stays green while
//
//	Composite{Identity: [RoleDecider{}]}, spec{Privileges: [...]}, actor holding none
//
// is ALLOWED: the false claim satisfies checkCoverage, RoleDecider then abstains
// because Spec.Roles is empty, and the combiner falls through to "an empty spec
// allows". That is #107 arriving through the very mechanism built to stop it.
//
// This is round 1's own lesson one level down. The NotApplicable rows above are
// asserted against the exact Decision constant rather than inferred from what
// Composite concludes; a coverage declaration is foundational in the same way
// and gets the same treatment.
//
// The negative assertions are the load-bearing half: a decider claiming MORE
// than it reads is the direction that fails open.
func TestDeciderSpecCoverageDeclarations(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		reader authz.SpecReader
		assert func(t *testing.T, got []authz.SpecField)
	}

	cases := []testCase{
		{
			name:   "PrivilegeDecider declares privileges and nothing else",
			reader: authz.PrivilegeDecider{},
			assert: func(t *testing.T, got []authz.SpecField) {
				assert.Equal(t, []authz.SpecField{authz.FieldPrivileges}, got)
			},
		},
		{
			name:   "RoleDecider declares roles and nothing else",
			reader: authz.RoleDecider{},
			assert: func(t *testing.T, got []authz.SpecField) {
				assert.Equal(t, []authz.SpecField{authz.FieldRoles}, got)
			},
		},
		{
			name:   "AttributeDecider declares the attribute and nothing else",
			reader: authz.AttributeDecider{},
			assert: func(t *testing.T, got []authz.SpecField) {
				assert.Equal(t, []authz.SpecField{authz.FieldAttribute}, got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, tc.reader.ReadsSpecFields())
		})
	}
}

// TestDeciderDeclarationMatchesBehaviour binds each declaration to the
// behaviour it promises, closing the gap the assertions above cannot see on
// their own: that a decider declaring a field actually DECIDES on it, and that
// it abstains on every field it does not declare.
//
// Together the two tests pin both directions — declaring too much (fails open
// through checkCoverage) and declaring too little (fails closed with a spurious
// ErrSpecNotEvaluable).
func TestDeciderDeclarationMatchesBehaviour(t *testing.T) {
	t.Parallel()

	// A spec that sets every field at once, and an actor satisfying none of
	// them, so a decider that reads its declared field must Deny and one that
	// does not must abstain.
	spec := authz.AuthzSpec{
		Roles:      []string{"a-role"},
		Privileges: []string{"a-privilege"},
		Attribute:  `1 == 2`,
	}

	type testCase struct {
		name    string
		decider authz.Decider
	}

	cases := []testCase{
		{name: "PrivilegeDecider", decider: authz.PrivilegeDecider{}},
		{name: "RoleDecider", decider: authz.RoleDecider{}},
		{name: "AttributeDecider", decider: authz.AttributeDecider{}},
	}

	// fieldSetters blanks one spec field at a time, so each decider can be shown
	// to go NotApplicable for exactly the fields it does not declare.
	blank := map[authz.SpecField]func(s authz.AuthzSpec) authz.AuthzSpec{
		authz.FieldRoles:      func(s authz.AuthzSpec) authz.AuthzSpec { s.Roles = nil; return s },
		authz.FieldPrivileges: func(s authz.AuthzSpec) authz.AuthzSpec { s.Privileges = nil; return s },
		authz.FieldAttribute:  func(s authz.AuthzSpec) authz.AuthzSpec { s.Attribute = ""; return s },
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reader, ok := tc.decider.(authz.SpecReader)
			require.True(t, ok, "every decider in this package declares its coverage")
			declared := reader.ReadsSpecFields()
			require.Len(t, declared, 1, "each decider here reads exactly one field")

			// With its declared field SET, it must reach a decision.
			d, err := tc.decider.Decide(t.Context(), authz.Request{
				Operation: authz.OpClaim,
				Spec:      spec,
				Actor:     authz.Actor{ID: "u1"},
				Vars:      map[string]any{},
			})
			require.NoError(t, err)
			assert.Equal(t, authz.Deny, d,
				"the actor satisfies none of the fields, so the declared field must DENY — "+
					"a decider that declares a field it does not read would abstain here, "+
					"and checkCoverage would have been satisfied by a claim it cannot honour")

			// With its declared field BLANK, it must abstain — proving it reads
			// that field and not one of the others left standing.
			narrowed := blank[declared[0]](spec)
			d, err = tc.decider.Decide(t.Context(), authz.Request{
				Operation: authz.OpClaim,
				Spec:      narrowed,
				Actor:     authz.Actor{ID: "u1"},
				Vars:      map[string]any{},
			})
			require.NoError(t, err)
			assert.Equal(t, authz.NotApplicable, d,
				"with its declared field blank it must abstain, even though the OTHER "+
					"two fields are still set — that is what proves the declaration names "+
					"the field this decider actually reads")
		})
	}
}
