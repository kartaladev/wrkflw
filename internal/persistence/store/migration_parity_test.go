package store_test

import (
	"context"
	"database/sql"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
)

// colFacts is the logical (dialect-independent) shape of one column. Physical
// types are intentionally NOT compared — JSONB/JSON/TEXT and TIMESTAMPTZ/
// DATETIME(6)/TEXT legitimately differ per dialect.
type colFacts struct {
	Nullable   bool
	PrimaryKey bool
}

// logicalSchema is table -> column -> facts, restricted to wrkflw_* tables.
type logicalSchema map[string]map[string]colFacts

// TestMigrationParity_LogicalSchemaConverges asserts that applying all migrations
// for Postgres, MySQL, and SQLite yields the same logical schema: identical
// wrkflw_* table names, column names, nullability, and PK membership.
//
// Physical type differences (JSONB/JSON/TEXT, TIMESTAMPTZ/DATETIME(6)/TEXT) are
// intentionally excluded from the comparison — they are documented dialect trade-
// offs that do not affect the engine's correctness.
//
// One additional normalization is applied before comparison: MySQL names the
// wrkflw_journal payload column "trigger_" because "trigger" is a reserved word
// in MySQL SQL syntax. Postgres and SQLite use "trigger". The dialect interface
// already surfaces this via JournalTriggerColumn(). We rename "trigger_" →
// "trigger" in the MySQL schema before the final equality assertion so that the
// reserved-word workaround does not cause a spurious parity failure.
func TestMigrationParity_LogicalSchemaConverges(t *testing.T) {
	ctx := context.Background()

	// SQLite (always available, no Docker).
	sqliteDB, err := sql.Open("sqlite", "file:parity?mode=memory&cache=shared")
	require.NoError(t, err)
	sqliteDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqliteDB.Close() })
	sm, err := store.NewSQLiteMigrator(sqliteDB)
	require.NoError(t, err)
	require.NoError(t, sm.Up(ctx))
	sqliteSchema := introspectSQLite(t, sqliteDB)

	// Postgres + MySQL (Docker-gated; dbtest skips the test when unavailable).
	pool := dbtest.RunTestDatabase(t)
	pm, err := store.NewPostgresMigrator(pool)
	require.NoError(t, err)
	require.NoError(t, pm.Up(ctx))
	pgSchema := introspectPostgres(t, pool)

	mysqlDB := dbtest.RunTestMySQL(t) // already migrated
	mysqlSchema := introspectMySQL(t, mysqlDB)

	// Normalize reserved-word column rename: MySQL uses "trigger_" for the
	// wrkflw_journal payload column; Postgres and SQLite use "trigger".
	// Rename before comparison so this single intentional divergence is
	// transparent to the guardrail.
	normalizeMySQLTriggerColumn(mysqlSchema)

	require.Equal(t, normalize(pgSchema), normalize(sqliteSchema), "postgres vs sqlite logical schema")
	require.Equal(t, normalize(pgSchema), normalize(mysqlSchema), "postgres vs mysql logical schema")
}

// normalize forces PK columns to NOT NULL across all dialects (SQLite's INTEGER
// PRIMARY KEY rowid is implicitly nullable in table_info), removing the one
// legitimate cross-dialect nullability quirk before comparison.
func normalize(s logicalSchema) logicalSchema {
	for _, cols := range s {
		for name, f := range cols {
			if f.PrimaryKey {
				f.Nullable = false
				cols[name] = f
			}
		}
	}
	return s
}

// normalizeMySQLTriggerColumn renames the MySQL-specific journal payload column
// name back to the canonical name used by Postgres and SQLite. MySQL disallows
// "trigger" as a column identifier (reserved word), so the migration uses the
// alias returned by dialect.NewMySQL().JournalTriggerColumn() ("trigger_").
// The dialect's JournalTriggerColumn() method returns the correct name at query
// time; here we normalise the introspected name so the parity comparison is not
// tripped by this one intentional asymmetry.
func normalizeMySQLTriggerColumn(s logicalSchema) {
	// mysqlCol is the MySQL-specific column name; canonicalCol is the name used
	// by Postgres and SQLite. Both are sourced from the dialect package so this
	// function stays in sync with the migration automatically.
	mysqlCol := dialect.NewMySQL().JournalTriggerColumn()        // "trigger_"
	canonicalCol := dialect.NewPostgres().JournalTriggerColumn() // "trigger"
	journal, ok := s["wrkflw_journal"]
	if !ok {
		return
	}
	if f, exists := journal[mysqlCol]; exists {
		journal[canonicalCol] = f
		delete(journal, mysqlCol)
	}
}

