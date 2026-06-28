package database

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
)

const (
	mysqlRootPassword = "wrkflw_root"
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

// RunTestMySQL starts (once per test binary) a MySQL 8.0 testcontainer, creates
// a fresh per-test database, opens a *sql.DB with parseTime=true&loc=UTC, and
// registers cleanup via t.Cleanup. The connection is safe to use immediately —
// Ping is verified before returning.
//
// Requires a running Docker daemon.
func RunTestMySQL(t *testing.T) *sql.DB {
	t.Helper()

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
				return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&loc=UTC&multiStatements=true",
					mysqlRootUser, mysqlRootPassword, host, port.Port(), dbName)
			},
		}
		// Container intentionally not terminated here; Ryuk reaps it when the
		// test binary exits.
	})
	require.NoError(t, mysqlSharedErr)

	n := mysqlDBCounter.Add(1)
	dbName := fmt.Sprintf("wrkflw_test_%d", n)
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

	db, err := sql.Open("mysql", mysqlShared.rootDSN(dbName))
	require.NoError(t, err, "open per-test mysql db")
	db.SetMaxOpenConns(8)
	db.SetConnMaxLifetime(time.Minute)

	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.PingContext(ctx), "ping per-test mysql db")
	return db
}
