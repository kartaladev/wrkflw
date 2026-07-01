package mysql_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	mypkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
)

// TestPruner_DeletesOlderThanCutoff seeds one OLD row (before cutoff) and one
// NEW row (after cutoff) into each prunable table, runs the corresponding prune
// method at the cutoff, and asserts that only the old row was deleted
// (rows-affected == 1) while the new row survives.
func TestPruner_DeletesOlderThanCutoff(t *testing.T) {
	t.Parallel()

	cutoff := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	old := cutoff.Add(-24 * time.Hour) // 2026-01-14 — before cutoff → must be deleted
	new_ := cutoff.Add(24 * time.Hour) // 2026-01-16 — after cutoff  → must survive

	// --- PruneOutbox ---
	t.Run("outbox", func(t *testing.T) {
		t.Parallel()
		db := dbtest.RunTestMySQL(t)
		ctx := t.Context()

		// Seed OLD row: status='published', published_at=old
		_, err := db.ExecContext(ctx,
			`INSERT INTO wrkflw_outbox
			 (instance_id, topic, payload, dedup_key, created_at, status, published_at)
			 VALUES (?, ?, '{}', ?, ?, 'published', ?)`,
			"inst-ob-old", "test.topic", "dk-ob-old", old, old)
		require.NoError(t, err, "seed old outbox row")

		// Seed NEW row: status='published', published_at=new_
		_, err = db.ExecContext(ctx,
			`INSERT INTO wrkflw_outbox
			 (instance_id, topic, payload, dedup_key, created_at, status, published_at)
			 VALUES (?, ?, '{}', ?, ?, 'published', ?)`,
			"inst-ob-new", "test.topic", "dk-ob-new", new_, new_)
		require.NoError(t, err, "seed new outbox row")

		p := mypkg.NewPruner(db)
		n, err := p.PruneOutbox(ctx, cutoff)
		require.NoError(t, err)
		assert.EqualValues(t, 1, n, "only old outbox row deleted")

		// OLD row must be gone.
		var count int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM wrkflw_outbox WHERE dedup_key = 'dk-ob-old'`).Scan(&count))
		assert.Equal(t, 0, count, "old outbox row must be deleted")

		// NEW row must survive.
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM wrkflw_outbox WHERE dedup_key = 'dk-ob-new'`).Scan(&count))
		assert.Equal(t, 1, count, "new outbox row must survive")
	})

	// --- PruneCallLinks ---
	t.Run("call_links", func(t *testing.T) {
		t.Parallel()
		db := dbtest.RunTestMySQL(t)
		ctx := t.Context()

		// Seed OLD row: status='notified', notified_at=old
		_, err := db.ExecContext(ctx,
			`INSERT INTO wrkflw_call_links
			 (child_instance_id, parent_instance_id, parent_command_id, parent_def_id,
			  parent_def_version, depth, status, created_at, notified_at)
			 VALUES (?, 'parent-1', 'cmd-1', 'def-1', 1, 0, 'notified', ?, ?)`,
			"child-cl-old", old, old)
		require.NoError(t, err, "seed old call_link row")

		// Seed NEW row: status='notified', notified_at=new_
		_, err = db.ExecContext(ctx,
			`INSERT INTO wrkflw_call_links
			 (child_instance_id, parent_instance_id, parent_command_id, parent_def_id,
			  parent_def_version, depth, status, created_at, notified_at)
			 VALUES (?, 'parent-2', 'cmd-2', 'def-2', 1, 0, 'notified', ?, ?)`,
			"child-cl-new", new_, new_)
		require.NoError(t, err, "seed new call_link row")

		p := mypkg.NewPruner(db)
		n, err := p.PruneCallLinks(ctx, cutoff)
		require.NoError(t, err)
		assert.EqualValues(t, 1, n, "only old call_link row deleted")

		var count int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM wrkflw_call_links WHERE child_instance_id = 'child-cl-old'`).Scan(&count))
		assert.Equal(t, 0, count, "old call_link row must be deleted")

		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM wrkflw_call_links WHERE child_instance_id = 'child-cl-new'`).Scan(&count))
		assert.Equal(t, 1, count, "new call_link row must survive")
	})

	// --- PruneChainLinks ---
	t.Run("chain_links", func(t *testing.T) {
		t.Parallel()
		db := dbtest.RunTestMySQL(t)
		ctx := t.Context()

		// Seed OLD row: created_at=old
		_, err := db.ExecContext(ctx,
			`INSERT INTO wrkflw_chain_links
			 (predecessor_instance_id, outcome, successor_instance_id, created_at)
			 VALUES ('pred-old', 'completed', 'succ-old', ?)`,
			old)
		require.NoError(t, err, "seed old chain_link row")

		// Seed NEW row: created_at=new_
		_, err = db.ExecContext(ctx,
			`INSERT INTO wrkflw_chain_links
			 (predecessor_instance_id, outcome, successor_instance_id, created_at)
			 VALUES ('pred-new', 'completed', 'succ-new', ?)`,
			new_)
		require.NoError(t, err, "seed new chain_link row")

		p := mypkg.NewPruner(db)
		n, err := p.PruneChainLinks(ctx, cutoff)
		require.NoError(t, err)
		assert.EqualValues(t, 1, n, "only old chain_link row deleted")

		var count int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM wrkflw_chain_links WHERE predecessor_instance_id = 'pred-old'`).Scan(&count))
		assert.Equal(t, 0, count, "old chain_link row must be deleted")

		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM wrkflw_chain_links WHERE predecessor_instance_id = 'pred-new'`).Scan(&count))
		assert.Equal(t, 1, count, "new chain_link row must survive")
	})

	// --- PruneProcessedMessages ---
	t.Run("processed_messages", func(t *testing.T) {
		t.Parallel()
		db := dbtest.RunTestMySQL(t)
		ctx := t.Context()

		// Seed OLD row: processed_at=old
		_, err := db.ExecContext(ctx,
			`INSERT INTO wrkflw_processed_message (subscriber, message_id, processed_at)
			 VALUES ('sub-prune', 'msg-old', ?)`,
			old)
		require.NoError(t, err, "seed old processed_message row")

		// Seed NEW row: processed_at=new_
		_, err = db.ExecContext(ctx,
			`INSERT INTO wrkflw_processed_message (subscriber, message_id, processed_at)
			 VALUES ('sub-prune', 'msg-new', ?)`,
			new_)
		require.NoError(t, err, "seed new processed_message row")

		p := mypkg.NewPruner(db)
		n, err := p.PruneProcessedMessages(ctx, cutoff)
		require.NoError(t, err)
		assert.EqualValues(t, 1, n, "only old processed_message row deleted")

		var count int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM wrkflw_processed_message WHERE message_id = 'msg-old'`).Scan(&count))
		assert.Equal(t, 0, count, "old processed_message row must be deleted")

		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM wrkflw_processed_message WHERE message_id = 'msg-new'`).Scan(&count))
		assert.Equal(t, 1, count, "new processed_message row must survive")
	})

	// --- PruneTimers ---
	t.Run("timers", func(t *testing.T) {
		t.Parallel()
		db := dbtest.RunTestMySQL(t)
		ctx := t.Context()

		// Seed OLD row: fire_at=old; kind=1 (arbitrary SMALLINT), def_id, def_version required.
		_, err := db.ExecContext(ctx,
			`INSERT INTO wrkflw_timers (instance_id, timer_id, fire_at, kind, def_id, def_version)
			 VALUES ('inst-tmr-old', 'tmr-old', ?, 1, 'def-tmr', 1)`,
			old)
		require.NoError(t, err, "seed old timer row")

		// Seed NEW row: fire_at=new_
		_, err = db.ExecContext(ctx,
			`INSERT INTO wrkflw_timers (instance_id, timer_id, fire_at, kind, def_id, def_version)
			 VALUES ('inst-tmr-new', 'tmr-new', ?, 1, 'def-tmr', 1)`,
			new_)
		require.NoError(t, err, "seed new timer row")

		p := mypkg.NewPruner(db)
		n, err := p.PruneTimers(ctx, cutoff)
		require.NoError(t, err)
		assert.EqualValues(t, 1, n, "only old timer row deleted")

		var count int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM wrkflw_timers WHERE timer_id = 'tmr-old'`).Scan(&count))
		assert.Equal(t, 0, count, "old timer row must be deleted")

		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM wrkflw_timers WHERE timer_id = 'tmr-new'`).Scan(&count))
		assert.Equal(t, 1, count, "new timer row must survive")
	})
}
