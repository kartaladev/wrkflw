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
			name:      "constraint-only is a legitimate composite and is honoured",
			composite: authz.Composite{Constraint: []authz.Decider{authz.AttributeDecider{}}},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err,
					"the at-limit accept: a composite with SOME deciders must not "+
						"be swept up by the zero-value guard")
			},
		},
		{
			name:      "identity-only is a legitimate composite and is honoured",
			composite: authz.Composite{Identity: []authz.Decider{authz.PrivilegeDecider{}}},
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
				Actor:     authz.Actor{ID: "u1"},
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
		name string
		in   authz.Decision
		want string
	}{
		{name: "the zero value is NotApplicable", in: authz.Decision(0), want: "NotApplicable"},
		{name: "NotApplicable", in: authz.NotApplicable, want: "NotApplicable"},
		{name: "Allow", in: authz.Allow, want: "Allow"},
		{name: "Deny", in: authz.Deny, want: "Deny"},
		{name: "an undefined value renders its number", in: authz.Decision(99), want: "Decision(99)"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.in.String())
		})
	}
}
