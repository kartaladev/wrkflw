package casbinauthz_test

import (
	"context"
	"testing"

	casbinv2 "github.com/casbin/casbin/v2"
	casbinmodel "github.com/casbin/casbin/v2/model"
	stringadapter "github.com/casbin/casbin/v2/persist/string-adapter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/casbinauthz"
	"github.com/kartaladev/wrkflw/service"
)

func newTestEnforcer(t *testing.T) *casbinv2.SyncedEnforcer {
	t.Helper()
	m, err := casbinmodel.NewModelFromString(casbinauthz.DefaultModel)
	require.NoError(t, err)
	e, err := casbinv2.NewSyncedEnforcer(m, stringadapter.NewAdapter("\n"))
	require.NoError(t, err)
	return e
}

func TestPolicyAdminFor_CasbinAuthorizer(t *testing.T) {
	e := newTestEnforcer(t)
	a, _, err := casbinauthz.NewCasbinAuthorizer(casbinauthz.FromEnforcer(e))
	require.NoError(t, err)
	pa, ok := casbinauthz.PolicyAdminFor(a)
	require.True(t, ok)
	require.NotNil(t, pa)
}

func TestPolicyAdminFor_NonCasbinAuthorizer(t *testing.T) {
	pa, ok := casbinauthz.PolicyAdminFor(authz.AllowAll{})
	assert.False(t, ok)
	assert.Nil(t, pa)
}

func TestPolicyAdmin_AddListRemovePolicy(t *testing.T) {
	e := newTestEnforcer(t)
	a, _, err := casbinauthz.NewCasbinAuthorizer(casbinauthz.FromEnforcer(e))
	require.NoError(t, err)
	pa, ok := casbinauthz.PolicyAdminFor(a)
	require.True(t, ok)

	ctx := context.Background()
	rule := service.PolicyRule{Subject: "mgr", Object: "approve", Action: "*"}

	// AddPolicy first time → true.
	added, err := pa.AddPolicy(ctx, rule)
	require.NoError(t, err)
	assert.True(t, added)

	// AddPolicy duplicate → false.
	added, err = pa.AddPolicy(ctx, rule)
	require.NoError(t, err)
	assert.False(t, added)

	// ListPolicies contains the rule.
	policies, err := pa.ListPolicies(ctx)
	require.NoError(t, err)
	assert.Contains(t, policies, rule)

	// RemovePolicy → true.
	removed, err := pa.RemovePolicy(ctx, rule)
	require.NoError(t, err)
	assert.True(t, removed)

	// ListPolicies no longer contains it.
	policies, err = pa.ListPolicies(ctx)
	require.NoError(t, err)
	assert.NotContains(t, policies, rule)
}

func TestPolicyAdmin_AddListRemoveRole(t *testing.T) {
	e := newTestEnforcer(t)
	a, _, err := casbinauthz.NewCasbinAuthorizer(casbinauthz.FromEnforcer(e))
	require.NoError(t, err)
	pa, ok := casbinauthz.PolicyAdminFor(a)
	require.True(t, ok)

	ctx := context.Background()
	binding := service.RoleBinding{User: "alice", Role: "mgr"}

	// AddRole first time → true.
	added, err := pa.AddRole(ctx, binding)
	require.NoError(t, err)
	assert.True(t, added)

	// Duplicate AddRole → false (already present), mirroring the policy side.
	added, err = pa.AddRole(ctx, binding)
	require.NoError(t, err)
	assert.False(t, added, "duplicate AddRole must report added=false")

	// ListRoles contains the binding.
	roles, err := pa.ListRoles(ctx)
	require.NoError(t, err)
	assert.Contains(t, roles, binding)

	// RemoveRole → true.
	removed, err := pa.RemoveRole(ctx, binding)
	require.NoError(t, err)
	assert.True(t, removed)

	// ListRoles no longer contains it.
	roles, err = pa.ListRoles(ctx)
	require.NoError(t, err)
	assert.NotContains(t, roles, binding)
}
