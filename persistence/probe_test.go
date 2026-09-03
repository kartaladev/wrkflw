package persistence_test

import (
	"database/sql"
	"testing"
	"time"
	_ "time/tzdata" // embed the IANA database so probeZone resolves on a host without tzdata

	gomysql "github.com/go-sql-driver/mysql"

	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/persistence"
)

// probeZone is a fixed non-UTC IANA zone, deliberately NOT time.Local.
//
// time.Local is unusable here: on a UTC host — which every GitHub Actions
// runner is — its offset is zero, so loc=Local is indistinguishable from
// loc=UTC, ProbeUTC correctly finds no instant drift, and the negative
// assertion below has nothing to catch. The guard this replaced tried to skip
// that case with `time.Local == time.UTC`, but that compares *time.Location
// POINTERS and is false even under TZ=UTC, so the skip never fired and CI
// failed while the author's non-UTC host stayed green.
//
// A constant offset makes the test assert the same thing in every timezone
// rather than only where the developer happens to sit. The driver re-resolves
// this name through time.LoadLocation when it parses the DSN, so it must be a
// real IANA zone — hence the time/tzdata import above.
const probeZone = "Asia/Jakarta"

// forceNonUTCLoc rewrites dsn so that loc=probeZone and parseTime=true, which
// makes the MySQL driver interpret DATETIME columns in a fixed +07:00 zone
// instead of UTC — shifting the scanned instant and tripping the ProbeUTC
// fail-fast check.
func forceNonUTCLoc(t *testing.T, dsn string) string {
	t.Helper()

	loc, err := time.LoadLocation(probeZone)
	if err != nil {
		t.Fatalf("load %s: %v", probeZone, err)
	}
	cfg, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	cfg.Loc = loc
	cfg.ParseTime = true
	return cfg.FormatDSN()
}

// TestOpenMySQLRejectsNonUTC opens a MySQL handle whose DSN forces a non-UTC
// loc and asserts that OpenMySQL returns an error — the fail-fast ProbeUTC
// rejection wired in at persistence/mysql.go.
func TestOpenMySQLRejectsNonUTC(t *testing.T) {
	dsn := dbtest.RunTestMySQLDSN(t)
	bad, err := sql.Open("mysql", forceNonUTCLoc(t, dsn))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer bad.Close() //nolint:errcheck

	if _, err := persistence.OpenMySQL(t.Context(), bad); err == nil {
		t.Fatal("want fail-fast error for non-UTC MySQL connection, got nil")
	}
}
