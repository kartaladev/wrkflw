package mysql_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	mysqlpkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
)

func TestMigrate_CreatesAllTables(t *testing.T) {
	db := database.RunTestMySQL(t)
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
	db := database.RunTestMySQL(t)
	ctx := context.Background()

	require.NoError(t, mysqlpkg.Migrate(ctx, db), "first migrate must succeed")
	require.NoError(t, mysqlpkg.Migrate(ctx, db), "second migrate (idempotent) must succeed")
}
