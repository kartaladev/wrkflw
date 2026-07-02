package casbinauthz_test

import (
	"testing"

	casbinv2 "github.com/casbin/casbin/v2"
	casbinmodel "github.com/casbin/casbin/v2/model"
	stringadapter "github.com/casbin/casbin/v2/persist/string-adapter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/casbinauthz"
)

// policy exercises the role graph and privilege check.
// g, alice, manager  → alice inherits manager role
// p, manager, approve, *  → manager may approve anything
const policy = `
p, manager, approve, *
g, alice, manager
`

func TestEndToEnd_AllowDeny(t *testing.T) {
	a, _, err := casbinauthz.NewCasbinAuthorizer(casbinauthz.FromStrings("", policy)) // "" ⇒ DefaultModel
	require.NoError(t, err)

	alice := authz.Actor{ID: "alice"}
	bob := authz.Actor{ID: "bob"}
	spec := authz.AuthzSpec{Privileges: []string{"approve"}}

	assert.NoError(t, a.Authorize(t.Context(), spec, alice, nil))
	assert.ErrorIs(t, a.Authorize(t.Context(), spec, bob, nil), authz.ErrNotAuthorized)
}

func TestNewCasbinAuthorizerFromStrings_ExplicitDefaultModel(t *testing.T) {
	a, _, err := casbinauthz.NewCasbinAuthorizer(casbinauthz.FromStrings(casbinauthz.DefaultModel, policy))
	require.NoError(t, err)
	require.NotNil(t, a)

	alice := authz.Actor{ID: "alice"}
	spec := authz.AuthzSpec{Privileges: []string{"approve"}}
	assert.NoError(t, a.Authorize(t.Context(), spec, alice, nil))
}

func TestReloadPolicy_ViaTypeAssertion(t *testing.T) {
	a, _, err := casbinauthz.NewCasbinAuthorizer(casbinauthz.FromStrings(casbinauthz.DefaultModel, policy))
	require.NoError(t, err)

	reloader, ok := a.(interface{ ReloadPolicy() error })
	require.True(t, ok)
	assert.NoError(t, reloader.ReloadPolicy())
}

func TestNewCasbinAuthorizer_PrebuiltEnforcer(t *testing.T) {
	m, err := casbinmodel.NewModelFromString(casbinauthz.DefaultModel)
	require.NoError(t, err)

	e, err := casbinv2.NewSyncedEnforcer(m, stringadapter.NewAdapter(policy))
	require.NoError(t, err)

	a, _, err := casbinauthz.NewCasbinAuthorizer(casbinauthz.FromEnforcer(e))
	require.NoError(t, err)
	require.NotNil(t, a)

	alice := authz.Actor{ID: "alice"}
	spec := authz.AuthzSpec{Privileges: []string{"approve"}}
	assert.NoError(t, a.Authorize(t.Context(), spec, alice, nil))
}

func TestNewCasbinAuthorizerFromStrings_MalformedModel(t *testing.T) {
	_, _, err := casbinauthz.NewCasbinAuthorizer(casbinauthz.FromStrings("not a valid casbin model", ""))
	assert.Error(t, err)
}

func TestNewCasbinAuthorizerSourceValidation(t *testing.T) {
	t.Parallel()

	// modelText / policyText are identical to the const used above so we don't
	// duplicate knowledge; we re-declare them here so the sub-tests are
	// self-contained and this function doesn't depend on the file-level const.
	const modelText = "" // empty ⇒ DefaultModel
	const policyText = `
p, manager, approve, *
g, alice, manager
`

	type testCase struct {
		name   string
		opts   []casbinauthz.Option
		assert func(t *testing.T, az authz.Authorizer, closer interface{ Close() error }, err error)
	}

	cases := []testCase{
		{
			name: "no source",
			opts: nil,
			assert: func(t *testing.T, _ authz.Authorizer, _ interface{ Close() error }, err error) {
				require.ErrorIs(t, err, casbinauthz.ErrNoAuthorizerSource)
			},
		},
		{
			name: "multiple sources",
			opts: []casbinauthz.Option{
				casbinauthz.FromStrings(modelText, policyText),
				casbinauthz.FromStrings(modelText, policyText),
			},
			assert: func(t *testing.T, _ authz.Authorizer, _ interface{ Close() error }, err error) {
				require.ErrorIs(t, err, casbinauthz.ErrMultipleAuthorizerSources)
			},
		},
		{
			name: "from strings ok",
			opts: []casbinauthz.Option{casbinauthz.FromStrings(modelText, policyText)},
			assert: func(t *testing.T, az authz.Authorizer, closer interface{ Close() error }, err error) {
				require.NoError(t, err)
				require.NotNil(t, az)
				if closer != nil {
					t.Cleanup(func() { _ = closer.Close() })
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			az, closer, err := casbinauthz.NewCasbinAuthorizer(tc.opts...)
			tc.assert(t, az, closer, err)
		})
	}
}
