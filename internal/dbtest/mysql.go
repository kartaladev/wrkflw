package dbtest

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
)

const (
	mysqlRootPassword = "wrkflw_root" //nolint:gosec // G101: ephemeral testcontainers root password, not a production secret.
	mysqlDefaultDB    = "wrkflw_test"
	mysqlRootUser     = "root"
)

type sharedMySQLContainer struct {
	// rootDSN builds a DSN for a named database on the shared server.
	rootDSN func(dbName string) string
	// adminDSN is the DSN used for CREATE/DROP DATABASE. It must address a
	// database that already exists: on the container branch that is
	// mysqlDefaultDB, which the container is created with; on the
	// EnvMySQLDSN branch it is the operator's DSN as given, since
	// mysqlDefaultDB may not exist on their server.
	adminDSN string
}

var (
	mysqlSharedOnce sync.Once
	mysqlShared     *sharedMySQLContainer
	mysqlSharedErr  error
	mysqlCreateMu   sync.Mutex
)

// initMySQLContainer resolves the shared MySQL server (once per test binary) and
// populates mysqlShared / mysqlSharedErr. It is called by both RunTestMySQL and
// RunTestMySQLDSN so neither duplicates the startup logic.
//
// If WRKFLW_TEST_MYSQL_DSN is set it adopts that already-running server and boots
// nothing; otherwise it starts a MySQL 8.0 testcontainer, which remains the
// default. See [EnvMySQLDSN] and blocker 7 for why the env branch exists.
func initMySQLContainer() {
	mysqlSharedOnce.Do(func() {
		ctx := context.Background()

		if base := envDSN(EnvMySQLDSN); base != "" {
			mysqlShared, mysqlSharedErr = sharedMySQLFromDSN(base)
			return
		}

		// Use root with a known password. WithPassword sets both MYSQL_PASSWORD
		// and MYSQL_ROOT_PASSWORD (via WithDefaultCredentials), and since we pass
		// WithUsername("root"), the container resolves to root credentials.
		container, err := tcmysql.Run(ctx, "mysql:8.0",
			tcmysql.WithDatabase(mysqlDefaultDB),
			tcmysql.WithUsername(mysqlRootUser),
			tcmysql.WithPassword(mysqlRootPassword),
			testcontainers.WithEnv(map[string]string{
				"MYSQL_ROOT_PASSWORD": mysqlRootPassword,
			}),
		)
		if err != nil {
			mysqlSharedErr = fmt.Errorf("start shared mysql container: %w", err)
			return
		}

		host, err := container.Host(ctx)
		if err != nil {
			mysqlSharedErr = fmt.Errorf("shared mysql container host: %w", err)
			return
		}
		port, err := container.MappedPort(ctx, "3306/tcp")
		if err != nil {
			mysqlSharedErr = fmt.Errorf("shared mysql container port: %w", err)
			return
		}

		rootDSN := func(dbName string) string {
			// parseTime=true&loc=UTC are required for correct DATETIME scanning.
			// multiStatements=true is required for goose multi-statement migration files.
			return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&loc=UTC&multiStatements=true",
				mysqlRootUser, mysqlRootPassword, host, port.Port(), dbName)
		}
		mysqlShared = &sharedMySQLContainer{
			rootDSN:  rootDSN,
			adminDSN: rootDSN(mysqlDefaultDB),
		}
		// Container intentionally not terminated here; Ryuk reaps it when the
		// test binary exits.
	})
}

// sharedMySQLFromDSN adopts an already-running MySQL server named by
// WRKFLW_TEST_MYSQL_DSN. Per-test databases are addressed by rewriting only the
// database segment of that DSN; CREATE/DROP run against the DSN as given, which
// is the one database the operator has guaranteed exists.
func sharedMySQLFromDSN(base string) (*sharedMySQLContainer, error) {
	// Validate once, here, so a malformed value fails with a message naming the
	// environment variable rather than as an opaque driver error in each test.
	if _, err := MySQLDSNForDB(base, mysqlDefaultDB); err != nil {
		return nil, err
	}
	return &sharedMySQLContainer{
		rootDSN: func(dbName string) string {
			// Cannot fail: base parsed above, and dbName only sets cfg.DBName.
			dsn, _ := MySQLDSNForDB(base, dbName)
			return dsn
		},
		adminDSN: base,
	}, nil
}

