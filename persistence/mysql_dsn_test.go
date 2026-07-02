package persistence_test

import (
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/zakyalvan/krtlwrkflw/persistence"
)

// TestMySQLDSNForcesUTC verifies that MySQLDSN always produces a DSN with:
//   - parseTime=true   (present as a query parameter)
//   - time_zone='+00:00' (URL-encoded in the params, applied as SET per connection)
//   - cfg.Loc == time.UTC (verified by re-parsing the output DSN)
//
// Note: go-sql-driver's FormatDSN intentionally omits "loc=UTC" from the
// serialised string because UTC is the driver default; the guarantee is verified
// by re-parsing the returned DSN and checking cfg.Loc directly.
func TestMySQLDSNForcesUTC(t *testing.T) {
	t.Run("sets parseTime and time_zone params", func(t *testing.T) {
		got, err := persistence.MySQLDSN("user:pass@tcp(127.0.0.1:3306)/wrkflw")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, want := range []string{"parseTime=true", "time_zone=%27%2B00%3A00%27"} {
			if !strings.Contains(got, want) {
				t.Errorf("dsn %q missing %q", got, want)
			}
		}
		// Re-parse the output DSN and confirm loc is UTC.
		cfg, err := mysql.ParseDSN(got)
		if err != nil {
			t.Fatalf("ParseDSN of output: %v", err)
		}
		if cfg.Loc != time.UTC {
			t.Errorf("cfg.Loc = %v; want time.UTC", cfg.Loc)
		}
	})

	t.Run("idempotent when called twice", func(t *testing.T) {
		first, err := persistence.MySQLDSN("user:pass@tcp(127.0.0.1:3306)/wrkflw")
		if err != nil {
			t.Fatalf("first call: %v", err)
		}
		second, err := persistence.MySQLDSN(first)
		if err != nil {
			t.Fatalf("second call: %v", err)
		}
		if first != second {
			t.Errorf("not idempotent:\n first:  %q\n second: %q", first, second)
		}
	})

	t.Run("error on invalid DSN", func(t *testing.T) {
		_, err := persistence.MySQLDSN("not a valid dsn ://")
		if err == nil {
			t.Error("expected error for invalid DSN, got nil")
		}
		if !strings.Contains(err.Error(), "workflow-persistence-mysql:") {
			t.Errorf("error %q missing prefix %q", err.Error(), "workflow-persistence-mysql:")
		}
	})
}
