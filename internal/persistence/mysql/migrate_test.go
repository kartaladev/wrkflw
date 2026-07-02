package mysql_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	mysqlpkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
)

func TestMigrate_CreatesAllTables(t *testing.T) {
	db := dbtest.RunTestMySQL(t)
	ctx := context.Background()

	require.NoError(t, mysqlpkg.Migrate(ctx, db), "migrate must succeed")

	expectedTables := []string{
		"wrkflw_instances",
		"wrkflw_journal",
		"wrkflw_outbox",
		"wrkflw_definitions",
		"wrkflw_processed_message",
		"wrkflw_call_links",
		"wrkflw_timers",
		"wrkflw_chain_links",
	}

	rows, err := db.QueryContext(ctx,
		"SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	found := make(map[string]bool)
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		found[name] = true
	}
	require.NoError(t, rows.Err())

	for _, tbl := range expectedTables {
		assert.True(t, found[tbl], "table %s must exist after migration", tbl)
	}
}

func TestMigrate_IsIdempotent(t *testing.T) {
	// RunTestMySQL already runs Migrate once internally; calling it again on the
	// same *sql.DB (same goose_db_version table) proves re-running is a no-op.
	db := dbtest.RunTestMySQL(t)
	ctx := context.Background()

	require.NoError(t, mysqlpkg.Migrate(ctx, db), "migrate on already-migrated db must be idempotent")
}
