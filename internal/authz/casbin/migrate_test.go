package casbin_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authzcasbin "github.com/kartaladev/wrkflw/internal/authz/casbin"
	"github.com/kartaladev/wrkflw/internal/dbtest"
)

func TestMigrateCasbinCreatesRuleTable(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)

	require.NoError(t, authzcasbin.MigrateCasbin(t.Context(), pool))
	// Idempotent: a second run is a no-op.
	require.NoError(t, authzcasbin.MigrateCasbin(t.Context(), pool))

	var exists bool
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'casbin_rule')`,
	).Scan(&exists))
	assert.True(t, exists, "casbin_rule table must exist after MigrateCasbin")
}
