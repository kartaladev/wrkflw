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

// TestMigrateFailsOnClosedPool verifies that Migrate surfaces a provider.Up error
// when the underlying *sql.DB cannot reach the database (pool is closed before use).
func TestMigrateFailsOnClosedPool(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	pool.Close() // close the pool so stdlib.OpenDBFromPool yields an unusable *sql.DB

	err := pg.Migrate(t.Context(), pool)
	require.Error(t, err, "Migrate on a closed pool must return an error")
}

// TestMigration0003AddsDLQAndDedup verifies that migration 0003 adds the DLQ
// columns to wrkflw_outbox and creates the wrkflw_processed_message table.
func TestMigration0003AddsDLQAndDedup(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	// Assert new columns exist on wrkflw_outbox.
	dlqColumns := []string{"status", "retry_count", "next_attempt_at", "last_error"}
	for _, col := range dlqColumns {
		var exists bool
		err := pool.QueryRow(t.Context(),
			`SELECT EXISTS (
				SELECT FROM information_schema.columns
				WHERE table_name = 'wrkflw_outbox' AND column_name = $1
			)`, col,
		).Scan(&exists)
		require.NoError(t, err)
		require.True(t, exists, "column wrkflw_outbox.%s should exist after migration 0003", col)
	}

	// Assert wrkflw_processed_message table exists.
	var tableExists bool
	err := pool.QueryRow(t.Context(),
		`SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_name = 'wrkflw_processed_message')`,
	).Scan(&tableExists)
	require.NoError(t, err)
	require.True(t, tableExists, "table wrkflw_processed_message should exist after migration 0003")

	// Assert the new indexes exist.
	indexes := []string{"wrkflw_outbox_claim_idx", "wrkflw_outbox_dead_idx"}
	for _, idx := range indexes {
		var idxExists bool
		err := pool.QueryRow(t.Context(),
			`SELECT EXISTS (SELECT FROM pg_indexes WHERE indexname = $1)`, idx,
		).Scan(&idxExists)
		require.NoError(t, err)
		require.True(t, idxExists, "index %s should exist after migration 0003", idx)
	}

	// Assert the old unpublished index no longer exists.
	var oldIdxExists bool
	err = pool.QueryRow(t.Context(),
		`SELECT EXISTS (SELECT FROM pg_indexes WHERE indexname = 'wrkflw_outbox_unpublished_idx')`,
	).Scan(&oldIdxExists)
	require.NoError(t, err)
	require.False(t, oldIdxExists, "index wrkflw_outbox_unpublished_idx should be dropped after migration 0003")
}
