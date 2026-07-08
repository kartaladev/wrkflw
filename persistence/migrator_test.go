package persistence_test

import (
	"database/sql"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/persistence"
)

func rawSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestNewSQLiteMigrator_NilRejected(t *testing.T) {
	t.Parallel()
	_, err := persistence.NewSQLiteMigrator(nil)
	require.ErrorIs(t, err, persistence.ErrNilDependency)
}

func TestNewPostgresMigrator_NilRejected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		pool *pgxpool.Pool
	}{
		{name: "untyped nil", pool: nil},
		{name: "typed nil pool", pool: (*pgxpool.Pool)(nil)},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := persistence.NewPostgresMigrator(tc.pool)
			assert.ErrorIs(t, err, persistence.ErrNilDependency)
		})
	}
}

func TestNewMySQLMigrator_NilRejected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		db   *sql.DB
	}{
		{name: "untyped nil", db: nil},
		{name: "typed nil db", db: (*sql.DB)(nil)},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := persistence.NewMySQLMigrator(tc.db)
			assert.ErrorIs(t, err, persistence.ErrNilDependency)
		})
	}
}

func TestMigrator_FacadeLifecycle_SQLite(t *testing.T) {
	t.Parallel()
	m, err := persistence.NewSQLiteMigrator(rawSQLite(t))
	require.NoError(t, err)
	ctx := t.Context()

	require.NoError(t, m.Up(ctx))
	v, err := m.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(4), v)

	st, err := m.Status(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, st)
	assert.True(t, st[0].Applied)
	assert.Equal(t, int64(1), st[0].Version)
	assert.NotEmpty(t, st[0].Source)

	pending, err := m.HasPending(ctx)
	require.NoError(t, err)
	assert.False(t, pending)

	// DownTo(0) rolls back all migrations.
	require.NoError(t, m.DownTo(ctx, 0))
	pending, err = m.HasPending(ctx)
	require.NoError(t, err)
	assert.True(t, pending, "rollback leaves all migrations pending")

	// UpByOne applies exactly one migration.
	require.NoError(t, m.UpByOne(ctx))
	v, err = m.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), v, "UpByOne advances to version 1")

	// Down rolls back the last applied migration.
	require.NoError(t, m.Down(ctx))
	v, err = m.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), v, "Down returns to version 0")

	// UpTo(1) applies up to and including version 1.
	require.NoError(t, m.UpTo(ctx, 1))
	v, err = m.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), v, "UpTo(1) advances to version 1")

	// UpTo(1) again is idempotent.
	require.NoError(t, m.UpTo(ctx, 1))
	v, err = m.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), v, "UpTo(1) is idempotent")
}

func TestMigrator_Postgres_Introspection(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t) // bare pool, no migrations
	m, err := persistence.NewPostgresMigrator(pool)
	require.NoError(t, err)
	ctx := t.Context()

	require.NoError(t, m.Up(ctx))
	v, err := m.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(12), v, "Postgres migration head is 12")

	pending, err := m.HasPending(ctx)
	require.NoError(t, err)
	assert.False(t, pending)

	st, err := m.Status(ctx)
	require.NoError(t, err)
	assert.Len(t, st, 12, "12 postgres migration sources")
	assert.Equal(t, int64(12), st[len(st)-1].Version)
}

func TestMigrator_MySQL_Introspection(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t) // already migrated
	m, err := persistence.NewMySQLMigrator(db)
	require.NoError(t, err)
	ctx := t.Context()

	v, err := m.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(5), v, "MySQL migration head is 5")

	pending, err := m.HasPending(ctx)
	require.NoError(t, err)
	assert.False(t, pending, "RunTestMySQL already migrated to head")

	st, err := m.Status(ctx)
	require.NoError(t, err)
	assert.Len(t, st, 5, "5 MySQL migration sources")
}
