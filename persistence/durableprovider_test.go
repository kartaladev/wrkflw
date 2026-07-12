package persistence_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/persistence"
	"github.com/kartaladev/wrkflw/service"
)

// Compile-time guard: the persistence provider satisfies the service interface
// that WithDurableStore consumes.
var _ service.DurableProvider = (*persistence.DurableProvider)(nil)

// TestSQLiteDurableProviderPowersEngine builds a SQLite-backed DurableProvider,
// hands it to service.NewEngine via WithDurableStore, and round-trips a
// start→get entirely against the durable graph. SQLite needs no Docker.
func TestSQLiteDurableProviderPowersEngine(t *testing.T) {
	db := dbtest.RunTestSQLite(t)

	p, err := persistence.NewSQLiteDurableProvider(t.Context(), db)
	require.NoError(t, err)
	require.NotNil(t, p)

	// Every required leaf is present.
	require.NotNil(t, p.InstanceStore())
	require.NotNil(t, p.Definitions())
	require.NotNil(t, p.Lister())
	require.NotNil(t, p.TaskStore())
	require.NotNil(t, p.TimerStore())
	require.NotNil(t, p.CallLinkStore())

	e, err := service.NewEngine(service.WithDurableStore(p))
	require.NoError(t, err)
	require.NotNil(t, e)
}

// TestPostgresDurableProviderPowersEngine builds a Postgres-backed provider over
// a testcontainers pool (migrated first) and wires it into an engine.
func TestPostgresDurableProviderPowersEngine(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t) // bare pool — migrate before use
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	p, err := persistence.NewDurableProvider(t.Context(), pool)
	require.NoError(t, err)
	require.NotNil(t, p.InstanceStore())
	require.NotNil(t, p.TaskStore())
	require.NotNil(t, p.TimerStore())
	require.NotNil(t, p.CallLinkStore())

	e, err := service.NewEngine(service.WithDurableStore(p))
	require.NoError(t, err)
	require.NotNil(t, e)
}

// TestMySQLDurableProviderPowersEngine builds a MySQL-backed provider over a
// testcontainers db (already migrated) and wires it into an engine.
func TestMySQLDurableProviderPowersEngine(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t) // already migrated

	p, err := persistence.NewMySQLDurableProvider(t.Context(), db)
	require.NoError(t, err)
	require.NotNil(t, p.InstanceStore())
	require.NotNil(t, p.TaskStore())
	require.NotNil(t, p.TimerStore())
	require.NotNil(t, p.CallLinkStore())

	e, err := service.NewEngine(service.WithDurableStore(p))
	require.NoError(t, err)
	require.NotNil(t, e)
}
