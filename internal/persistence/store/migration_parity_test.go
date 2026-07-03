package store_test

import (
	"context"
	"database/sql"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
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
