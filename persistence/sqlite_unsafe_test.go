package persistence_test

// sqlite_unsafe_test.go — OpenSQLite never checked the single-writer contract
// its own godoc documents.
//
// ⚠ The causal correction from triage is what these cases encode: a pool wider
// than one connection is NOT by itself the hazard (0 failures across 4 runs with
// busy_timeout set); the hazard is the COMBINATION of a wide pool and an
// unset busy_timeout (174–195 of 200 operations failed in 4–17 ms). A check that
// fired on pool size alone would warn about a configuration measured to be safe,
// and consumers would learn to ignore it.
//
// ⚠ dbtest.RunTestSQLite is deliberately NOT used here: it applies WAL,
// busy_timeout(5000) and SetMaxOpenConns(1), i.e. it constructs precisely the
// SAFE configuration, which would make the unsafe-path assertions unable to fail.
// Every fixture below opens its own *sql.DB by hand.
//
// Container-free: modernc.org/sqlite is pure Go.

import (
	"bytes"
	"database/sql"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite" // register the pinned "sqlite" driver

	"github.com/kartaladev/wrkflw/persistence"
)

// openRawSQLite opens a file-backed SQLite database with exactly the pragma
// query and pool width given — no safe defaults applied.
func openRawSQLite(t *testing.T, query string, maxOpenConns int) *sql.DB {
	t.Helper()

	dsn := "file:" + filepath.Join(t.TempDir(), "unsafe.db")
	if query != "" {
		dsn += "?" + query
	}
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(maxOpenConns)
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, db.PingContext(t.Context()))
	return db
}

// TestWarnUnsafeSQLite covers the single dangerous combination and, just as
// importantly, the three safe ones that must stay silent.
func TestWarnUnsafeSQLite(t *testing.T) {
	t.Parallel()

	const withTimeout = "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	const noTimeout = "_pragma=journal_mode(WAL)"

	tests := map[string]struct {
		query        string
		maxOpenConns int
		assert       func(t *testing.T, logged string)
	}{
		"wide pool without busy timeout warns": {
			query:        noTimeout,
			maxOpenConns: 8,
			assert: func(t *testing.T, logged string) {
				assert.Contains(t, logged, persistence.WarnMsgSQLiteBusyTimeout)
			},
		},
		"unlimited pool without busy timeout warns": {
			// SetMaxOpenConns(0) means unlimited — the database/sql default, and
			// the widest pool of all.
			query:        noTimeout,
			maxOpenConns: 0,
			assert: func(t *testing.T, logged string) {
				assert.Contains(t, logged, persistence.WarnMsgSQLiteBusyTimeout)
			},
		},
		"wide pool WITH busy timeout stays silent": {
			// The causal correction as an assertion: this configuration was
			// measured to produce zero failures, so it must not be flagged.
			query:        withTimeout,
			maxOpenConns: 8,
			assert: func(t *testing.T, logged string) {
				assert.NotContains(t, logged, persistence.WarnMsgSQLiteBusyTimeout)
				assert.Empty(t, strings.TrimSpace(logged), "a safe configuration must warn nothing")
			},
		},
		"single connection without busy timeout stays silent": {
			// Single-writer serialisation is the contract; in-process contention
			// cannot arise, so the unset timeout is not the measured hazard.
			query:        noTimeout,
			maxOpenConns: 1,
			assert: func(t *testing.T, logged string) {
				assert.NotContains(t, logged, persistence.WarnMsgSQLiteBusyTimeout)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			db := openRawSQLite(t, tc.query, tc.maxOpenConns)

			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

			persistence.WarnUnsafeSQLite(t.Context(), logger, db)
			tc.assert(t, buf.String())
		})
	}
}

func TestWarnUnsafeSQLiteNilArgumentsDoNotPanic(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() {
		persistence.WarnUnsafeSQLite(t.Context(), nil, nil)
	})
}

// TestOpenSQLiteWarnsOnUnsafePool asserts the probe actually runs at open time —
// the point of the item is that OpenSQLite accepted MaxOpenConns(8) silently.
//
// Not parallel: it swaps the process-wide default logger, which is how
// OpenSQLite reports (it takes no logger it can read back — Option is an alias
// of the opaque store.Option).
func TestOpenSQLiteWarnsOnUnsafePool(t *testing.T) {
	db := openRawSQLite(t, "_pragma=journal_mode(WAL)", 8)
	require.NoError(t, persistence.MigrateSQLite(t.Context(), db))

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	st, err := persistence.OpenSQLite(t.Context(), db)
	require.NoError(t, err, "the check must WARN, not reject — rejecting would break existing consumers")
	require.NotNil(t, st)

	assert.Contains(t, buf.String(), persistence.WarnMsgSQLiteBusyTimeout,
		"OpenSQLite accepted a wide pool with no busy timeout without saying anything")
}

// TestOpenSQLiteStaysSilentOnSafePool is the control: the same call path on the
// configuration dbtest.RunTestSQLite builds must emit nothing, or the warning is
// noise on every correct deployment.
func TestOpenSQLiteStaysSilentOnSafePool(t *testing.T) {
	db := openRawSQLite(t, "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", 1)
	require.NoError(t, persistence.MigrateSQLite(t.Context(), db))

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	_, err := persistence.OpenSQLite(t.Context(), db)
	require.NoError(t, err)

	assert.NotContains(t, buf.String(), persistence.WarnMsgSQLiteBusyTimeout)
}
