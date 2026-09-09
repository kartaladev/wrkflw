package authz_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
)

// stubDecider is a programmable [authz.Decider] used to exercise the combiner
// itself rather than the rules the default deciders implement. It records every
// Request it was consulted with.
//
// ⚠ It RECORDS rather than logs. `go test` swallows output from a passing
// package, so an instrumentation probe that prints reads as zero and looks
// exactly like a clean result. Every consultation below is asserted, never
// printed.
type stubDecider struct {
	decision authz.Decision
	err      error
	seen     *[]authz.Request
}

func (s stubDecider) Decide(_ context.Context, r authz.Request) (authz.Decision, error) {
	if s.seen != nil {
		*s.seen = append(*s.seen, r)
	}
	return s.decision, s.err
}

// TestCompositeCombiner tables the two-stage combiner over specs that reach it
// through the default [authz.NewComposite].
//
// The Allow rows are as load-bearing as the Deny rows: a Composite that refused
// everything, or one whose deciders were never consulted at all, would satisfy
// half this table on its own.
func TestCompositeCombiner(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		request authz.Request
		assert  func(t *testing.T, err error)
	}

	allowed := func(t *testing.T, err error) {
		t.Helper()
		require.NoError(t, err)
	}
	refused := func(t *testing.T, err error) {
		t.Helper()
		require.ErrorIs(t, err, authz.ErrNotAuthorized)
	}

	cases := []testCase{
		{
			name:    "an empty spec allows",
			request: authz.Request{Operation: authz.OpClaim, Actor: authz.Actor{ID: "u1"}},
			assert:  allowed,
		},
		{
			name: "privileges-only: matching privilege allows",
			request: authz.Request{
				Operation: authz.OpClaim,
				Spec:      authz.AuthzSpec{Privileges: []string{"finance-task claim"}},
				Actor:     authz.Actor{ID: "u1", Privileges: []string{"finance-task claim"}},
			},
			assert: allowed,
		},
		{
			name: "privileges-only: no matching privilege refuses",
			request: authz.Request{
				Operation: authz.OpClaim,
				Spec:      authz.AuthzSpec{Privileges: []string{"finance-task claim"}},
				Actor:     authz.Actor{ID: "u1", Privileges: []string{"other"}},
			},
			assert: refused,
		},
		{
			name: "roles-only: matching role allows",
			request: authz.Request{
				Operation: authz.OpClaim,
				Spec:      authz.AuthzSpec{Roles: []string{"approver"}},
				Actor:     authz.Actor{ID: "u1", Roles: []string{"approver"}},
			},
			assert: allowed,
		},
		{
			name: "roles-only: no matching role refuses",
			request: authz.Request{
				Operation: authz.OpClaim,
				Spec:      authz.AuthzSpec{Roles: []string{"approver"}},
				Actor:     authz.Actor{ID: "u1", Roles: []string{"viewer"}},
			},
			assert: refused,
		},
		{
			name: "attribute alone: predicate true allows",
			request: authz.Request{
				Operation: authz.OpComplete,
				Spec:      authz.AuthzSpec{Attribute: `vars["region"] == "EU"`},
				Actor:     authz.Actor{ID: "u1"},
				Vars:      map[string]any{"region": "EU"},
			},
			assert: allowed,
		},
		{
			name: "attribute alone: predicate false refuses",
			request: authz.Request{
				Operation: authz.OpComplete,
				Spec:      authz.AuthzSpec{Attribute: `vars["region"] == "EU"`},
				Actor:     authz.Actor{ID: "u1"},
				Vars:      map[string]any{"region": "US"},
			},
			assert: refused,
		},
		{
			name: "identity allows but a constraint denies: refused",
			request: authz.Request{
				Operation: authz.OpComplete,
				Spec: authz.AuthzSpec{
					Roles:     []string{"approver"},
					Attribute: `actor.ID == "someone-else"`,
				},
				Actor: authz.Actor{ID: "u1", Roles: []string{"approver"}},
				Vars:  map[string]any{},
			},
			assert: refused,
		},
		{
			name: "identity denies even though the constraint would allow",
			request: authz.Request{
				Operation: authz.OpComplete,
				Spec: authz.AuthzSpec{
					Roles:     []string{"approver"},
					Attribute: `actor.ID == "u1"`,
				},
				Actor: authz.Actor{ID: "u1", Roles: []string{"viewer"}},
				Vars:  map[string]any{},
			},
			assert: refused,
		},
		{
			name: "privilege identity plus a satisfied constraint allows",
			request: authz.Request{
				Operation: authz.OpComplete,
				Spec: authz.AuthzSpec{
					Privileges: []string{"finance-task claim"},
					Attribute:  `vars["amount"] < 1000`,
				},
				Actor: authz.Actor{ID: "u1", Privileges: []string{"finance-task claim"}},
				Vars:  map[string]any{"amount": 10},
			},
			assert: allowed,
		},
		{
			// FIRST-APPLICABLE, pinned. PrivilegeDecider precedes RoleDecider, so
			// a spec setting both consults only the privilege rule and the role
			// is never read. That is the reason model.Validate rejects the
			// combination with ErrRolesAndPrivileges rather than letting an
			// authoring mistake silently drop a field.
			name: "first-applicable: privileges decide and roles are never consulted",
			request: authz.Request{
				Operation: authz.OpClaim,
				Spec: authz.AuthzSpec{
					Roles:      []string{"approver"},
					Privileges: []string{"finance-task claim"},
				},
				Actor: authz.Actor{ID: "u1", Roles: []string{"approver"}},
			},
			assert: refused,
		},
		{
			name: "first-applicable: the privilege match wins without any role",
			request: authz.Request{
				Operation: authz.OpClaim,
				Spec: authz.AuthzSpec{
					Roles:      []string{"approver"},
					Privileges: []string{"finance-task claim"},
				},
				Actor: authz.Actor{ID: "u1", Privileges: []string{"finance-task claim"}},
			},
			assert: allowed,
		},
		{
			name: "an unevaluable predicate refuses without claiming a denial",
			request: authz.Request{
				Operation: authz.OpComplete,
				Spec:      authz.AuthzSpec{Attribute: `actor.ID ===== `},
				Actor:     authz.Actor{ID: "u1"},
			},
			assert: func(t *testing.T, err error) {
				require.Error(t, err, "a broken rule must still fail closed")
				assert.NotErrorIs(t, err, authz.ErrNotAuthorized,
					"a rule that could not be evaluated has decided nothing")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := authz.NewComposite().Authorize(t.Context(), tc.request)
			tc.assert(t, err)

			// Applies to EVERY row. A denial classifies 403, whose arm renders
			// the whole error chain into the client's response body, so a
			// refusal must carry no policy detail at all — not the predicate,
			// not the roles, not which decider objected.
			if err != nil && errors.Is(err, authz.ErrNotAuthorized) {
				assert.Equal(t, authz.ErrNotAuthorized, err,
					"a refusal must be bare ErrNotAuthorized: anything wrapped around "+
						"it is rendered to the refused caller by the 403 arm")
			}
		})
	}
}

