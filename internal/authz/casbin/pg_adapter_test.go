package casbin_test

import (
	"testing"

	"github.com/casbin/casbin/v2/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/database"
	authzcasbin "github.com/zakyalvan/krtlwrkflw/internal/authz/casbin"
)

// rbacModel is a minimal RBAC model for adapter round-trip tests.
const rbacModel = `
[request_definition]
r = sub, obj, act
[policy_definition]
p = sub, obj, act
[role_definition]
g = _, _
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = g(r.sub, p.sub) && r.obj == p.obj && r.act == p.act
`

func TestPGAdapterAutoSaveMutations(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, authzcasbin.MigrateCasbin(t.Context(), pool))
	a := authzcasbin.NewPGAdapter(pool)

	// AddPolicy persists.
	require.NoError(t, a.AddPolicy("p", "p", []string{"admin", "data1", "read"}))
	require.NoError(t, a.AddPolicy("p", "p", []string{"admin", "data2", "read"}))
	require.NoError(t, a.AddPolicy("p", "p", []string{"viewer", "data1", "read"}))

	load := func() model.Model {
		m, err := model.NewModelFromString(rbacModel)
		require.NoError(t, err)
		require.NoError(t, a.LoadPolicy(m))
		return m
	}

	m := load()
	ok, err := m.HasPolicy("p", "p", []string{"admin", "data1", "read"})
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = m.HasPolicy("p", "p", []string{"viewer", "data1", "read"})
	require.NoError(t, err)
	assert.True(t, ok)

	// RemovePolicy deletes exactly that rule.
	require.NoError(t, a.RemovePolicy("p", "p", []string{"admin", "data1", "read"}))
	m = load()
	ok, err = m.HasPolicy("p", "p", []string{"admin", "data1", "read"})
	require.NoError(t, err)
	assert.False(t, ok)

	ok, err = m.HasPolicy("p", "p", []string{"admin", "data2", "read"})
	require.NoError(t, err)
	assert.True(t, ok)

	// RemoveFilteredPolicy(fieldIndex=0, "viewer") removes all rules whose v0=viewer.
	require.NoError(t, a.RemoveFilteredPolicy("p", "p", 0, "viewer"))
	m = load()
	ok, err = m.HasPolicy("p", "p", []string{"viewer", "data1", "read"})
	require.NoError(t, err)
	assert.False(t, ok)

	ok, err = m.HasPolicy("p", "p", []string{"admin", "data2", "read"})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestPGAdapterSaveLoadRoundTrip(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, authzcasbin.MigrateCasbin(t.Context(), pool))

	a := authzcasbin.NewPGAdapter(pool)

	// Build a model holding two p rules and one g rule, then SavePolicy.
	m, err := model.NewModelFromString(rbacModel)
	require.NoError(t, err)
	require.NoError(t, m.AddPolicy("p", "p", []string{"admin", "data1", "read"}))
	require.NoError(t, m.AddPolicy("p", "p", []string{"admin", "data1", "write"}))
	require.NoError(t, m.AddPolicy("g", "g", []string{"alice", "admin"}))
	require.NoError(t, a.SavePolicy(m))

	// Load into a FRESH model and assert the rules round-tripped.
	m2, err := model.NewModelFromString(rbacModel)
	require.NoError(t, err)
	require.NoError(t, a.LoadPolicy(m2))

	ok, err := m2.HasPolicy("p", "p", []string{"admin", "data1", "read"})
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = m2.HasPolicy("p", "p", []string{"admin", "data1", "write"})
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = m2.HasPolicy("g", "g", []string{"alice", "admin"})
	require.NoError(t, err)
	assert.True(t, ok)
}
