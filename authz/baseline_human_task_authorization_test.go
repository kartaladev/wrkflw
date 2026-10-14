package authz_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
)

// TestBaseline_UnevaluablePredicateErrorNamesTheAttributePredicate
// characterizes openspec/specs/human-task-authorization "Unevaluable
// predicates fail closed as errors": under the default composite, a predicate
// that does not compile or does not yield a boolean refuses the request with
// an error naming the attribute predicate, and that error is not
// authz.ErrNotAuthorized. No sentinel is exported for it, so the name is
// asserted on the error text.
func TestBaseline_UnevaluablePredicateErrorNamesTheAttributePredicate(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		predicate string
		assert    func(t *testing.T, err error)
	}

	refusedNamingThePredicate := func(t *testing.T, err error) {
		require.Error(t, err, "an unevaluable predicate must fail closed")
		assert.ErrorContains(t, err, "attribute predicate")
		assert.NotErrorIs(t, err, authz.ErrNotAuthorized)
	}

	cases := []testCase{
		{name: "predicate that does not compile", predicate: `actor.ID =====`, assert: refusedNamingThePredicate},
		{name: "non-boolean expression", predicate: `actor.ID`, assert: refusedNamingThePredicate},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := authz.NewComposite().Authorize(t.Context(), authz.Request{
				Operation: authz.OpClaim,
				Spec:      authz.AuthzSpec{Attribute: tc.predicate},
				Actor:     authz.Actor{ID: "u1"},
			})
			tc.assert(t, err)
		})
	}
}
