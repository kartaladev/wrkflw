package persistence_test

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
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
	require.ErrorIs(t, err, store.ErrNilDependency)
}

func TestMigrator_FacadeLifecycle_SQLite(t *testing.T) {
	t.Parallel()
	m, err := persistence.NewSQLiteMigrator(rawSQLite(t))
	require.NoError(t, err)
	ctx := t.Context()

	require.NoError(t, m.Up(ctx))
	v, err := m.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), v)

	st, err := m.Status(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, st)
	assert.True(t, st[0].Applied)

	pending, err := m.HasPending(ctx)
	require.NoError(t, err)
	assert.False(t, pending)

	// DownTo(0) is a data-loss rollback: re-opening reports pending again.
	require.NoError(t, m.DownTo(ctx, 0))
	pending, err = m.HasPending(ctx)
	require.NoError(t, err)
	assert.True(t, pending, "rollback leaves all migrations pending")
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
	assert.Equal(t, int64(9), v, "Postgres migration head is 9")

	pending, err := m.HasPending(ctx)
	require.NoError(t, err)
	assert.False(t, pending)

	st, err := m.Status(ctx)
	require.NoError(t, err)
	assert.Len(t, st, 9, "9 postgres migration sources")
}

func TestMigrator_MySQL_Introspection(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t) // already migrated
	m, err := persistence.NewMySQLMigrator(db)
	require.NoError(t, err)
	ctx := t.Context()

	v, err := m.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), v, "MySQL migration head is 2")

	pending, err := m.HasPending(ctx)
	require.NoError(t, err)
	assert.False(t, pending, "RunTestMySQL already migrated to head")
}
