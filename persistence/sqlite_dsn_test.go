package persistence_test

// sqlite_dsn_test.go — the mattn/go-sqlite3 DSN form (`_busy_timeout=5000`)
// this package once documented is silently ignored by the pinned pure-Go driver
// `modernc.org/sqlite`, and every consumer-facing godoc example omitted the busy
// timeout altogether. Measured: a SQLite pool with no busy timeout fails 174–195
// of 200 concurrent operations, so an inert timeout in the canonical example is
// the dangerous half of that combination.
//
// These tests derive the DSNs from the documents themselves, so the guard cannot
// drift away from the text it protects. Container-free: modernc.org/sqlite is
// pure Go.

import (
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite" // register the pinned "sqlite" driver
)

// readBusyTimeout opens dsnQuery against a throwaway database file and returns
// the effective `PRAGMA busy_timeout` in milliseconds.
//
// Only the query part of a documented DSN is exercised (that is where the pragma
// parameters live); the file path is redirected into t.TempDir() so a doc example
// naming "app.db" does not write into the repository.
func readBusyTimeout(t *testing.T, dsnQuery string) int {
	t.Helper()

	dsn := "file:" + filepath.Join(t.TempDir(), "probe.db")
	if dsnQuery != "" {
		dsn += "?" + dsnQuery
	}

	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err, "open %s", dsn)
	t.Cleanup(func() { _ = db.Close() })

	var ms int
	require.NoError(t, db.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&ms),
		"read back PRAGMA busy_timeout for %s", dsn)
	return ms
}

// TestSQLiteDSNSyntaxOnPinnedDriver executes the premise: on
// modernc.org/sqlite only the `_pragma=name(value)` form has any effect, and the
// mattn/go-sqlite3 `_busy_timeout=` form is accepted and silently ignored.
//
// It is evidence, not the gate — it passes today and documents driver behaviour.
// The gate is TestDocumentedSQLiteDSNsSetBusyTimeout below.
func TestSQLiteDSNSyntaxOnPinnedDriver(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		query  string
		assert func(t *testing.T, busyTimeoutMS int)
	}

	cases := []testCase{
		{
			name:  "mattn syntax is silently ignored",
			query: "_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on",
			assert: func(t *testing.T, ms int) {
				assert.Equal(t, 0, ms,
					"the pinned driver must ignore the mattn `_busy_timeout=` form")
			},
		},
		{
			name:  "pinned-driver syntax takes effect",
			query: "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)",
			assert: func(t *testing.T, ms int) {
				assert.Equal(t, 5000, ms,
					"the `_pragma=busy_timeout(5000)` form must set the pragma")
			},
		},
		{
			name:  "omitting the pragma leaves the timeout at zero",
			query: "_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)",
			assert: func(t *testing.T, ms int) {
				assert.Equal(t, 0, ms, "SQLite's default busy timeout is zero")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, readBusyTimeout(t, tc.query))
		})
	}
}

// sqliteDocDSN is a DSN literal harvested from a consumer-facing godoc example.
type sqliteDocDSN struct {
	file  string
	query string
}

// sqliteOpenLiteral matches `sql.Open("sqlite", "file:…")` in a godoc example.
var sqliteOpenLiteral = regexp.MustCompile(`sql\.Open\("sqlite",\s*"(file:[^"]*)"\)`)

// harvestDocDSNs derives every SQLite DSN literal from the non-test sources of
// the persistence package. Deriving the set from the sources (rather than
// hard-coding a count) means a new godoc example is covered the moment it lands.
func harvestDocDSNs(t *testing.T) []sqliteDocDSN {
	t.Helper()

	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	var found []sqliteDocDSN
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		require.NoError(t, err)

		for _, m := range sqliteOpenLiteral.FindAllStringSubmatch(string(src), -1) {
			dsn := m[1]
			file, query, _ := strings.Cut(dsn, "?")
			found = append(found, sqliteDocDSN{file: name + ": " + file, query: query})
		}
	}
	return found
}

// TestDocumentedSQLiteDSNsSetBusyTimeout asserts that every SQLite DSN a
// consumer can copy out of this package's godoc actually sets a non-zero busy
// timeout on the pinned driver.
//
// What makes it fail before the fix: all eleven godoc DSN literals in
// persistence/sqlite.go and persistence/humantask.go read
// `file:app.db?_pragma=journal_mode(WAL)[&_pragma=foreign_keys(1)]` — correct
// syntax, but no busy_timeout at all — so the readback is 0 for every one of them.
func TestDocumentedSQLiteDSNsSetBusyTimeout(t *testing.T) {
	t.Parallel()

	dsns := harvestDocDSNs(t)
	require.NotEmpty(t, dsns,
		"control: the harvester found no godoc DSN literals, so this test would pass vacuously")

	for _, d := range dsns {
		t.Run(d.file, func(t *testing.T) {
			t.Parallel()
			assert.Positive(t, readBusyTimeout(t, d.query),
				"documented DSN %q leaves PRAGMA busy_timeout at 0 on modernc.org/sqlite; "+
					"a consumer copying it gets the configuration measured to fail "+
					"174–195 of 200 concurrent operations", d.query)
		})
	}
}