// TestCompositeZeroValueFailsClosed pins the direction of the zero value.
//
// A Composite with no deciders reaches the end of both stages with no opinion.
// Under the combiner's own rules that is "allow", which is precisely the shape of
// the defect this port exists to fix — so it is refused instead, and refused as a
// configuration error rather than as an authorization decision.
func TestCompositeZeroValueFailsClosed(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		composite authz.Composite
		spec      authz.AuthzSpec
		assert    func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name:      "the zero Composite refuses",
			composite: authz.Composite{},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, authz.ErrNoDeciders)
				assert.NotErrorIs(t, err, authz.ErrNotAuthorized,
					"a composite with nothing to consult has decided nothing; "+
						"it is a misconfiguration, not a denial")
			},
		},
		{
			// ⚠ AT-LIMIT ACCEPT, and it carries a NON-EMPTY spec on purpose.
			// This row previously authorized an EMPTY spec, which made it pass
			// under both the correct implementation and the fail-open one that
			// shipped in this PR's first round — a non-discriminating test in
			// exactly the class this change exists to close. The spec must
			// exercise the field the installed decider actually reads.
			name:      "constraint-only is legitimate when the spec only needs a constraint",
			composite: authz.Composite{Constraint: []authz.Decider{authz.AttributeDecider{}}},
			spec:      authz.AuthzSpec{Attribute: `actor.ID == "u1"`},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err,
					"a composite with SOME deciders must not be swept up by the "+
						"zero-value guard when it covers every field the spec sets")
			},
		},
		{
			name:      "identity-only is legitimate when the spec only needs an identity rule",
			composite: authz.Composite{Identity: []authz.Decider{authz.PrivilegeDecider{}}},
			spec:      authz.AuthzSpec{Privileges: []string{"p"}},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.composite.Authorize(t.Context(), authz.Request{
				Operation: authz.OpClaim,
				Spec:      tc.spec,
				Actor:     authz.Actor{ID: "u1", Privileges: []string{"p"}},
			})
			tc.assert(t, err)
		})
	}
}

