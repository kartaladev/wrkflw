// Package database provides the shared testcontainers helper for PostgreSQL tests.
//
// [RunTestDatabase] is the single entry point for all tests that need a real
// PostgreSQL instance: it spins up a postgres:17-alpine container, waits for it
// to be ready, opens a [pgxpool.Pool], and registers cleanup hooks via t.Cleanup.
// The database is never named "postgres" because the snapshot Restore mechanism
// drops the connected database by name.
package database

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

type testConfig struct {
	dbName   string
	user     string
	password string
}

// TestOption customises the test database container.
type TestOption func(*testConfig)

// WithDBName overrides the database name. It must never be "postgres" because
// the Snapshot/Restore mechanism drops the connected database by name.
func WithDBName(name string) TestOption { return func(c *testConfig) { c.dbName = name } }

// WithUser overrides the PostgreSQL username.
func WithUser(user string) TestOption { return func(c *testConfig) { c.user = user } }

// WithPassword overrides the PostgreSQL password.
func WithPassword(password string) TestOption { return func(c *testConfig) { c.password = password } }

// RunTestDatabase starts a PostgreSQL 17 container and returns a connected
// [pgxpool.Pool]. Both the container and the pool are torn down via t.Cleanup.
//
// The database is always named "wrkflw_test" by default — never "postgres",
// because the Snapshot/Restore mechanism drops the connected database.
//
// Requires a running Docker daemon.
func RunTestDatabase(t *testing.T, opts ...TestOption) *pgxpool.Pool {
	t.Helper()

	cfg := testConfig{
		dbName:   "wrkflw_test",
		user:     "wrkflw",
		password: "wrkflw",
	}
	for _, o := range opts {
		o(&cfg)
	}

	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase(cfg.dbName),
		tcpostgres.WithUsername(cfg.user),
		tcpostgres.WithPassword(cfg.password),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err, "start postgres container")
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "get connection string")

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err, "open pgxpool")
	t.Cleanup(pool.Close)

	require.NoError(t, pool.Ping(ctx), "ping postgres")
	return pool
}