func introspectPostgres(t *testing.T, pool *pgxpool.Pool) logicalSchema {
	t.Helper()
	ctx := context.Background()
	sc := logicalSchema{}
	rows, err := pool.Query(ctx, `
		SELECT table_name, column_name, (is_nullable = 'YES')
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name LIKE 'wrkflw_%'`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var tbl, col string
		var nullable bool
		require.NoError(t, rows.Scan(&tbl, &col, &nullable))
		if sc[tbl] == nil {
			sc[tbl] = map[string]colFacts{}
		}
		sc[tbl][col] = colFacts{Nullable: nullable}
	}
	require.NoError(t, rows.Err(), "postgres columns iteration error")
	pkRows, err := pool.Query(ctx, `
		SELECT tc.table_name, kcu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
		WHERE tc.constraint_type = 'PRIMARY KEY'
		  AND tc.table_schema = 'public' AND tc.table_name LIKE 'wrkflw_%'`)
	require.NoError(t, err)
	defer pkRows.Close()
	for pkRows.Next() {
		var tbl, col string
		require.NoError(t, pkRows.Scan(&tbl, &col))
		// Guard against a PK referencing a table not seen in the columns query
		// (should never happen but prevents a nil-map panic if it does).
		if sc[tbl] == nil {
			sc[tbl] = map[string]colFacts{}
		}
		f := sc[tbl][col]
		f.PrimaryKey = true
		sc[tbl][col] = f
	}
	require.NoError(t, pkRows.Err(), "postgres PKs iteration error")
	return sc
}

func introspectMySQL(t *testing.T, db *sql.DB) logicalSchema {
	t.Helper()
	sc := logicalSchema{}
	rows, err := db.Query(`
		SELECT table_name, column_name, (is_nullable = 'YES'), (column_key = 'PRI')
		FROM information_schema.columns
		WHERE table_schema = DATABASE() AND table_name LIKE 'wrkflw_%'`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var tbl, col string
		var nullable, pk bool
		require.NoError(t, rows.Scan(&tbl, &col, &nullable, &pk))
		if sc[tbl] == nil {
			sc[tbl] = map[string]colFacts{}
		}
		sc[tbl][col] = colFacts{Nullable: nullable, PrimaryKey: pk}
	}
	require.NoError(t, rows.Err(), "mysql columns iteration error")
	return sc
}