// TestCompositeRefusesASpecItCannotEvaluate pins the invariant that a partially
// populated Composite must not silently allow.
//
// ⚠ This is #107 one level up from where this PR closed it. A Composite holding
// SOME deciders — which NewComposite's own godoc invites, since the fields are
// exported — used to ignore every spec field none of its deciders reads, let
// every decider abstain, and fall through to "an empty spec allows". A
// Privileges-only spec against a composite with no privilege decider was
// allowed: the original defect, reproduced verbatim in the code written to fix
// it. Found independently by both round-1 reviewers.
//
// The outcome is an ERROR, not Deny, and that is #69's rule one level up: a spec
// the authorizer cannot evaluate has determined NOTHING, so calling it a denial
// claims a fact this code does not have. It classifies 500, not 403, so no
// policy text reaches the refused caller. It still fails CLOSED — every caller
// treats a non-nil error as a refusal.
//
// Every REFUSE row below is paired with an at-limit ACCEPT that differs only in
// the composite covering the field. Without those, an implementation that
// errored on every partial composite would satisfy the whole table.
func TestCompositeRefusesASpecItCannotEvaluate(t *testing.T) {
	t.Parallel()

	identityOnly := authz.Composite{Identity: []authz.Decider{
		authz.PrivilegeDecider{}, authz.RoleDecider{},
	}}
	privilegeOnly := authz.Composite{Identity: []authz.Decider{authz.PrivilegeDecider{}}}
	constraintOnly := authz.Composite{Constraint: []authz.Decider{authz.AttributeDecider{}}}

	type testCase struct {
		name      string
		composite authz.Composite
		spec      authz.AuthzSpec
		actor     authz.Actor
		assert    func(t *testing.T, err error)
	}

	unevaluable := func(field string) func(t *testing.T, err error) {
		return func(t *testing.T, err error) {
			t.Helper()
			require.Error(t, err, "a spec field nothing evaluates must not be allowed")
			assert.ErrorIs(t, err, authz.ErrSpecNotEvaluable)
			assert.NotErrorIs(t, err, authz.ErrNotAuthorized,
				"a spec the authorizer cannot evaluate has decided nothing; "+
					"reporting it as a denial routes it to the 403 arm, which "+
					"renders the whole chain to the client")
			assert.Contains(t, err.Error(), field,
				"the operator-facing diagnostic must name the unevaluated field")
		}
	}
	allowed := func(t *testing.T, err error) {
		t.Helper()
		require.NoError(t, err)
	}

	cases := []testCase{
		{
			// The original defect, verbatim: a Privileges-only spec allowed by a
			// composite that installs no privilege decider.
			name:      "REFUSE: privileges set, no decider reads them",
			composite: constraintOnly,
			spec:      authz.AuthzSpec{Privileges: []string{"finance-task claim"}},
			actor:     authz.Actor{ID: "attacker"},
			assert:    unevaluable("privileges"),
		},
		{
			name:      "ACCEPT: privileges set, a decider reads them",
			composite: privilegeOnly,
			spec:      authz.AuthzSpec{Privileges: []string{"finance-task claim"}},
			actor:     authz.Actor{ID: "u1", Privileges: []string{"finance-task claim"}},
			assert:    allowed,
		},
		{
			// The sharper one: the predicate would have evaluated to FALSE. Not
			// abstained — evaluated-to-deny, had anyone evaluated it.
			name:      "REFUSE: attribute set, no decider reads it",
			composite: identityOnly,
			spec:      authz.AuthzSpec{Roles: []string{"admin"}, Attribute: `1 == 2`},
			actor:     authz.Actor{ID: "u1", Roles: []string{"admin"}},
			assert:    unevaluable("attribute"),
		},
		{
			name:      "ACCEPT: attribute set, a decider reads it",
			composite: authz.NewComposite(),
			spec:      authz.AuthzSpec{Roles: []string{"admin"}, Attribute: `1 == 1`},
			actor:     authz.Actor{ID: "u1", Roles: []string{"admin"}},
			assert:    allowed,
		},
		{
			name:      "REFUSE: roles set, no decider reads them",
			composite: privilegeOnly,
			spec:      authz.AuthzSpec{Roles: []string{"admin"}},
			actor:     authz.Actor{ID: "u1", Roles: []string{"admin"}},
			assert:    unevaluable("roles"),
		},
		{
			name:      "ACCEPT: roles set, a decider reads them",
			composite: identityOnly,
			spec:      authz.AuthzSpec{Roles: []string{"admin"}},
			actor:     authz.Actor{ID: "u1", Roles: []string{"admin"}},
			assert:    allowed,
		},
		{
			// An empty spec asks nothing of the authorizer, so a partial
			// composite is not a misconfiguration for it.
			name:      "ACCEPT: an empty spec needs no coverage at all",
			composite: privilegeOnly,
			spec:      authz.AuthzSpec{},
			actor:     authz.Actor{ID: "u1"},
			assert:    allowed,
		},
		{
			name:      "ACCEPT: the default composite covers every field at once",
			composite: authz.NewComposite(),
			spec: authz.AuthzSpec{
				Privileges: []string{"p"},
				Attribute:  `actor.ID == "u1"`,
			},
			actor:  authz.Actor{ID: "u1", Privileges: []string{"p"}},
			assert: allowed,
		},
		{
			// Coverage is not authorization: a covered field that DENIES must
			// still deny, not turn into a configuration error.
			name:      "a covered field that denies is still a denial, not a config error",
			composite: authz.NewComposite(),
			spec:      authz.AuthzSpec{Privileges: []string{"p"}},
			actor:     authz.Actor{ID: "u1", Privileges: []string{"other"}},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, authz.ErrNotAuthorized)
				assert.NotErrorIs(t, err, authz.ErrSpecNotEvaluable)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.composite.Authorize(t.Context(), authz.Request{
				Operation: authz.OpClaim,
				Spec:      tc.spec,
				Actor:     tc.actor,
				Vars:      map[string]any{},
			})
			tc.assert(t, err)
		})
	}
}