// allocTestMySQLDB creates a fresh per-test database in the shared container,
// registers a DROP DATABASE cleanup, and returns (dbName, dsn). Callers must
// invoke initMySQLContainer (and check mysqlSharedErr) before calling this.
func allocTestMySQLDB(t *testing.T) (dbName, dsn string) {
	t.Helper()

	dbName = nextTestDBName()
	ctx := context.Background()

	// Create per-test database using a root connection to the default DB.
	adminDB, err := sql.Open("mysql", mysqlShared.adminDSN)
	require.NoError(t, err, "open admin mysql db")
	defer func() { _ = adminDB.Close() }()

	mysqlCreateMu.Lock()
	// Plain CREATE DATABASE, not IF NOT EXISTS: on a shared server the latter
	// turns a name collision into two test binaries silently running against ONE
	// database — migrations applied twice, rows mixed, and whichever finishes
	// first dropping it from under the other. Names are process-unique, so this
	// can no longer happen; if it somehow does, it must fail loudly here.
	_, err = adminDB.ExecContext(ctx, "CREATE DATABASE `"+dbName+"`")
	mysqlCreateMu.Unlock()
	require.NoError(t, err, "create per-test mysql database")

	t.Cleanup(func() {
		// Never drop a database this process did not create: on a shared server
		// the other databases belong to OTHER test binaries running right now.
		if err2 := ownedTestDBName(dbName); err2 != nil {
			t.Errorf("per-test database cleanup: %v", err2)
			return
		}
		dropDB, err2 := sql.Open("mysql", mysqlShared.adminDSN)
		if err2 == nil {
			_, _ = dropDB.ExecContext(context.Background(), "DROP DATABASE IF EXISTS `"+dbName+"`")
			_ = dropDB.Close()
		}
	})

	return dbName, mysqlShared.rootDSN(dbName)
}

// RunTestMySQL resolves the shared MySQL 8.0 server (once per test binary),
// creates a fresh per-test database, opens a *sql.DB with parseTime=true&loc=UTC,
// and registers cleanup via t.Cleanup. The connection is safe to use immediately —
// Ping is verified before returning.
//
// Requires a running Docker daemon, UNLESS [EnvMySQLDSN] points at an
// already-running server — see scripts/testdb.sh.
func RunTestMySQL(t *testing.T) *sql.DB {
	t.Helper()

	initMySQLContainer()
	require.NoError(t, mysqlSharedErr)

	_, dsn := allocTestMySQLDB(t)
	ctx := context.Background()

	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err, "open per-test mysql db")
	db.SetMaxOpenConns(8)
	db.SetConnMaxLifetime(time.Minute)

	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.PingContext(ctx), "ping per-test mysql db")
	require.NoError(t, store.MigrateMySQL(ctx, db), "auto-migrate per-test mysql db")
	return db
}

// RunTestMySQLDSN resolves the shared MySQL server (same singleton as
// RunTestMySQL), creates a fresh per-test database, and returns the raw DSN
// string — identical to what RunTestMySQL passes to sql.Open internally.
// Use this when a test needs to manipulate the DSN (e.g. to inject a wrong
// loc= for negative-probe tests) rather than accept the pre-opened *sql.DB.
//
// The per-test database is created and registered for cleanup exactly as in
// RunTestMySQL. Migrations are NOT applied; call persistence.MigrateMySQL if
// the schema is needed.
//
// Requires a running Docker daemon, UNLESS [EnvMySQLDSN] points at an
// already-running server — see scripts/testdb.sh.
func RunTestMySQLDSN(t *testing.T) string {
	t.Helper()

	initMySQLContainer()
	require.NoError(t, mysqlSharedErr)

	_, dsn := allocTestMySQLDB(t)
	return dsn
}
