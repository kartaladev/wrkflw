package postgres_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
)

// TestDeduperPrune verifies the Prune method deletes rows with processed_at
// before the supplied cutoff and leaves newer rows intact (spec §2.1).
func TestDeduperPrune(t *testing.T) {
	t.Parallel()

	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	d := pg.NewDeduper(pool)

	// Insert two rows with explicit processed_at values.
	// 'old' was processed on 2026-01-01 — before the cutoff.
	// 'recent' was processed on 2026-06-01 — after the cutoff.
	_, err := pool.Exec(t.Context(),
		`INSERT INTO wrkflw_processed_message (subscriber, message_id, processed_at)
		 VALUES ($1, $2, $3), ($4, $5, $6)`,
		"s", "old", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		"s", "recent", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)

	cutoff := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	// First Prune: exactly one old row should be deleted.
	pruned, err := d.Prune(t.Context(), cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(1), pruned, "Prune must delete exactly the 'old' row")

	// Only 'recent' must survive.
	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM wrkflw_processed_message WHERE subscriber = 's'`,
	).Scan(&count))
	assert.Equal(t, 1, count, "exactly one row must remain after Prune")

	var survivingID string
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT message_id FROM wrkflw_processed_message WHERE subscriber = 's'`,
	).Scan(&survivingID))
	assert.Equal(t, "recent", survivingID, "the surviving row must be 'recent'")

	// Second Prune with the same cutoff: nothing to delete.
	pruned2, err := d.Prune(t.Context(), cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(0), pruned2, "second Prune with same cutoff must delete zero rows")
}