// TestCompositeConsultation asserts what the combiner actually DOES with its
// deciders — which ones it consults, in what order, and with what Request —
// rather than only what it concludes. A Composite that never consulted anything
// would satisfy every "allowed" row of TestCompositeCombiner.
func TestCompositeConsultation(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		build  func(seen *[]authz.Request) authz.Composite
		assert func(t *testing.T, seen []authz.Request, err error)
	}

	cases := []testCase{
		{
			name: "an abstaining identity decider falls through to the next one",
			build: func(seen *[]authz.Request) authz.Composite {
				return authz.Composite{Identity: []authz.Decider{
					stubDecider{decision: authz.NotApplicable, seen: seen},
					stubDecider{decision: authz.Allow, seen: seen},
				}}
			},
			assert: func(t *testing.T, seen []authz.Request, err error) {
				require.NoError(t, err)
				assert.Len(t, seen, 2, "both identity deciders must be consulted")
			},
		},
		{
			name: "the first applicable identity decider short-circuits the rest",
			build: func(seen *[]authz.Request) authz.Composite {
				return authz.Composite{Identity: []authz.Decider{
					stubDecider{decision: authz.Allow, seen: seen},
					stubDecider{decision: authz.Deny, seen: seen},
				}}
			},
			assert: func(t *testing.T, seen []authz.Request, err error) {
				require.NoError(t, err, "the second decider must never be reached")
				assert.Len(t, seen, 1)
			},
		},
		{
			name: "every constraint decider is consulted, not just the first",
			build: func(seen *[]authz.Request) authz.Composite {
				return authz.Composite{Constraint: []authz.Decider{
					stubDecider{decision: authz.Allow, seen: seen},
					stubDecider{decision: authz.Allow, seen: seen},
					stubDecider{decision: authz.Deny, seen: seen},
				}}
			},
			assert: func(t *testing.T, seen []authz.Request, err error) {
				require.ErrorIs(t, err, authz.ErrNotAuthorized,
					"a Deny anywhere in the constraint stage refuses")
				assert.Len(t, seen, 3)
			},
		},
		{
			name: "an abstaining constraint decider does not refuse",
			build: func(seen *[]authz.Request) authz.Composite {
				return authz.Composite{Constraint: []authz.Decider{
					stubDecider{decision: authz.NotApplicable, seen: seen},
				}}
			},
			assert: func(t *testing.T, seen []authz.Request, err error) {
				require.NoError(t, err)
				assert.Len(t, seen, 1)
			},
		},
		{
			name: "the whole Request reaches the decider unaltered",
			build: func(seen *[]authz.Request) authz.Composite {
				return authz.Composite{Identity: []authz.Decider{
					stubDecider{decision: authz.Allow, seen: seen},
				}}
			},
			assert: func(t *testing.T, seen []authz.Request, err error) {
				require.NoError(t, err)
				require.Len(t, seen, 1)
				assert.Equal(t, authz.OpReassign, seen[0].Operation)
				assert.Equal(t, "u1", seen[0].Actor.ID)
				assert.Equal(t, authz.TaskView{Claimed: true, ClaimantID: "u9"}, seen[0].Task,
					"the task projection must survive to the decider: the ownership "+
						"rule arriving later is the only reason it is carried")
				assert.Equal(t, map[string]any{"region": "EU"}, seen[0].Vars)
			},
		},
		{
			name: "an identity decider's error aborts and is not a denial",
			build: func(seen *[]authz.Request) authz.Composite {
				return authz.Composite{
					Identity:   []authz.Decider{stubDecider{err: errors.New("directory unreachable"), seen: seen}},
					Constraint: []authz.Decider{stubDecider{decision: authz.Allow, seen: seen}},
				}
			},
			assert: func(t *testing.T, seen []authz.Request, err error) {
				require.Error(t, err)
				assert.NotErrorIs(t, err, authz.ErrNotAuthorized,
					"a rule that could not be evaluated has decided nothing")
				assert.Len(t, seen, 1, "evaluation must stop at the failing decider")
			},
		},
		{
			// Fail-closed direction for a Decision value this package does not
			// define — a consumer decider returning a raw int, say. It must
			// refuse, in BOTH stages, rather than fall through.
			name: "an unrecognised Decision refuses in the identity stage",
			build: func(seen *[]authz.Request) authz.Composite {
				return authz.Composite{Identity: []authz.Decider{
					stubDecider{decision: authz.Decision(99), seen: seen},
				}}
			},
			assert: func(t *testing.T, seen []authz.Request, err error) {
				require.ErrorIs(t, err, authz.ErrNotAuthorized)
				assert.Len(t, seen, 1)
			},
		},
		{
			name: "an unrecognised Decision refuses in the constraint stage",
			build: func(seen *[]authz.Request) authz.Composite {
				return authz.Composite{Constraint: []authz.Decider{
					stubDecider{decision: authz.Decision(99), seen: seen},
				}}
			},
			assert: func(t *testing.T, seen []authz.Request, err error) {
				require.ErrorIs(t, err, authz.ErrNotAuthorized)
				assert.Len(t, seen, 1)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var seen []authz.Request
			err := tc.build(&seen).Authorize(t.Context(), authz.Request{
				Operation: authz.OpReassign,
				Actor:     authz.Actor{ID: "u1"},
				Vars:      map[string]any{"region": "EU"},
				Task:      authz.TaskView{Claimed: true, ClaimantID: "u9"},
			})
			tc.assert(t, seen, err)
		})
	}
}

