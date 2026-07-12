package persistence_test

import (
	"database/sql"
	"testing"
	"time"

	gomysql "github.com/go-sql-driver/mysql"

	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/persistence"
)

// forceLocalLoc rewrites dsn so that loc=time.Local and parseTime=true, which
// causes the MySQL driver to interpret DATETIME columns in the local timezone
// instead of UTC — triggering the ProbeUTC fail-fast check.
func forceLocalLoc(dsn string) string {
	cfg, err := gomysql.ParseDSN(dsn)
	if err != nil {
		panic("forceLocalLoc: " + err.Error())
	}
	cfg.Loc = time.Local
	cfg.ParseTime = true
	return cfg.FormatDSN()
}

// TestOpenMySQLRejectsNonUTC opens a MySQL handle whose DSN forces loc=Local
// and asserts that OpenMySQL returns an error (fail-fast ProbeUTC rejection).
// This test will FAIL (RED) until ProbeUTC is wired into OpenMySQL.
func TestOpenMySQLRejectsNonUTC(t *testing.T) {
	if time.Local == time.UTC {
		t.Skip("host TZ is UTC; loc=Local is indistinguishable from loc=UTC — skipping negative probe test")
	}

	dsn := dbtest.RunTestMySQLDSN(t)
	bad, err := sql.Open("mysql", forceLocalLoc(dsn))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer bad.Close() //nolint:errcheck

	if _, err := persistence.OpenMySQL(t.Context(), bad); err == nil {
		t.Fatal("want fail-fast error for non-UTC MySQL connection, got nil")
	}
}
