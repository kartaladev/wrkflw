package store

import (
	"database/sql"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// rawSQLite opens a fresh in-memory SQLite DB with single-writer serialisation.
// No migrations are applied — the Migrator drives them.
func rawSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestNewSQLiteMigrator_RejectsNilDB(t *testing.T) {
	t.Parallel()
	_, err := NewSQLiteMigrator(nil)
	require.ErrorIs(t, err, ErrNilDependency)
}

func TestNewSQLiteMigrator_RejectsTypedNilDB(t *testing.T) {
	t.Parallel()
	var db *sql.DB // typed nil
	_, err := NewSQLiteMigrator(db)
	require.ErrorIs(t, err, ErrNilDependency)
}

func TestMigrator_SQLiteLifecycle(t *testing.T) {
	t.Parallel()
	m, err := NewSQLiteMigrator(rawSQLite(t))
	require.NoError(t, err)
	ctx := t.Context()

	// Empty DB: pending work exists, version 0.
	pending, err := m.HasPending(ctx)
	require.NoError(t, err)
	assert.True(t, pending, "fresh DB must have pending migrations")

	// Up applies everything; SQLite head is 0003 (consolidated init + human_task
	// + timers_trigger).
	require.NoError(t, m.Up(ctx))
	v, err := m.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), v, "SQLite migration head is version 3")

	pending, err = m.HasPending(ctx)
	require.NoError(t, err)
	assert.False(t, pending, "no pending migrations after Up")

	// Status lists the applied source.
	rows, err := m.Status(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	for _, r := range rows {
		assert.True(t, r.Applied, "every source applied after Up: %s", r.Source)
	}

	// DownTo(0) rolls everything back — the wrkflw tables are gone.
	require.NoError(t, m.DownTo(ctx, 0))
	var n int
	err = m.provDB(t).QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='wrkflw_instances'`,
	).Scan(&n)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "wrkflw_instances dropped after DownTo(0)")
}

func TestMigrator_SQLiteUpTo(t *testing.T) {
	t.Parallel()
	m, err := NewSQLiteMigrator(rawSQLite(t))
	require.NoError(t, err)
	// SQLite head is 1, so UpTo(1) == Up; assert it lands on 1 and is a no-op re-run.
	require.NoError(t, m.UpTo(t.Context(), 1))
	v, err := m.Version(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(1), v)
	require.NoError(t, m.UpTo(t.Context(), 1), "re-running UpTo is idempotent")
}

// provDB returns the *sql.DB backing a sqlite/mysql Migrator for white-box
// assertions. Postgres (pool-backed) is not used in this test file.
func (m *Migrator) provDB(t *testing.T) *sql.DB {
	t.Helper()
	db, ok := m.conn.(*sql.DB)
	require.True(t, ok, "provDB only valid for *sql.DB-backed migrators")
	return db
}

// TestMigrator_SQLiteUpByOneAndDown exercises UpByOne and Down against an
// in-memory SQLite migrator (SQLite head is version 3: consolidated init +
// human_task + timers_trigger):
//   - UpByOne applies one pending migration at a time until head.
//   - Down rolls each migration back until the wrkflw_instances table is gone.
func TestMigrator_SQLiteUpByOneAndDown(t *testing.T) {
	t.Parallel()
	m, err := NewSQLiteMigrator(rawSQLite(t))
	require.NoError(t, err)
	ctx := t.Context()

	// First UpByOne applies migration 1; migrations 2 and 3 are still pending.
	require.NoError(t, m.UpByOne(ctx))
	v, err := m.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), v, "first UpByOne must land on version 1")

	pending, err := m.HasPending(ctx)
	require.NoError(t, err)
	assert.True(t, pending, "still pending after one of three migrations")

	// Second UpByOne applies migration 2; migration 3 is still pending.
	require.NoError(t, m.UpByOne(ctx))
	v, err = m.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), v, "second UpByOne must land on version 2")

	// Third UpByOne reaches head.
	require.NoError(t, m.UpByOne(ctx))
	v, err = m.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), v, "third UpByOne must reach head version 3")

	pending, err = m.HasPending(ctx)
	require.NoError(t, err)
	assert.False(t, pending, "no pending migrations after UpByOne reaches head")

	// Down rolls back one migration at a time back to empty.
	require.NoError(t, m.Down(ctx))
	v, err = m.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), v, "Down rolls back to version 2")

	require.NoError(t, m.Down(ctx))
	v, err = m.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), v, "Down rolls back to version 1")

	require.NoError(t, m.Down(ctx))
	v, err = m.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), v, "version must be 0 after third Down")

	var n int
	err = m.provDB(t).QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='wrkflw_instances'`,
	).Scan(&n)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "wrkflw_instances must be dropped after Down")
}

// TestNewPostgresMigrator_RejectsNilPool verifies that NewPostgresMigrator
// rejects both an untyped nil and a typed-nil *pgxpool.Pool without requiring
// any live database connection (the nil guard fires before DB access).
func TestNewPostgresMigrator_RejectsNilPool(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		pool   *pgxpool.Pool
		assert func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name: "untyped nil passed as typed param",
			pool: nil,
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, ErrNilDependency)
			},
		},
		{
			name: "typed-nil *pgxpool.Pool",
			pool: (*pgxpool.Pool)(nil),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, ErrNilDependency)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewPostgresMigrator(tc.pool)
			tc.assert(t, err)
		})
	}
}

// TestNewMySQLMigrator_RejectsNilDB verifies that NewMySQLMigrator rejects
// both an untyped nil and a typed-nil *sql.DB without requiring any live
// database connection (the nil guard fires before DB access).
func TestNewMySQLMigrator_RejectsNilDB(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		db     *sql.DB
		assert func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name: "untyped nil passed as typed param",
			db:   nil,
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, ErrNilDependency)
			},
		},
		{
			name: "typed-nil *sql.DB",
			db:   (*sql.DB)(nil),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, ErrNilDependency)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewMySQLMigrator(tc.db)
			tc.assert(t, err)
		})
	}
}