// TestDecisionString pins the debug rendering, including the abstaining zero
// value: a Decision that printed as "Allow" by accident would make every
// diagnostic in this package misleading in the one direction that matters.
func TestDecisionString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		in     authz.Decision
		assert func(t *testing.T, got string)
	}{
		{
			name: "the zero value is NotApplicable",
			in:   authz.Decision(0),
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "NotApplicable", got,
					"a zero Decision that printed as Allow would make every "+
						"diagnostic in this package misleading in the one "+
						"direction that matters")
			},
		},
		{
			name:   "NotApplicable",
			in:     authz.NotApplicable,
			assert: func(t *testing.T, got string) { assert.Equal(t, "NotApplicable", got) },
		},
		{
			name:   "Allow",
			in:     authz.Allow,
			assert: func(t *testing.T, got string) { assert.Equal(t, "Allow", got) },
		},
		{
			name:   "Deny",
			in:     authz.Deny,
			assert: func(t *testing.T, got string) { assert.Equal(t, "Deny", got) },
		},
		{
			name: "an undefined value renders its number",
			in:   authz.Decision(99),
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "Decision(99)", got)
				assert.NotContains(t, got, "Allow")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, tc.in.String())
		})
	}
}

// TestCompositeRejectsMisconfiguration covers the two remaining ways a Composite
// can be built wrong, both of which must produce a diagnosable configuration
// error rather than a panic or an allow.
//
// Both rows are paired with an at-limit accept so an implementation that refused
// every composite could not satisfy them.
func TestCompositeRejectsMisconfiguration(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		composite authz.Composite
		spec      authz.AuthzSpec
		assert    func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name:      "a nil decider element is a configuration error, not a panic",
			composite: authz.Composite{Identity: []authz.Decider{nil}},
			spec:      authz.AuthzSpec{Roles: []string{"admin"}},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, authz.ErrNilDecider)
				assert.NotErrorIs(t, err, authz.ErrNotAuthorized,
					"a wiring typo is not an authorization decision")
			},
		},
		{
			// ⚠ THE EMPTY-SPEC CELL. A nil element is a wiring defect whatever
			// the spec asks for, but the coverage shortcut used to return before
			// the nil scan, so this case reached stage 1 and PANICKED — while
			// ErrNilDecider's own godoc promised "a diagnosable refusal instead
			// of a nil-pointer panic on the request path". A documented limit
			// may be stated only if it is true.
			//
			// An empty spec is the documented allow case — every task authored
			// with no eligibility — so it is the shape most likely to be in
			// flight when a wiring bug lands.
			name:      "a nil decider is caught even when the spec sets nothing",
			composite: authz.Composite{Identity: []authz.Decider{nil}},
			spec:      authz.AuthzSpec{},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, authz.ErrNilDecider)
				assert.NotErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
		{
			// ⚠ The direction that matters. A decider that does not implement
			// SpecReader declares no coverage and is treated as covering
			// NOTHING. The permissive alternative would let a single custom
			// decider switch the whole coverage check off — which is precisely
			// the reported fail-open: Composite{Identity: {mine, Privilege,
			// Role}} silently ignoring every Attribute in every definition.
			name: "a decider that declares no coverage covers nothing",
			composite: authz.Composite{Identity: []authz.Decider{
				stubDecider{decision: authz.Allow},
			}},
			spec: authz.AuthzSpec{Roles: []string{"admin"}},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, authz.ErrSpecNotEvaluable)
				assert.Contains(t, err.Error(), "roles")
			},
		},
		{
			name: "an undeclared decider alongside a declaring one still leaves the declared field covered",
			composite: authz.Composite{Identity: []authz.Decider{
				stubDecider{decision: authz.NotApplicable},
				authz.RoleDecider{},
			}},
			spec: authz.AuthzSpec{Roles: []string{"admin"}},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err,
					"the at-limit accept: an undeclared decider must not poison "+
						"coverage another decider does provide")
			},
		},
		{
			name: "an undeclared decider is fine when the spec sets nothing",
			composite: authz.Composite{Identity: []authz.Decider{
				stubDecider{decision: authz.Allow},
			}},
			spec: authz.AuthzSpec{},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name:      "the message names every uncovered field, sorted",
			composite: authz.Composite{Constraint: []authz.Decider{authz.AttributeDecider{}}},
			spec: authz.AuthzSpec{
				Roles:      []string{"admin"},
				Privileges: []string{"p"},
				Attribute:  `1 == 1`,
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, authz.ErrSpecNotEvaluable)
				assert.Contains(t, err.Error(), "privileges, roles",
					"a deterministic operator-facing diagnostic; attribute IS covered "+
						"and must not be listed")
				assert.NotContains(t, err.Error(), "attribute")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.composite.Authorize(t.Context(), authz.Request{
				Operation: authz.OpClaim,
				Spec:      tc.spec,
				Actor:     authz.Actor{ID: "u1", Roles: []string{"admin"}},
			})
			tc.assert(t, err)
		})
	}
}
