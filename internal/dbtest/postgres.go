// Package dbtest provides the shared helpers for database tests against real
// PostgreSQL and MySQL instances (via testcontainers) and an in-process SQLite
// database (no Docker required).
//
// [RunTestDatabase] is the entry point for tests needing a real PostgreSQL
// instance (returns a connected [pgxpool.Pool]); [RunTestMySQL] and
// [RunTestMySQLDSN] (in mysql.go) are the MySQL equivalents; [RunTestSQLite]
// (in sqlite.go) provides a file-backed SQLite database with all migrations
// applied — no Docker daemon is required. Each returns a database isolated to
// the calling test.
//
// These helpers live in their own package (not in internal/database) so that
// the internal/database and internal/database/transaction toolkit packages stay
// free of any wrkflw imports (the extraction constraint — see ADR-0079).
//
// # Reusing an already-running server
//
// Both the PostgreSQL and MySQL helpers boot their container from a package-level
// sync.Once, and Go builds one test binary per package — so "once per binary"
// means one boot per PACKAGE. A full `go test ./...` therefore pays 10 PostgreSQL
// and 7 MySQL container boots — the packages whose own *_test.go files call
// RunTestDatabase / RunTestMySQL*, re-counted 2026-08-20.
//
// Set [EnvPostgresDSN] and/or [EnvMySQLDSN] to point every binary at one
// long-lived server instead. `scripts/testdb.sh up` starts a suitable pair and
// prints the exports; `scripts/testdb.sh down` removes them.
//
// The testcontainers path stays the DEFAULT: with neither variable set, behaviour
// is exactly as it was. Per-test isolation is identical on both paths — each test
// gets its own freshly-created database, dropped in t.Cleanup — so the only thing
// that changes is who owns the server process.
//
// What that change DOES cost is naming. With per-package containers, two binaries
// could both call their database "wrkflw_test_1" and never meet; one shared server
// puts them in the same namespace, and `go test ./...` runs up to GOMAXPROCS
// binaries at once. Per-test database names therefore carry a per-PROCESS tag as
// well as a counter, and every DROP goes through an ownership check — see
// dbname.go.
//
// # The PostgreSQL helper below
//
// To avoid the resource storm of booting one container per test — which, under
// high `-p 1` parallelism (GOMAXPROCS tests each starting a postgres:17-alpine
// container at once), starved the Docker host and made container-startup-sensitive
// tests flake — the helper reuses ONE container per test binary and hands each
// test its own freshly-created database (CREATE DATABASE is cheap; a container
// start is not). The container lives for the lifetime of the test binary and is
// reaped by testcontainers' Ryuk after the process exits; each test's database
// and pool are torn down via t.Cleanup.
//
// The database is never named "postgres" because the Snapshot/Restore mechanism
// drops the connected database by name.
package dbtest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	defaultDBName   = "wrkflw_test"
	defaultUser     = "wrkflw"
	defaultPassword = "wrkflw"

	// perTestMaxConns caps each per-test pool so that many parallel tests sharing
	// the single container stay well under the server's max_connections.
	perTestMaxConns = 8
	// serverMaxConns is set on the shared container to give the per-test pools
	// ample headroom regardless of the host's core count / test parallelism.
	serverMaxConns = "300"
)

type testConfig struct {
	dbName   string
	user     string
	password string
}

// TestOption customises the test database container.
type TestOption func(*testConfig)

// WithDBName overrides the database name. It must never be "postgres" because
// the Snapshot/Restore mechanism drops the connected database by name. Passing
// any custom option falls back to a dedicated container for that test.
func WithDBName(name string) TestOption { return func(c *testConfig) { c.dbName = name } }

// WithUser overrides the PostgreSQL username.
func WithUser(user string) TestOption { return func(c *testConfig) { c.user = user } }

// WithPassword overrides the PostgreSQL password.
func WithPassword(password string) TestOption { return func(c *testConfig) { c.password = password } }

// sharedContainer holds the single PostgreSQL container reused by all
// default RunTestDatabase calls in a test binary.
type sharedContainer struct {
	adminPool *pgxpool.Pool          // connected to the default db; issues CREATE/DROP DATABASE
	dsnFor    func(db string) string // builds a DSN for a named database on the container
}

var (
	sharedOnce sync.Once
	shared     *sharedContainer
	sharedErr  error
	// createMu serialises CREATE DATABASE so concurrent calls don't race on the
	// template database ("source database template1 is being accessed by other users").
	createMu sync.Mutex
)

