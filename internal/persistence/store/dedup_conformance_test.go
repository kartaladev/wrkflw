package store_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/database/transaction"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
)

// TestDedupSeen_FirstThenDuplicate verifies that Deduper.Seen returns true on the
// first observation of a (subscriber, messageID) pair and false on all subsequent
// observations, across all three dialects.
func TestDedupSeen_FirstThenDuplicate(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		d := store.NewDeduper(b.conn, b.dialect)

		// First Seen within an ambient transaction — must return true (first-time).
		q, ctx, err := transaction.Begin(t.Context(), b.conn)
		require.NoError(t, err, "%s: Begin", b.name)

		first, err := d.Seen(ctx, "sub-a", "msg-1")
		require.NoError(t, err, "%s: Seen first", b.name)
		assert.True(t, first, "%s: first observation must return true", b.name)

		require.NoError(t, q.Commit(ctx), "%s: Commit first tx", b.name)

		// Second Seen (same pair, new context / no ambient tx) — must return false (duplicate).
		second, err := d.Seen(t.Context(), "sub-a", "msg-1")
		require.NoError(t, err, "%s: Seen second", b.name)
		assert.False(t, second, "%s: second observation (duplicate) must return false", b.name)
	})
}

// TestDedupSeen_DifferentSubscribersOrMessages verifies that the (subscriber,
// message_id) pair is the unique key: different subscribers with the same message
// ID are independent, and the same subscriber with different IDs are independent.
func TestDedupSeen_DifferentSubscribersOrMessages(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		d := store.NewDeduper(b.conn, b.dialect)

		for _, tc := range []struct {
			sub string
			id  string
		}{
			{"sub-x", "msg-2"},
			{"sub-y", "msg-2"}, // same msg-id, different subscriber → first-time
			{"sub-x", "msg-3"}, // same subscriber, different msg-id → first-time
		} {
			first, err := d.Seen(t.Context(), tc.sub, tc.id)
			require.NoError(t, err, "%s: Seen %s/%s", b.name, tc.sub, tc.id)
			assert.True(t, first, "%s: %s/%s must be first-time", b.name, tc.sub, tc.id)
		}
	})
}

// TestDedupSeen_RollbackParticipation verifies that when the ambient owning
// transaction rolls back, the Seen mark is also rolled back — i.e. the Deduper
// joins the caller's ambient transaction and does not independently commit.
func TestDedupSeen_RollbackParticipation(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		d := store.NewDeduper(b.conn, b.dialect)

		// Begin an ambient transaction and call Seen inside it.
		q, ctx, err := transaction.Begin(t.Context(), b.conn)
		require.NoError(t, err, "%s: Begin", b.name)

		first, err := d.Seen(ctx, "sub-rollback", "msg-rollback")
		require.NoError(t, err, "%s: Seen in tx", b.name)
		assert.True(t, first, "%s: must be first-time inside the tx", b.name)

		// Roll back the owning transaction — the Seen mark must NOT persist.
		require.NoError(t, q.Rollback(ctx), "%s: Rollback", b.name)

		// Count the row directly to confirm absence.
		rowCount := dedupRowCount(t, b, "sub-rollback", "msg-rollback")
		assert.Equal(t, 0, rowCount, "%s: row must not exist after rollback", b.name)

		// A fresh Seen (no ambient tx) must again return true — confirming the
		// rollback erased the mark.
		again, err := d.Seen(t.Context(), "sub-rollback", "msg-rollback")
		require.NoError(t, err, "%s: Seen after rollback", b.name)
		assert.True(t, again, "%s: after rollback the pair must be first-time again", b.name)
	})
}

// TestDedupPrune verifies that Prune deletes rows whose processed_at is strictly
// before the supplied cutoff and leaves rows at or after the cutoff untouched, on
// all three dialects. This test specifically exercises the SQLite TEXT-timestamp
// comparison path — a regression test for the strftime DEFAULT vs RFC3339Nano
// cutoff format mismatch (see implementation comments).
func TestDedupPrune(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		d := store.NewDeduper(b.conn, b.dialect)

		// Obtain a raw Querier to insert rows with explicit processed_at values so
		// we can control the timestamp without sleeping.
		s := store.New(b.conn, b.dialect)

		cutoff := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
		oldTime := cutoff.Add(-24 * time.Hour) // strictly before — should be pruned
		newTime := cutoff.Add(24 * time.Hour)  // after — must survive

		// Insert one "old" row and one "new" row via a raw helper that uses the
		// same timeArg encoding the Deduper uses.
		insertDedupRow(t, s, b, "sub-prune", "msg-old", oldTime)
		insertDedupRow(t, s, b, "sub-prune", "msg-new", newTime)

		n, err := d.Prune(t.Context(), cutoff)
		require.NoError(t, err, "%s: Prune", b.name)
		assert.Equal(t, int64(1), n, "%s: Prune must delete exactly 1 row", b.name)

		// Old row must be gone.
		assert.Equal(t, 0, dedupRowCount(t, b, "sub-prune", "msg-old"),
			"%s: old row must be pruned", b.name)
		// New row must survive.
		assert.Equal(t, 1, dedupRowCount(t, b, "sub-prune", "msg-new"),
			"%s: new row must survive Prune", b.name)
	})
}

// insertDedupRow inserts a row into wrkflw_processed_message with an explicit
// processed_at value, bypassing the DEFAULT so tests can control the timestamp.
// It uses the same timeArg encoding the Deduper uses to guarantee format parity
// on SQLite (TEXT comparison via RFC3339Nano string).
func insertDedupRow(t *testing.T, s *store.Store, b backend, sub, id string, at time.Time) {
	t.Helper()
	q := s.QuerierForTest(t.Context())
	_, err := q.Exec(t.Context(),
		b.dialect.Rebind(
			`INSERT INTO wrkflw_processed_message (subscriber, message_id, processed_at)
			 VALUES (?, ?, ?)`),
		sub, id, s.TimeArgForTest(at),
	)
	require.NoError(t, err, "%s: insertDedupRow %s/%s", b.name, sub, id)
}

// dedupRowCount returns the count of wrkflw_processed_message rows matching
// (subscriber, message_id), using a plain pool query (no ambient tx).
func dedupRowCount(t *testing.T, b backend, sub, id string) int {
	t.Helper()
	q, err := database.From(b.conn)
	require.NoError(t, err, "%s: database.From", b.name)

	var count int
	row := q.QueryRow(t.Context(),
		b.dialect.Rebind(
			`SELECT COUNT(*) FROM wrkflw_processed_message WHERE subscriber = ? AND message_id = ?`),
		sub, id,
	)
	// Both *sql.DB and pgxpool.Pool surface the count as an int-compatible value.
	// We try scanning into int, then sql.NullInt64 as a fallback for drivers that
	// differ in their Scan target preference.
	if err := row.Scan(&count); err != nil {
		// Try with NullInt64 for drivers that return NULL-capable types.
		var n sql.NullInt64
		row = q.QueryRow(t.Context(),
			b.dialect.Rebind(
				`SELECT COUNT(*) FROM wrkflw_processed_message WHERE subscriber = ? AND message_id = ?`),
			sub, id,
		)
		require.NoError(t, row.Scan(&n), "%s: COUNT scan", b.name)
		return int(n.Int64)
	}
	return count
}