func introspectSQLite(t *testing.T, db *sql.DB) logicalSchema {
	t.Helper()
	sc := logicalSchema{}
	tblRows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'wrkflw_%'`)
	require.NoError(t, err)
	var tables []string
	for tblRows.Next() {
		var name string
		require.NoError(t, tblRows.Scan(&name))
		tables = append(tables, name)
	}
	require.NoError(t, tblRows.Err(), "sqlite table list iteration error")
	_ = tblRows.Close()
	sort.Strings(tables)
	for _, tbl := range tables {
		info, err := db.Query(`SELECT name, "notnull", pk FROM pragma_table_info(?)`, tbl)
		require.NoError(t, err)
		sc[tbl] = map[string]colFacts{}
		for info.Next() {
			var name string
			var notnull, pk int
			require.NoError(t, info.Scan(&name, &notnull, &pk))
			sc[tbl][name] = colFacts{Nullable: notnull == 0, PrimaryKey: pk > 0}
		}
		require.NoError(t, info.Err(), "sqlite pragma_table_info iteration error for table %s", tbl)
		_ = info.Close()
	}
	return sc
}

// indexNames returns the explicitly-named indexes on table for one backend.
// Implicit indexes — those the engine creates to back a PRIMARY KEY, a UNIQUE
// constraint, or (on MySQL) a foreign key — are excluded: they are named
// differently by every engine (PRIMARY / *_pkey / sqlite_autoindex_*) and
// comparing them reports divergence that is not divergence.
func indexNames(t *testing.T, query func(sqlText string) ([]string, error), sqlText string) map[string]struct{} {
	t.Helper()
	got, err := query(sqlText)
	require.NoError(t, err)
	out := make(map[string]struct{}, len(got))
	for _, n := range got {
		out[n] = struct{}{}
	}
	return out
}

// TestMigrationTimersKeysetIndex asserts every backend carries the composite
// keyset index the paged armed-timer read seeks on, and no longer carries the
// single-column next_run index it replaces (ADR-0159).
//
// This must FAIL LOUDLY rather than skip when the index is absent. goose keys
// by version and stores no checksum, so an in-place edit of 0001_init.sql never
// re-applies to a database already migrated at version 1 — a persistent local
// database or a reused test DSN would silently keep the old index and lose the
// benefit entirely.
func TestMigrationTimersKeysetIndex(t *testing.T) {
	ctx := context.Background()

	const (
		wantIndex = "wrkflw_timers_keyset_idx"
		goneIndex = "wrkflw_timers_next_run_idx"
	)

	assertIndexes := func(t *testing.T, backend string, names map[string]struct{}) {
		t.Helper()
		_, hasWant := names[wantIndex]
		require.True(t, hasWant, "%s: %s missing — a database migrated before the in-place 0001_init.sql edit keeps the old index forever", backend, wantIndex)
		_, hasGone := names[goneIndex]
		require.False(t, hasGone, "%s: %s must be gone, it is a prefix of the composite index", backend, goneIndex)
	}

	t.Run("sqlite", func(t *testing.T) {
		db, err := sql.Open("sqlite", "file:keysetidx?mode=memory&cache=shared")
		require.NoError(t, err)
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = db.Close() })
		m, err := store.NewSQLiteMigrator(db)
		require.NoError(t, err)
		require.NoError(t, m.Up(ctx))

		names := indexNames(t, func(sqlText string) ([]string, error) {
			return scanStrings(db.QueryContext(ctx, sqlText))
		}, `SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'wrkflw_timers' AND name NOT LIKE 'sqlite_autoindex%'`)
		assertIndexes(t, "sqlite", names)
	})

	t.Run("postgres", func(t *testing.T) {
		pool := dbtest.RunTestDatabase(t)
		m, err := store.NewPostgresMigrator(pool)
		require.NoError(t, err)
		require.NoError(t, m.Up(ctx))

		names := indexNames(t, func(sqlText string) ([]string, error) {
			return scanPgxStrings(pool.Query(ctx, sqlText))
		}, `SELECT indexname FROM pg_indexes WHERE tablename = 'wrkflw_timers'`)
		assertIndexes(t, "postgres", names)
	})

	t.Run("mysql", func(t *testing.T) {
		db := dbtest.RunTestMySQL(t) // already migrated
		names := indexNames(t, func(sqlText string) ([]string, error) {
			return scanStrings(db.QueryContext(ctx, sqlText))
		}, `SELECT DISTINCT index_name FROM information_schema.statistics
		    WHERE table_schema = DATABASE() AND table_name = 'wrkflw_timers'`)
		assertIndexes(t, "mysql", names)
	})
}

// scanStrings drains a database/sql result set of single string columns.
func scanStrings(rows *sql.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// scanPgxStrings drains a pgx result set of single string columns.
func scanPgxStrings(rows pgx.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// explicitIndexQuery is the per-dialect introspection that lists ONLY the
// explicitly-named indexes on wrkflw_* tables — the ones a migration file
// declares with CREATE INDEX (or MySQL's inline INDEX clause).
//
// Every engine names its implicit indexes differently (PRIMARY vs *_pkey vs
// sqlite_autoindex_*), so including them reports divergence that is not
// divergence. Each query therefore filters out:
//   - PRIMARY KEY backing indexes,
//   - UNIQUE constraint backing indexes (wrkflw_outbox.dedup_key),
//   - MySQL foreign-key backing indexes.
const (
	postgresExplicitIndexQuery = `
		SELECT c.relname
		FROM   pg_index i
		JOIN   pg_class     c ON c.oid = i.indexrelid
		JOIN   pg_class     t ON t.oid = i.indrelid
		JOIN   pg_namespace n ON n.oid = t.relnamespace
		WHERE  n.nspname = 'public'
		  AND  t.relname LIKE 'wrkflw\_%'
		  AND  NOT i.indisprimary
		  AND  NOT i.indisunique`

	mysqlExplicitIndexQuery = `
		SELECT DISTINCT s.index_name
		FROM   information_schema.statistics s
		WHERE  s.table_schema = DATABASE()
		  AND  s.table_name LIKE 'wrkflw\_%'
		  AND  s.non_unique = 1
		  AND  s.index_name NOT IN (
		         SELECT tc.constraint_name
		         FROM   information_schema.table_constraints tc
		         WHERE  tc.table_schema = DATABASE()
		           AND  tc.constraint_type = 'FOREIGN KEY')`

	// sqlite_master.sql is NULL for indexes SQLite created implicitly and
	// non-NULL for indexes a CREATE INDEX statement declared — the exact
	// distinction this test needs, without name-pattern guessing.
	sqliteExplicitIndexQuery = `
		SELECT name
		FROM   sqlite_master
		WHERE  type = 'index'
		  AND  tbl_name LIKE 'wrkflw\_%' ESCAPE '\'
		  AND  sql IS NOT NULL`
)

// TestMigrationParity_IndexNamesConverge asserts that Postgres, MySQL and
// SQLite declare the SAME SET of explicitly-named indexes across all wrkflw_*
// tables. An index added to one dialect's 0001_init.sql and forgotten on the
// other two is otherwise invisible: the logical-schema guardrail above compares
// columns, not indexes, so a backend silently loses the seek the engine's hot
// path depends on (ADR-0159 is exactly that failure mode, caught late).
//
// NAMES ONLY. Index COLUMN LISTS diverge on purpose and must not be compared:
// Postgres expresses wrkflw_outbox_dead_idx and wrkflw_call_links_pending_idx
// as PARTIAL indexes (`... WHERE status = 'dead'`), while MySQL and SQLite have
// no partial-index support and fold the predicate column into the key instead
// (`(status, id)`). Both encode the same intent; comparing columns would fail
// on a deliberate, documented trade-off.
func TestMigrationParity_IndexNamesConverge(t *testing.T) {
	ctx := context.Background()

	sqliteDB, err := sql.Open("sqlite", "file:idxparity?mode=memory&cache=shared")
	require.NoError(t, err)
	sqliteDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqliteDB.Close() })
	sm, err := store.NewSQLiteMigrator(sqliteDB)
	require.NoError(t, err)
	require.NoError(t, sm.Up(ctx))
	sqliteIndexes := indexNames(t, func(sqlText string) ([]string, error) {
		return scanStrings(sqliteDB.QueryContext(ctx, sqlText))
	}, sqliteExplicitIndexQuery)

	pool := dbtest.RunTestDatabase(t)
	pm, err := store.NewPostgresMigrator(pool)
	require.NoError(t, err)
	require.NoError(t, pm.Up(ctx))
	postgresIndexes := indexNames(t, func(sqlText string) ([]string, error) {
		return scanPgxStrings(pool.Query(ctx, sqlText))
	}, postgresExplicitIndexQuery)

	mysqlDB := dbtest.RunTestMySQL(t) // already migrated
	mysqlIndexes := indexNames(t, func(sqlText string) ([]string, error) {
		return scanStrings(mysqlDB.QueryContext(ctx, sqlText))
	}, mysqlExplicitIndexQuery)

	// Non-empty is a precondition, not a nicety: a filter that over-excluded
	// would make three empty sets compare equal and the guardrail would pass
	// while asserting nothing.
	require.NotEmpty(t, postgresIndexes, "postgres: no explicitly-named wrkflw_* indexes found — the filter over-excludes")

	require.Equal(t, sortedKeys(postgresIndexes), sortedKeys(sqliteIndexes), "postgres vs sqlite explicit index names")
	require.Equal(t, sortedKeys(postgresIndexes), sortedKeys(mysqlIndexes), "postgres vs mysql explicit index names")
}

// sortedKeys renders a name set as a sorted slice so a require.Equal failure
// prints the two lists side by side instead of an unordered map diff.
func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
