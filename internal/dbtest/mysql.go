package dbtest

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	mysqlpersistence "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
)

const (
	mysqlRootPassword = "wrkflw_root" //nolint:gosec // G101: ephemeral testcontainers root password, not a production secret.
	mysqlDefaultDB    = "wrkflw_test"
	mysqlRootUser     = "root"
)

type sharedMySQLContainer struct {
	// rootDSN connects as root to mysqlDefaultDB (for CREATE DATABASE)
	rootDSN func(dbName string) string
}

var (
	mysqlSharedOnce sync.Once
	mysqlShared     *sharedMySQLContainer
	mysqlSharedErr  error
	mysqlDBCounter  atomic.Int64
	mysqlCreateMu   sync.Mutex
)

// initMySQLContainer initialises the shared MySQL 8.0 testcontainer (once per
// test binary) and populates mysqlShared / mysqlSharedErr. It is called by both
// RunTestMySQL and RunTestMySQLDSN so neither duplicates the startup logic.
func initMySQLContainer() {
	mysqlSharedOnce.Do(func() {
		ctx := context.Background()

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

		mysqlShared = &sharedMySQLContainer{
			rootDSN: func(dbName string) string {
				// parseTime=true&loc=UTC are required for correct DATETIME scanning.
				// multiStatements=true is required for goose multi-statement migration files.
				return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&loc=UTC&multiStatements=true",
					mysqlRootUser, mysqlRootPassword, host, port.Port(), dbName)
			},
		}
		// Container intentionally not terminated here; Ryuk reaps it when the
		// test binary exits.
	})
}

// allocTestMySQLDB creates a fresh per-test database in the shared container,
// registers a DROP DATABASE cleanup, and returns (dbName, dsn). Callers must
// invoke initMySQLContainer (and check mysqlSharedErr) before calling this.
func allocTestMySQLDB(t *testing.T) (dbName, dsn string) {
	t.Helper()

	n := mysqlDBCounter.Add(1)
	dbName = fmt.Sprintf("wrkflw_test_%d", n)
	ctx := context.Background()

	// Create per-test database using a root connection to the default DB.
	adminDB, err := sql.Open("mysql", mysqlShared.rootDSN(mysqlDefaultDB))
	require.NoError(t, err, "open admin mysql db")
	defer func() { _ = adminDB.Close() }()

	mysqlCreateMu.Lock()
	_, err = adminDB.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS `"+dbName+"`")
	mysqlCreateMu.Unlock()
	require.NoError(t, err, "create per-test mysql database")

	t.Cleanup(func() {
		dropDB, err2 := sql.Open("mysql", mysqlShared.rootDSN(mysqlDefaultDB))
		if err2 == nil {
			_, _ = dropDB.ExecContext(context.Background(), "DROP DATABASE IF EXISTS `"+dbName+"`")
			_ = dropDB.Close()
		}
	})

	return dbName, mysqlShared.rootDSN(dbName)
}

// RunTestMySQL starts (once per test binary) a MySQL 8.0 testcontainer, creates
// a fresh per-test database, opens a *sql.DB with parseTime=true&loc=UTC, and
// registers cleanup via t.Cleanup. The connection is safe to use immediately —
// Ping is verified before returning.
//
// Requires a running Docker daemon.
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
	require.NoError(t, mysqlpersistence.Migrate(ctx, db), "auto-migrate per-test mysql db")
	return db
}

// RunTestMySQLDSN starts the shared MySQL testcontainer (same singleton as
// RunTestMySQL), creates a fresh per-test database, and returns the raw DSN
// string — identical to what RunTestMySQL passes to sql.Open internally.
// Use this when a test needs to manipulate the DSN (e.g. to inject a wrong
// loc= for negative-probe tests) rather than accept the pre-opened *sql.DB.
//
// The per-test database is created and registered for cleanup exactly as in
// RunTestMySQL. Migrations are NOT applied; call persistence.MigrateMySQL if
// the schema is needed.
//
// Requires a running Docker daemon.
func RunTestMySQLDSN(t *testing.T) string {
	t.Helper()

	initMySQLContainer()
	require.NoError(t, mysqlSharedErr)

	_, dsn := allocTestMySQLDB(t)
	return dsn
}
