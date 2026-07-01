package postgres_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
)

// pruneFixture spins up a migrated database and a Pruner over it.
func pruneFixture(t *testing.T) (*pgxpool.Pool, *pg.Pruner) {
	t.Helper()
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))
	return pool, pg.NewPruner(pool)
}

// TestPrunerPruneOutbox verifies PruneOutbox deletes only published rows whose
// published_at is strictly before the cutoff; unpublished and dead rows, and
// recently-published rows, must survive.
func TestPrunerPruneOutbox(t *testing.T) {
	t.Parallel()

	pool, p := pruneFixture(t)

	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	// 1: published & old              -> eligible
	// 2: published & recent           -> survives (too new)
	// 3: pending, never published     -> survives (not published)
	// 4: dead, never published        -> survives (not published)
	_, err := pool.Exec(t.Context(),
		`INSERT INTO wrkflw_outbox
		   (instance_id, topic, payload, dedup_key, created_at, published_at, status)
		 VALUES
		   ('i', 't', '{}', 'k1', $1, $2, 'published'),
		   ('i', 't', '{}', 'k2', $1, $3, 'published'),
		   ('i', 't', '{}', 'k3', $1, NULL, 'pending'),
		   ('i', 't', '{}', 'k4', $1, NULL, 'dead')`,
		old, old, recent,
	)
	require.NoError(t, err)

	pruned, err := p.PruneOutbox(t.Context(), cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(1), pruned, "exactly the old published row must be deleted")

	var survivors []string
	rows, err := pool.Query(t.Context(),
		`SELECT dedup_key FROM wrkflw_outbox ORDER BY dedup_key`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var k string
		require.NoError(t, rows.Scan(&k))
		survivors = append(survivors, k)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"k2", "k3", "k4"}, survivors)

	// Idempotent: a second prune with the same cutoff deletes nothing.
	again, err := p.PruneOutbox(t.Context(), cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(0), again)
}

// TestPrunerPruneCallLinks verifies PruneCallLinks deletes only notified
// (delivered-to-parent) rows whose notified_at is strictly before the cutoff.
// Running and terminal-but-undelivered rows MUST survive — a parent may still
// need to be resumed from them.
func TestPrunerPruneCallLinks(t *testing.T) {
	t.Parallel()

	pool, p := pruneFixture(t)

	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	insert := func(child, status string, notifiedAt *time.Time) {
		t.Helper()
		_, err := pool.Exec(t.Context(),
			`INSERT INTO wrkflw_call_links
			   (child_instance_id, parent_instance_id, parent_command_id,
			    parent_def_id, parent_def_version, depth, status, created_at, notified_at)
			 VALUES ($1, 'p', 'cmd', 'd', 1, 1, $2, $3, $4)`,
			child, status, old, notifiedAt)
		require.NoError(t, err)
	}

	insert("notified-old", "notified", &old)       // eligible
	insert("notified-recent", "notified", &recent) // survives (too new)
	insert("running", "running", nil)              // survives (not terminal/notified)
	insert("completed", "completed", nil)          // survives (terminal but UNdelivered)

	pruned, err := p.PruneCallLinks(t.Context(), cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(1), pruned, "exactly the old notified row must be deleted")

	var survivors []string
	rows, err := pool.Query(t.Context(),
		`SELECT child_instance_id FROM wrkflw_call_links ORDER BY child_instance_id`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var k string
		require.NoError(t, rows.Scan(&k))
		survivors = append(survivors, k)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"completed", "notified-recent", "running"}, survivors)

	again, err := p.PruneCallLinks(t.Context(), cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(0), again)
}

// TestPrunerPruneProcessedMessages verifies PruneProcessedMessages delegates to
// Deduper.Prune: rows with processed_at before cutoff are deleted, newer survive.
func TestPrunerPruneProcessedMessages(t *testing.T) {
	t.Parallel()

	pool, p := pruneFixture(t)

	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	_, err := pool.Exec(t.Context(),
		`INSERT INTO wrkflw_processed_message (subscriber, message_id, processed_at)
		 VALUES ('s', 'old', $1), ('s', 'recent', $2)`,
		old, recent)
	require.NoError(t, err)

	pruned, err := p.PruneProcessedMessages(t.Context(), cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(1), pruned)

	var survivor string
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT message_id FROM wrkflw_processed_message`).Scan(&survivor))
	assert.Equal(t, "recent", survivor)
}

// TestPrunerPruneChainLinks verifies PruneChainLinks deletes lineage rows whose
// created_at is strictly before the cutoff (lineage loss is the documented
// trade-off; newer lineage survives).
func TestPrunerPruneChainLinks(t *testing.T) {
	t.Parallel()

	pool, p := pruneFixture(t)

	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	_, err := pool.Exec(t.Context(),
		`INSERT INTO wrkflw_chain_links
		   (predecessor_instance_id, outcome, successor_instance_id, created_at)
		 VALUES
		   ('p1', 'completed', 's1', $1),
		   ('p2', 'completed', 's2', $2)`,
		old, recent,
	)
	require.NoError(t, err)

	pruned, err := p.PruneChainLinks(t.Context(), cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(1), pruned, "exactly the old lineage row must be deleted")

	var survivor string
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT predecessor_instance_id FROM wrkflw_chain_links`).Scan(&survivor))
	assert.Equal(t, "p2", survivor)

	again, err := p.PruneChainLinks(t.Context(), cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(0), again)
}
