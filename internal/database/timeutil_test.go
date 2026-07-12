package database_test

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/internal/database"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite" // register "sqlite" driver for TestProbeUTCPassesOnSQLite
)

func TestUTCNormalizes(t *testing.T) {
	loc := time.FixedZone("WIB", 7*3600)
	in := time.Date(2020, 1, 2, 10, 0, 0, 0, loc)
	got := database.UTC(in)
	if _, off := got.Zone(); off != 0 {
		t.Fatalf("zone offset = %d, want 0", off)
	}
	if !got.Equal(in) {
		t.Fatalf("instant changed: %v != %v", got, in)
	}
}

func TestProbeUTCPassesOnPostgres(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	q, _ := database.From(pool)
	if err := database.ProbeUTC(t.Context(), q, database.Postgres); err != nil {
		t.Fatalf("probe: %v", err)
	}
}

// TestProbeUTCPassesOnMySQL verifies the MySQL probe path (loc=UTC DSN) passes.
// The shared RunTestMySQL helper configures parseTime=true&loc=UTC, so the
// TIMESTAMP literal is read back as UTC and the instant matches.
func TestProbeUTCPassesOnMySQL(t *testing.T) {
	db := dbtest.RunTestMySQL(t)
	q, err := database.From(db)
	require.NoError(t, err)
	require.NoError(t, database.ProbeUTC(t.Context(), q, database.MySQL))
}

// TestProbeUTCPassesOnSQLite verifies the SQLite probe path passes on an
// in-process SQLite database opened directly (no Docker required). The test
// imports modernc.org/sqlite as a blank driver import in this test file only;
// that import does not appear in non-test code so the extraction constraint
// (ADR-0079) is not violated.
func TestProbeUTCPassesOnSQLite(t *testing.T) {
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "probe.db") + "?_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err, "open sqlite")
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.PingContext(t.Context()), "ping sqlite")

	q, err := database.From(db)
	require.NoError(t, err)
	require.NoError(t, database.ProbeUTC(t.Context(), q, database.SQLite))
}

// TestProbeUTCUnknownDialect verifies that an unknown Dialect constant is
// rejected with a descriptive error (covers the default branch in ProbeUTC).
func TestProbeUTCUnknownDialect(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	q, err := database.From(pool)
	require.NoError(t, err)

	err = database.ProbeUTC(t.Context(), q, database.Dialect(99))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown dialect")
}
