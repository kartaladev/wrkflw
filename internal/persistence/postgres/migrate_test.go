package postgres_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/database"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
)

func TestMigrateCreatesTables(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)

	require.NoError(t, pg.Migrate(t.Context(), pool))

	tables := []string{"wrkflw_instances", "wrkflw_journal", "wrkflw_outbox", "wrkflw_definitions"}
	for _, tbl := range tables {
		var exists bool
		err := pool.QueryRow(t.Context(),
			`SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_name = $1)`, tbl,
		).Scan(&exists)
		require.NoError(t, err)
		require.True(t, exists, "table %s should exist", tbl)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))
	require.NoError(t, pg.Migrate(t.Context(), pool)) // second run is a no-op
}
