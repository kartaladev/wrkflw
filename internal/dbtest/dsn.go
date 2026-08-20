package dbtest

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// Environment variables that point the helpers at an ALREADY-RUNNING database
// server instead of booting a testcontainer.
//
// Why they exist (blocker 7): the shared-container singletons in postgres.go and
// mysql.go are package-level sync.Once values, and Go builds one test binary per
// package — so "once per binary" means one container boot per package. Today that
// it is 10 Postgres boots and 7 MySQL boots for a full `go test ./...`, paid on
// every run (re-counted 2026-08-20 as the packages whose own *_test.go files call
// RunTestDatabase / RunTestMySQL*; an earlier count of 12 Postgres had included
// this package's own definition and a mention in a comment). Pointing every binary
// at one long-lived server collapses that to one each.
//
// The container path remains the DEFAULT and is unchanged: a developer with
// neither variable set sees exactly today's behaviour. Set them (see
// scripts/testdb.sh) only when you want the shared server.
//
// Isolation is preserved either way — each test still gets a freshly CREATEd
// database of its own, dropped in t.Cleanup. The only difference is who owns the
// server process — and that all those databases now share ONE namespace, which is
// why their names carry a per-process tag (see dbname.go).
const (
	// EnvPostgresDSN, when non-empty, must be a URL-form PostgreSQL DSN
	// (postgres://user:pass@host:port/db?params) for a running server on which
	// the helper may CREATE and DROP databases.
	EnvPostgresDSN = "WRKFLW_TEST_POSTGRES_DSN"

	// EnvMySQLDSN, when non-empty, must be a go-sql-driver DSN
	// (user:pass@tcp(host:port)/db?params) for a running server on which the
	// helper may CREATE and DROP databases.
	EnvMySQLDSN = "WRKFLW_TEST_MYSQL_DSN"
)

// PostgresDSNForDB returns base with its database name replaced by dbName,
// preserving scheme, userinfo, host, port and every query parameter.
//
// Only the URL form is accepted. pgx also understands libpq keyword/value strings
// ("host=… dbname=…"), but accepting both would mean two rewrite paths and a
// silent wrong-database connection if the wrong one were picked; the error names
// the environment variable so the operator can see which value to fix.
func PostgresDSNForDB(base, dbName string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("workflow-dbtest: %s is not a valid URL DSN: %w", EnvPostgresDSN, err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return "", fmt.Errorf(
			"workflow-dbtest: %s must be a URL DSN of the form postgres://user:pass@host:port/db?params, got scheme %q",
			EnvPostgresDSN, u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("workflow-dbtest: %s has no host: %q", EnvPostgresDSN, base)
	}
	u.Path = "/" + dbName
	return u.String(), nil
}

// MySQLDSNForDB returns base with its database name replaced by dbName.
//
// parseTime, loc=UTC and multiStatements are FORCED rather than inherited: the
// container path hardcodes all three (parseTime/loc for correct DATETIME
// scanning, multiStatements for goose's multi-statement migration files), and a
// shared server whose DSN omitted them would fail migrations in ways that look
// like product bugs. Everything else in the operator's DSN is preserved.
func MySQLDSNForDB(base, dbName string) (string, error) {
	cfg, err := mysqldriver.ParseDSN(base)
	if err != nil {
		return "", fmt.Errorf("workflow-dbtest: %s is not a valid MySQL DSN: %w", EnvMySQLDSN, err)
	}
	cfg.DBName = dbName
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.MultiStatements = true
	return cfg.FormatDSN(), nil
}

// envDSN reads name and trims surrounding whitespace, so a variable exported as
// an empty or blank string is treated as unset rather than as a broken DSN.
func envDSN(name string) string { return strings.TrimSpace(os.Getenv(name)) }
