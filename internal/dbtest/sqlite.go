package dbtest

import (
	"database/sql"
	"path/filepath"
	"testing"

	store "github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite" // register "sqlite" driver
)

// RunTestSQLite opens a fresh file-backed SQLite database in t.TempDir(),
// configures it with WAL journal mode, a 5 000 ms busy timeout, and foreign-key
// enforcement, applies all wrkflw SQLite migrations via [store.MigrateSQLite],
// and returns the *sql.DB torn down via t.Cleanup.
//
// The helper is in-process: no Docker daemon is required. SetMaxOpenConns(1)
// enforces single-writer access, matching SQLite's write-serialisation model.
// This is a lightweight, test-only analogue of [RunTestDatabase] / [RunTestMySQL]
// for use when a real networked database is unnecessary.
func RunTestSQLite(t *testing.T) *sql.DB {
	t.Helper()

	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "test.db") +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)"

	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err, "open sqlite")
	db.SetMaxOpenConns(1) // single-writer: sqlite does not support concurrent writes
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, db.PingContext(t.Context()), "ping sqlite")
	require.NoError(t, store.MigrateSQLite(t.Context(), db), "migrate sqlite")

	return db
}
