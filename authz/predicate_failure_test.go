package authz_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
)

// TestRoleAuthorizer_PredicateFailureIsNotADenial pins the distinction between
// the two things an attribute predicate can produce.
//
// # The invariant
//
// A predicate that EVALUATES to false is a denial: the actor is not authorized,
// and [authz.ErrNotAuthorized] says so. A predicate that FAILS TO EVALUATE has
// determined nothing at all — the policy is broken. Reporting that as
// ErrNotAuthorized asserts a fact the code does not know.
//
// # Why it is a disclosure, not only a modelling slip
//
// ErrNotAuthorized classifies 403, and httpcore.ClassifyError's 403 arm renders
// err.Error() — the WHOLE wrapped chain — into the client's response body. Every
// error path in the expression evaluator embeds the expression SOURCE verbatim
// (`compile %q`, `run %q`, `%q did not evaluate to bool`). So wrapping an
// evaluation failure in ErrNotAuthorized handed a denied caller the deployment's
// own authorization rule.
//
// MEASURED before the fix, from a real Authorize call through ClassifyError:
//
//	status 403, body.Message =
//	  workflow-authz: not authorized: attribute predicate: workflow-expreval:
//	  "actor.Attributes[\"internal_clearance_tier\"]" did not evaluate to bool (got string)
//
// The attribute name governing access, disclosed to precisely the caller who
// was refused it.
//
// ⚠ The fix keeps failing CLOSED — an unevaluable predicate still returns a
// non-nil error, and every caller treats any error from Authorize as a refusal.
// What changes is that it no longer claims to be an authorization decision, so
// it classifies 500, its Message is dropped, and the adapters log the raw error
// for operators instead of sending it.
//
// No ctx modifier: RoleAuthorizer.Authorize ignores its context entirely (its
// receiver discards it), so there is no cancellation path to exercise.
func TestRoleAuthorizer_PredicateFailureIsNotADenial(t *testing.T) {
	t.Parallel()

	// predicateSource is deliberately recognisable: if any assertion below is
	// weakened, this string appearing in an error is the tell.
	const predicateSource = `actor.Attributes["internal_clearance_tier"]`

	type testCase struct {
		name   string
		spec   authz.AuthzSpec
		actor  authz.Actor
		assert func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name:  "predicate evaluating false is a denial",
			spec:  authz.AuthzSpec{Attribute: `actor.ID == "someone-else"`},
			actor: authz.Actor{ID: "u1"},
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, authz.ErrNotAuthorized,
					"a predicate that evaluates to false IS an authorization denial")
			},
		},
		{
			name:  "predicate satisfied authorizes",
			spec:  authz.AuthzSpec{Attribute: `actor.ID == "u1"`},
			actor: authz.Actor{ID: "u1"},
			assert: func(t *testing.T, err error) {
				assert.NoError(t, err)
			},
		},
		{
			name: "predicate that cannot evaluate is not a denial and does not leak its source",
			// Yields a string rather than a bool — what an author writing an
			// attribute lookup without a comparison produces.
			spec:  authz.AuthzSpec{Attribute: predicateSource},
			actor: authz.Actor{ID: "u1", Attributes: map[string]any{"internal_clearance_tier": "SECRET-3"}},
			assert: func(t *testing.T, err error) {
				require.Error(t, err, "an unevaluable predicate must still fail CLOSED")
				assert.NotErrorIs(t, err, authz.ErrNotAuthorized,
					"a broken policy has determined nothing; reporting it as a denial "+
						"routes it to the 403 arm, which renders the whole chain to the client")
			},
		},
		{
			name:  "predicate that cannot compile is not a denial either",
			spec:  authz.AuthzSpec{Attribute: `actor.ID ===== `},
			actor: authz.Actor{ID: "u1"},
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.NotErrorIs(t, err, authz.ErrNotAuthorized,
					"a predicate that does not compile has determined nothing")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := authz.RoleAuthorizer{}.Authorize(t.Context(), tc.spec, tc.actor, map[string]any{})
			tc.assert(t, err)

			// ⚠ Applies to EVERY row, including the ones expected to deny: no
			// error this authorizer produces may carry the predicate source,
			// whatever its classification. Asserting this only on the failure
			// row would leave the denial rows free to start leaking.
			if err != nil && errors.Is(err, authz.ErrNotAuthorized) {
				assert.NotContains(t, err.Error(), predicateSource,
					"an error that classifies 403 renders err.Error() to the client, "+
						"so it must never carry the policy expression")
			}
		})
	}
}
