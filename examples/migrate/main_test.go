package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sqliteDSN returns a file-backed SQLite DSN in t.TempDir() so that schema
// state persists across separate run() calls within the same test (each call
// opens and closes its own *sql.DB; a named in-memory database is destroyed
// when its last connection closes, which would lose state between calls).
func sqliteDSN(t *testing.T) string {
	t.Helper()
	return "file:" + filepath.Join(t.TempDir(), "test.db") +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)"
}

func TestRun_SQLiteUpThenStatusThenVersion(t *testing.T) {
	dsn := sqliteDSN(t)

	var out bytes.Buffer
	require.Equal(t, 0, run([]string{"-dialect=sqlite", "-dsn=" + dsn, "up"}, &out), "up must exit 0")

	out.Reset()
	require.Equal(t, 0, run([]string{"-dialect=sqlite", "-dsn=" + dsn, "version"}, &out))
	assert.Contains(t, out.String(), "current version: 4", "version should report head 4")

	out.Reset()
	require.Equal(t, 0, run([]string{"-dialect=sqlite", "-dsn=" + dsn, "status"}, &out))
	assert.Contains(t, strings.ToLower(out.String()), "applied")
}

func TestRun_UnknownSubcommand(t *testing.T) {
	var out bytes.Buffer
	dsn := "file:" + filepath.Join(t.TempDir(), "x.db") + "?_pragma=journal_mode(WAL)"
	assert.Equal(t, 2, run([]string{"-dialect=sqlite", "-dsn=" + dsn, "frobnicate"}, &out))
}

func TestRun_MissingArgs(t *testing.T) {
	var out bytes.Buffer
	// no flags at all: missing dialect, dsn, and subcommand → usage error
	assert.Equal(t, 2, run([]string{}, &out))
}

func TestRun_UnknownDialect(t *testing.T) {
	var out bytes.Buffer
	assert.Equal(t, 1, run([]string{"-dialect=baddriver", "-dsn=x", "up"}, &out))
	assert.Contains(t, out.String(), "unknown dialect")
}

func TestRun_SQLiteUpToAndDownTo(t *testing.T) {
	dsn := sqliteDSN(t)

	var out bytes.Buffer
	// upto 1
	require.Equal(t, 0, run([]string{"-dialect=sqlite", "-dsn=" + dsn, "upto", "1"}, &out), "upto 1 must exit 0")

	// downto 0 (rolls back all)
	out.Reset()
	require.Equal(t, 0, run([]string{"-dialect=sqlite", "-dsn=" + dsn, "downto", "0"}, &out), "downto 0 must exit 0")

	// version after full rollback → 0
	out.Reset()
	require.Equal(t, 0, run([]string{"-dialect=sqlite", "-dsn=" + dsn, "version"}, &out))
	assert.Contains(t, out.String(), "current version: 0")
}

func TestRun_SQLiteDown(t *testing.T) {
	dsn := sqliteDSN(t)

	var out bytes.Buffer
	// apply all, then roll back one
	require.Equal(t, 0, run([]string{"-dialect=sqlite", "-dsn=" + dsn, "up"}, &out))
	out.Reset()
	require.Equal(t, 0, run([]string{"-dialect=sqlite", "-dsn=" + dsn, "down"}, &out))
}

func TestRun_UpToMissingVersionArg(t *testing.T) {
	dsn := sqliteDSN(t)
	var out bytes.Buffer
	// upto without version argument → usage error
	assert.Equal(t, 2, run([]string{"-dialect=sqlite", "-dsn=" + dsn, "upto"}, &out))
}

func TestRun_BadFlagParsing(t *testing.T) {
	var out bytes.Buffer
	// passing an unknown flag → parse error → exit 2
	assert.Equal(t, 2, run([]string{"-zzz=bad"}, &out))
}

func TestRun_UpToInvalidVersion(t *testing.T) {
	dsn := sqliteDSN(t)
	var out bytes.Buffer
	// "abc" is not a valid int64 → runtime error
	require.Equal(t, 0, run([]string{"-dialect=sqlite", "-dsn=" + dsn, "up"}, &out))
	out.Reset()
	assert.Equal(t, 1, run([]string{"-dialect=sqlite", "-dsn=" + dsn, "upto", "abc"}, &out))
	assert.Contains(t, out.String(), "invalid version")
}
