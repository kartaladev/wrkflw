package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/database"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/persistence"
)

// TestPruner exercises the neutral store.Pruner across all three dialects
// (Postgres, MySQL, SQLite) via forEachDialect. For each prunable table it
// seeds one old row (before cutoff) and one new row (after cutoff), calls the
// corresponding prune method at the cutoff, and asserts:
//   - exactly one row was deleted (rows-affected == 1)
//   - the old row is gone
//   - the new row survives
//
// The test folds in assertions from internal/persistence/{postgres,mysql}/pruner_test.go
// and exercises them on all three dialects. The SQLite backend is particularly
// important because pruner cutoffs must be formatted via timeArg so that the
// lexicographic TEXT comparison in SQLite is format-compatible with the values
// written by the store layer (ADR-0080).
func TestPruner(t *testing.T) {
	// Compile-time check: *store.Pruner satisfies the public persistence.Pruner interface.
	var _ persistence.Pruner = (*store.Pruner)(nil)

	cutoff := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	old := cutoff.Add(-24 * time.Hour)   // strictly before cutoff — must be deleted
	recent := cutoff.Add(24 * time.Hour) // after cutoff — must survive

	t.Run("PruneOutbox", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			p, err := store.NewPruner(b.conn, b.dialect)
			require.NoError(t, err)
			s, err := store.New(b.conn, b.dialect)
			require.NoError(t, err)
			ctx := t.Context()

			// Seed rows: published+old (eligible), published+recent (survives),
			// pending (not published — survives), dead (not published — survives).
			prunerExec(t, ctx, b, s,
				`INSERT INTO wrkflw_outbox
				   (instance_id, topic, payload, dedup_key, created_at, published_at, status)
				 VALUES
				   ('i', 't', '{}', 'k-ob-old',     ?, ?, 'published'),
				   ('i', 't', '{}', 'k-ob-recent',  ?, ?, 'published'),
				   ('i', 't', '{}', 'k-ob-pending', ?, NULL, 'pending'),
				   ('i', 't', '{}', 'k-ob-dead',    ?, NULL, 'dead')`,
				s.TimeArgForTest(old), s.TimeArgForTest(old),
				s.TimeArgForTest(recent), s.TimeArgForTest(recent),
				s.TimeArgForTest(old),
				s.TimeArgForTest(old),
			)

			n, err := p.PruneOutbox(ctx, cutoff)
			require.NoError(t, err, "%s: PruneOutbox", b.name)
			assert.Equal(t, int64(1), n, "%s: exactly the old published row must be deleted", b.name)

			assert.Equal(t, 0, prunerCount(t, ctx, b, `SELECT COUNT(*) FROM wrkflw_outbox WHERE dedup_key = ?`, "k-ob-old"),
				"%s: old outbox row must be deleted", b.name)
			assert.Equal(t, 1, prunerCount(t, ctx, b, `SELECT COUNT(*) FROM wrkflw_outbox WHERE dedup_key = ?`, "k-ob-recent"),
				"%s: recent outbox row must survive", b.name)
			assert.Equal(t, 1, prunerCount(t, ctx, b, `SELECT COUNT(*) FROM wrkflw_outbox WHERE dedup_key = ?`, "k-ob-pending"),
				"%s: pending outbox row must survive", b.name)
			assert.Equal(t, 1, prunerCount(t, ctx, b, `SELECT COUNT(*) FROM wrkflw_outbox WHERE dedup_key = ?`, "k-ob-dead"),
				"%s: dead outbox row must survive", b.name)

			// Idempotent: a second prune with the same cutoff deletes nothing.
			again, err := p.PruneOutbox(ctx, cutoff)
			require.NoError(t, err, "%s: PruneOutbox idempotent", b.name)
			assert.Equal(t, int64(0), again, "%s: second prune must delete 0 rows", b.name)
		})
	})

	t.Run("PruneCallLinks", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			p, err := store.NewPruner(b.conn, b.dialect)
			require.NoError(t, err)
			s, err := store.New(b.conn, b.dialect)
			require.NoError(t, err)
			ctx := t.Context()

			// Seed rows per the conservative eligibility rule:
			//   notified+old notified_at  → eligible
			//   notified+recent notified_at → survives (too new)
			//   running, notified_at IS NULL → survives (not terminal/notified)
			//   completed, notified_at IS NULL → survives (terminal but UNdelivered)
			prunerExec(t, ctx, b, s,
				`INSERT INTO wrkflw_call_links
				   (child_instance_id, parent_instance_id, parent_command_id,
				    parent_def_id, parent_def_version, depth, status, created_at, notified_at)
				 VALUES
				   ('cl-notified-old',    'p', 'cmd', 'd', 1, 1, 'notified',  ?, ?),
				   ('cl-notified-recent', 'p', 'cmd', 'd', 1, 1, 'notified',  ?, ?),
				   ('cl-running',         'p', 'cmd', 'd', 1, 1, 'running',   ?, NULL),
				   ('cl-completed',       'p', 'cmd', 'd', 1, 1, 'completed', ?, NULL)`,
				s.TimeArgForTest(old), s.TimeArgForTest(old),
				s.TimeArgForTest(old), s.TimeArgForTest(recent),
				s.TimeArgForTest(old),
				s.TimeArgForTest(old),
			)

			n, err := p.PruneCallLinks(ctx, cutoff)
			require.NoError(t, err, "%s: PruneCallLinks", b.name)
			assert.Equal(t, int64(1), n, "%s: exactly the old notified row must be deleted", b.name)

			assert.Equal(t, 0, prunerCount(t, ctx, b, `SELECT COUNT(*) FROM wrkflw_call_links WHERE child_instance_id = ?`, "cl-notified-old"),
				"%s: old notified call_link must be deleted", b.name)
			assert.Equal(t, 1, prunerCount(t, ctx, b, `SELECT COUNT(*) FROM wrkflw_call_links WHERE child_instance_id = ?`, "cl-notified-recent"),
				"%s: recent notified call_link must survive", b.name)
			assert.Equal(t, 1, prunerCount(t, ctx, b, `SELECT COUNT(*) FROM wrkflw_call_links WHERE child_instance_id = ?`, "cl-running"),
				"%s: running call_link must survive", b.name)
			assert.Equal(t, 1, prunerCount(t, ctx, b, `SELECT COUNT(*) FROM wrkflw_call_links WHERE child_instance_id = ?`, "cl-completed"),
				"%s: completed (undelivered) call_link must survive", b.name)

			// Idempotent.
			again, err := p.PruneCallLinks(ctx, cutoff)
			require.NoError(t, err, "%s: PruneCallLinks idempotent", b.name)
			assert.Equal(t, int64(0), again, "%s: second prune must delete 0 rows", b.name)
		})
	})

	t.Run("PruneChainLinks", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			p, err := store.NewPruner(b.conn, b.dialect)
			require.NoError(t, err)
			s, err := store.New(b.conn, b.dialect)
			require.NoError(t, err)
			ctx := t.Context()

			prunerExec(t, ctx, b, s,
				`INSERT INTO wrkflw_chain_links
				   (predecessor_instance_id, outcome, successor_instance_id, created_at)
				 VALUES
				   ('pred-cl-old',    'completed', 'succ-cl-old',    ?),
				   ('pred-cl-recent', 'completed', 'succ-cl-recent', ?)`,
				s.TimeArgForTest(old),
				s.TimeArgForTest(recent),
			)

			n, err := p.PruneChainLinks(ctx, cutoff)
			require.NoError(t, err, "%s: PruneChainLinks", b.name)
			assert.Equal(t, int64(1), n, "%s: exactly the old chain_link row must be deleted", b.name)

			assert.Equal(t, 0, prunerCount(t, ctx, b, `SELECT COUNT(*) FROM wrkflw_chain_links WHERE predecessor_instance_id = ?`, "pred-cl-old"),
				"%s: old chain_link must be deleted", b.name)
			assert.Equal(t, 1, prunerCount(t, ctx, b, `SELECT COUNT(*) FROM wrkflw_chain_links WHERE predecessor_instance_id = ?`, "pred-cl-recent"),
				"%s: recent chain_link must survive", b.name)

			// Idempotent.
			again, err := p.PruneChainLinks(ctx, cutoff)
			require.NoError(t, err, "%s: PruneChainLinks idempotent", b.name)
			assert.Equal(t, int64(0), again, "%s: second prune must delete 0 rows", b.name)
		})
	})

	t.Run("PruneProcessedMessages", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			p, err := store.NewPruner(b.conn, b.dialect)
			require.NoError(t, err)
			s, err := store.New(b.conn, b.dialect)
			require.NoError(t, err)

			// Seed rows via the shared insertDedupRow helper (uses timeArg encoding,
			// same as Deduper.Seen — guarantees format parity on SQLite TEXT path).
			insertDedupRow(t, s, b, "sub-pruner", "msg-pm-old", old)
			insertDedupRow(t, s, b, "sub-pruner", "msg-pm-recent", recent)

			n, err := p.PruneProcessedMessages(t.Context(), cutoff)
			require.NoError(t, err, "%s: PruneProcessedMessages", b.name)
			assert.Equal(t, int64(1), n, "%s: exactly the old processed_message row must be deleted", b.name)

			assert.Equal(t, 0, dedupRowCount(t, b, "sub-pruner", "msg-pm-old"),
				"%s: old processed_message row must be deleted", b.name)
			assert.Equal(t, 1, dedupRowCount(t, b, "sub-pruner", "msg-pm-recent"),
				"%s: recent processed_message row must survive", b.name)
		})
	})

	t.Run("PruneTimers", func(t *testing.T) {
		forEachDialect(t, func(t *testing.T, b backend) {
			p, err := store.NewPruner(b.conn, b.dialect)
			require.NoError(t, err)
			s, err := store.New(b.conn, b.dialect)
			require.NoError(t, err)
			ctx := t.Context()

			prunerExec(t, ctx, b, s,
				`INSERT INTO wrkflw_timers (instance_id, timer_id, next_run, kind, def_id, def_version)
				 VALUES
				   ('inst-tmr-old',    'tmr-old',    ?, 1, 'def-tmr', 1),
				   ('inst-tmr-recent', 'tmr-recent', ?, 1, 'def-tmr', 1)`,
				s.TimeArgForTest(old),
				s.TimeArgForTest(recent),
			)

			n, err := p.PruneTimers(ctx, cutoff)
			require.NoError(t, err, "%s: PruneTimers", b.name)
			assert.Equal(t, int64(1), n, "%s: exactly the old timer row must be deleted", b.name)

			assert.Equal(t, 0, prunerCount(t, ctx, b, `SELECT COUNT(*) FROM wrkflw_timers WHERE timer_id = ?`, "tmr-old"),
				"%s: old timer row must be deleted", b.name)
			assert.Equal(t, 1, prunerCount(t, ctx, b, `SELECT COUNT(*) FROM wrkflw_timers WHERE timer_id = ?`, "tmr-recent"),
				"%s: recent timer row must survive", b.name)

			// Idempotent.
			again, err := p.PruneTimers(ctx, cutoff)
			require.NoError(t, err, "%s: PruneTimers idempotent", b.name)
			assert.Equal(t, int64(0), again, "%s: second PruneTimers must delete 0 rows", b.name)
		})
	})
}

// prunerExec executes a raw INSERT using the backend's Querier, rebinding ? placeholders
// to the dialect's native form and encoding time.Time arguments via TimeArgForTest.
func prunerExec(t *testing.T, ctx context.Context, b backend, s *store.Store, query string, args ...any) {
	t.Helper()
	q, err := database.From(b.conn)
	require.NoError(t, err, "%s: database.From", b.name)
	_, err = q.Exec(ctx, b.dialect.Rebind(query), args...)
	require.NoError(t, err, "%s: prunerExec", b.name)
}

// prunerCount executes a COUNT query with a single ? placeholder and one string
// argument, returning the count as an int.
func prunerCount(t *testing.T, ctx context.Context, b backend, query string, arg string) int {
	t.Helper()
	q, err := database.From(b.conn)
	require.NoError(t, err, "%s: database.From", b.name)
	var count int
	err = q.QueryRow(ctx, b.dialect.Rebind(query), arg).Scan(&count)
	require.NoError(t, err, "%s: COUNT scan: %s", b.name, query)
	return count
}
