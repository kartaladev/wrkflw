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
	a, err := casbinauthz.NewCasbinAuthorizerFromStrings("", policy) // "" ⇒ DefaultModel
	require.NoError(t, err)

	alice := authz.Actor{ID: "alice"}
	bob := authz.Actor{ID: "bob"}
	spec := authz.AuthzSpec{Privileges: []string{"approve"}}

	assert.NoError(t, a.Authorize(t.Context(), spec, alice, nil))
	assert.ErrorIs(t, a.Authorize(t.Context(), spec, bob, nil), authz.ErrNotAuthorized)
}

func TestNewCasbinAuthorizerFromStrings_ExplicitDefaultModel(t *testing.T) {
	a, err := casbinauthz.NewCasbinAuthorizerFromStrings(casbinauthz.DefaultModel, policy)
	require.NoError(t, err)
	require.NotNil(t, a)

	alice := authz.Actor{ID: "alice"}
	spec := authz.AuthzSpec{Privileges: []string{"approve"}}
	assert.NoError(t, a.Authorize(t.Context(), spec, alice, nil))
}

func TestReloadPolicy_ViaTypeAssertion(t *testing.T) {
	a, err := casbinauthz.NewCasbinAuthorizerFromStrings(casbinauthz.DefaultModel, policy)
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

	a := casbinauthz.NewCasbinAuthorizer(e)
	require.NotNil(t, a)

	alice := authz.Actor{ID: "alice"}
	spec := authz.AuthzSpec{Privileges: []string{"approve"}}
	assert.NoError(t, a.Authorize(t.Context(), spec, alice, nil))
}

func TestNewCasbinAuthorizerFromStrings_MalformedModel(t *testing.T) {
	_, err := casbinauthz.NewCasbinAuthorizerFromStrings("not a valid casbin model", "")
	assert.Error(t, err)
}