// RunTestDatabase returns a connected [pgxpool.Pool] to a database isolated to
// the calling test. With default options it shares one server across the test
// binary (one fresh database per test); custom user/password/db name fall back to
// a dedicated container. The database and pool are torn down via t.Cleanup.
//
// Requires a running Docker daemon, UNLESS [EnvPostgresDSN] points at an
// already-running server — see scripts/testdb.sh. The custom-options path always
// starts its own container regardless of that variable, because it needs a server
// with different credentials.
func RunTestDatabase(t *testing.T, opts ...TestOption) *pgxpool.Pool {
	t.Helper()

	cfg := testConfig{dbName: defaultDBName, user: defaultUser, password: defaultPassword}
	for _, o := range opts {
		o(&cfg)
	}

	// A custom user/password/db name needs its own server: fall back to a
	// dedicated container (preserves the original semantics; no current caller
	// exercises this path).
	if cfg.dbName != defaultDBName || cfg.user != defaultUser || cfg.password != defaultPassword {
		return runDedicated(t, cfg)
	}

	base := sharedBase(t)

	name := nextTestDBName()
	ctx := context.Background()

	createMu.Lock()
	_, err := base.adminPool.Exec(ctx, "CREATE DATABASE "+name)
	createMu.Unlock()
	require.NoError(t, err, "create per-test database")
	t.Cleanup(func() {
		// Never drop a database this process did not create: on a shared server
		// the other databases belong to OTHER test binaries running right now,
		// and WITH (FORCE) would sever their live connections.
		if err := ownedTestDBName(name); err != nil {
			t.Errorf("per-test database cleanup: %v", err)
			return
		}
		// FORCE terminates any connections (e.g. a relay LISTEN) lingering past
		// the pool close so the drop never blocks.
		_, _ = base.adminPool.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})

	pool, err := newPool(ctx, base.dsnFor(name), perTestMaxConns)
	require.NoError(t, err, "open pgxpool")
	t.Cleanup(pool.Close)
	require.NoError(t, pool.Ping(ctx), "ping postgres")
	return pool
}

// sharedBase resolves (once per test binary) the server that per-test databases
// are created on: an already-running one named by WRKFLW_TEST_POSTGRES_DSN if that
// is set, otherwise a testcontainer booted here.
//
// The container branch is the DEFAULT and is unchanged. The env branch exists
// because "once per binary" still means once per PACKAGE — Go builds one test
// binary per package — so a full `go test ./...` boots one container in each of
// the packages that call this (blocker 7). Per-test isolation is identical on both
// branches: each test still gets its own CREATEd database, dropped in t.Cleanup.
func sharedBase(t *testing.T) *sharedContainer {
	t.Helper()
	sharedOnce.Do(func() {
		ctx := context.Background()

		if base := envDSN(EnvPostgresDSN); base != "" {
			shared, sharedErr = sharedFromDSN(ctx, base)
			return
		}

		container, err := startContainer(ctx, defaultDBName, defaultUser, defaultPassword)
		if err != nil {
			sharedErr = fmt.Errorf("start shared postgres container: %w", err)
			return
		}
		host, err := container.Host(ctx)
		if err != nil {
			sharedErr = fmt.Errorf("shared container host: %w", err)
			return
		}
		port, err := container.MappedPort(ctx, "5432/tcp")
		if err != nil {
			sharedErr = fmt.Errorf("shared container port: %w", err)
			return
		}
		dsnFor := func(db string) string {
			return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
				defaultUser, defaultPassword, host, port.Port(), db)
		}
		adminPool, err := newPool(ctx, dsnFor(defaultDBName), 4)
		if err != nil {
			sharedErr = fmt.Errorf("shared admin pool: %w", err)
			return
		}
		shared = &sharedContainer{adminPool: adminPool, dsnFor: dsnFor}
		// The container is intentionally NOT terminated here: it must outlive the
		// first test so later tests can reuse it. Ryuk reaps it after the process exits.
	})
	require.NoError(t, sharedErr)
	return shared
}

// sharedFromDSN builds the shared base against an already-running server.
//
// The admin pool connects to base exactly as given — CREATE/DROP DATABASE must run
// against a database that already exists, and that is the one the operator named.
// Per-test databases are then addressed by rewriting only base's database segment.
func sharedFromDSN(ctx context.Context, base string) (*sharedContainer, error) {
	// Validate once, here, so a malformed value fails with a message naming the
	// environment variable rather than as an opaque connection error in each test.
	if _, err := PostgresDSNForDB(base, defaultDBName); err != nil {
		return nil, err
	}
	dsnFor := func(db string) string {
		// Cannot fail: base parsed above, and db only replaces the URL path.
		dsn, _ := PostgresDSNForDB(base, db)
		return dsn
	}
	adminPool, err := newPool(ctx, base, 4)
	if err != nil {
		return nil, fmt.Errorf("workflow-dbtest: admin pool for %s: %w", EnvPostgresDSN, err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		return nil, fmt.Errorf("workflow-dbtest: %s is set but unreachable: %w", EnvPostgresDSN, err)
	}
	return &sharedContainer{adminPool: adminPool, dsnFor: dsnFor}, nil
}

// runDedicated starts a container dedicated to a single test (the custom-options path).
func runDedicated(t *testing.T, cfg testConfig) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	container, err := startContainer(ctx, cfg.dbName, cfg.user, cfg.password)
	require.NoError(t, err, "start postgres container")
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "get connection string")

	pool, err := newPool(ctx, dsn, perTestMaxConns)
	require.NoError(t, err, "open pgxpool")
	t.Cleanup(pool.Close)
	require.NoError(t, pool.Ping(ctx), "ping postgres")
	return pool
}

func startContainer(ctx context.Context, dbName, user, password string) (*tcpostgres.PostgresContainer, error) {
	return tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase(dbName),
		tcpostgres.WithUsername(user),
		tcpostgres.WithPassword(password),
		// Raise max_connections so the shared container is not connection-starved
		// when many parallel tests each hold a capped pool.
		testcontainers.WithCmd("postgres", "-c", "max_connections="+serverMaxConns),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
}

func newPool(ctx context.Context, dsn string, maxConns int32) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	poolCfg.MaxConns = maxConns
	return pgxpool.NewWithConfig(ctx, poolCfg)
}
