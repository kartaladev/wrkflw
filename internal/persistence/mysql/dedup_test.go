package mysql_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	mypkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
)

// openTx opens a transaction, calls fn, commits on success.
func openTx(t *testing.T, db *sql.DB, fn func(*sql.Tx) error) {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	require.NoError(t, fn(tx))
	require.NoError(t, tx.Commit())
}

// TestDeduper_FirstSeenThenDuplicate verifies that Seen returns true on the
// first call for a (subscriber, messageID) pair and false on a subsequent call
// with the same pair (idempotent-consumer pattern via INSERT IGNORE).
func TestDeduper_FirstSeenThenDuplicate(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)

	d := mypkg.NewDeduper(db)

	// First observation: must be firstTime=true.
	openTx(t, db, func(tx *sql.Tx) error {
		firstTime, err := d.Seen(t.Context(), tx, "sub-a", "msg-1")
		require.NoError(t, err)
		require.True(t, firstTime, "first observation must return true")
		return nil
	})

	// Second observation: must be firstTime=false (duplicate).
	openTx(t, db, func(tx *sql.Tx) error {
		firstTime, err := d.Seen(t.Context(), tx, "sub-a", "msg-1")
		require.NoError(t, err)
		require.False(t, firstTime, "second observation must return false (duplicate)")
		return nil
	})

	// Different subscriber, same message ID — must be first time (distinct pair).
	openTx(t, db, func(tx *sql.Tx) error {
		firstTime, err := d.Seen(t.Context(), tx, "sub-b", "msg-1")
		require.NoError(t, err)
		require.True(t, firstTime, "different subscriber with same message ID must be first time")
		return nil
	})

	// Different message ID, same subscriber — must be first time.
	openTx(t, db, func(tx *sql.Tx) error {
		firstTime, err := d.Seen(t.Context(), tx, "sub-a", "msg-2")
		require.NoError(t, err)
		require.True(t, firstTime, "different message ID must be first time")
		return nil
	})
}

// TestDeduper_SeenErrorPath verifies that Seen propagates an ExecContext error
// by using a cancelled context so the underlying query fails.
func TestDeduper_SeenErrorPath(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)
	d := mypkg.NewDeduper(db)

	tx, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	// Use a cancelled context so the ExecContext call fails immediately.
	cancelledCtx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	_, err = d.Seen(cancelledCtx, tx, "sub", "msg")
	require.Error(t, err, "Seen must propagate a cancelled context error")
}

// TestDeduper_PruneOnClosedDB verifies that Prune propagates a DB error.
func TestDeduper_PruneOnClosedDB(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)
	d := mypkg.NewDeduper(db)
	require.NoError(t, db.Close())

	_, err := d.Prune(t.Context(), time.Now().Add(time.Hour))
	require.Error(t, err, "Prune must propagate the DB error")
	require.Contains(t, err.Error(), "workflow-persistence-mysql: deduper: prune")
}

// TestDeduper_Prune deletes processed messages strictly older than a cutoff.
func TestDeduper_Prune(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)

	d := mypkg.NewDeduper(db)

	openTx(t, db, func(tx *sql.Tx) error {
		_, err := d.Seen(t.Context(), tx, "sub", "msg-a")
		return err
	})
	openTx(t, db, func(tx *sql.Tx) error {
		_, err := d.Seen(t.Context(), tx, "sub", "msg-b")
		return err
	})

	var count int
	require.NoError(t, db.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM wrkflw_processed_message WHERE subscriber = 'sub'`,
	).Scan(&count))
	require.Equal(t, 2, count)

	// Prune with a future cutoff: all rows are older than "now + 1h".
	n, err := d.Prune(t.Context(), time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.EqualValues(t, 2, n, "Prune must delete both rows older than cutoff")

	require.NoError(t, db.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM wrkflw_processed_message WHERE subscriber = 'sub'`,
	).Scan(&count))
	require.Equal(t, 0, count)
}
