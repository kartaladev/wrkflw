package dbtest_test

import (
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireParsedDSN re-parses a produced MySQL DSN with the driver's own parser so
// cases assert the EFFECTIVE connection settings rather than the DSN's spelling.
func requireParsedDSN(t *testing.T, dsn string) *mysqldriver.Config {
	t.Helper()
	cfg, err := mysqldriver.ParseDSN(dsn)
	require.NoError(t, err, "the produced DSN must round-trip through the driver's parser")
	return cfg
}

// These tests cover the pure DSN-rewriting half of the shared-server path
// (blocker 7): when WRKFLW_TEST_POSTGRES_DSN / WRKFLW_TEST_MYSQL_DSN point at an
// already-running server, every helper still hands each test its OWN database, so
// the base DSN must be rewritten per test exactly as the container path rewrites
// its own. No Docker is involved in this file.

func TestPostgresDSNForDB(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		base   string
		dbName string
		assert func(t *testing.T, dsn string, err error)
	}

	cases := []testCase{
		{
			name:   "swaps the database in a URL DSN",
			base:   "postgres://wrkflw:wrkflw@127.0.0.1:5432/wrkflw_test?sslmode=disable",
			dbName: "wrkflw_test_7",
			assert: func(t *testing.T, dsn string, err error) {
				require.NoError(t, err)
				assert.Equal(t, "postgres://wrkflw:wrkflw@127.0.0.1:5432/wrkflw_test_7?sslmode=disable", dsn)
			},
		},
		{
			name:   "preserves every query parameter",
			base:   "postgresql://u:p@db.internal:6432/base?sslmode=require&application_name=wrkflw&connect_timeout=5",
			dbName: "wrkflw_test_1",
			assert: func(t *testing.T, dsn string, err error) {
				require.NoError(t, err)
				assert.Contains(t, dsn, "/wrkflw_test_1?")
				assert.Contains(t, dsn, "sslmode=require")
				assert.Contains(t, dsn, "application_name=wrkflw")
				assert.Contains(t, dsn, "connect_timeout=5")
			},
		},
		{
			name:   "keeps userinfo and a non-default port",
			base:   "postgres://alice:s3cr%40t@10.0.0.9:15432/whatever",
			dbName: "wrkflw_test_2",
			assert: func(t *testing.T, dsn string, err error) {
				require.NoError(t, err)
				assert.Contains(t, dsn, "alice:s3cr%40t@10.0.0.9:15432")
				assert.True(t, strings.HasSuffix(dsn, "/wrkflw_test_2"), "got %q", dsn)
			},
		},
		{
			name:   "rejects a keyword/value DSN and names the env var",
			base:   "host=127.0.0.1 port=5432 user=wrkflw dbname=wrkflw_test sslmode=disable",
			dbName: "wrkflw_test_3",
			assert: func(t *testing.T, _ string, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "workflow-dbtest")
				assert.Contains(t, err.Error(), dbtest.EnvPostgresDSN)
			},
		},
		{
			name:   "rejects a non-postgres scheme",
			base:   "mysql://root@127.0.0.1:3306/wrkflw_test",
			dbName: "wrkflw_test_4",
			assert: func(t *testing.T, _ string, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), dbtest.EnvPostgresDSN)
			},
		},
		{
			name:   "rejects an unparsable DSN",
			base:   "postgres://user:pw@%%%/db",
			dbName: "wrkflw_test_5",
			assert: func(t *testing.T, _ string, err error) {
				require.Error(t, err)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dsn, err := dbtest.PostgresDSNForDB(tc.base, tc.dbName)
			tc.assert(t, dsn, err)
		})
	}
}

// TestEnvDSNSelectsTheContainerFallback pins the requirement that the
// testcontainers path stays the DEFAULT: a developer who exports nothing — or who
// exports an empty/blank value, which is what an unset variable expands to in most
// shell wrappers — must see exactly today's behaviour. Only a non-blank value
// diverts to the shared-server path.
//
// It fails if envDSN ever stops trimming (the blank cases would return " " and be
// treated as a DSN, sending every test to a shared-server path that cannot
// connect) or stops reading the variable at all (the last case would return "").
func TestEnvDSNSelectsTheContainerFallback(t *testing.T) {
	type testCase struct {
		name   string
		set    bool
		value  string
		assert func(t *testing.T, got string)
	}

	cases := []testCase{
		{
			name: "unset means container path",
			set:  false,
			assert: func(t *testing.T, got string) {
				assert.Empty(t, got)
			},
		},
		{
			name:  "empty means container path",
			set:   true,
			value: "",
			assert: func(t *testing.T, got string) {
				assert.Empty(t, got)
			},
		},
		{
			name:  "blank means container path",
			set:   true,
			value: "   \t ",
			assert: func(t *testing.T, got string) {
				assert.Empty(t, got, "a blank value must not be mistaken for a DSN")
			},
		},
		{
			name:  "a real DSN diverts to the shared server, trimmed",
			set:   true,
			value: "  postgres://u:p@127.0.0.1:5432/db?sslmode=disable\n",
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "postgres://u:p@127.0.0.1:5432/db?sslmode=disable", got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Not parallel: t.Setenv forbids it, and process env is global.
			const key = "WRKFLW_TEST_DBTEST_PROBE_DSN"
			if tc.set {
				t.Setenv(key, tc.value)
			}
			tc.assert(t, dbtest.EnvDSNForTest(key))
		})
	}
}

func TestMySQLDSNForDB(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		base   string
		dbName string
		assert func(t *testing.T, dsn string, err error)
	}

	cases := []testCase{
		{
			name:   "swaps the database and forces the three required parameters",
			base:   "root:wrkflw_root@tcp(127.0.0.1:3306)/wrkflw_test",
			dbName: "wrkflw_test_9",
			assert: func(t *testing.T, dsn string, err error) {
				require.NoError(t, err)
				// Asserted through the driver's own parser, not on the DSN text:
				// FormatDSN omits loc= when it equals the driver default (UTC), so
				// a Contains("loc=UTC") check would fail on a DSN that is in fact
				// correct. What must hold is the effective setting.
				cfg := requireParsedDSN(t, dsn)
				assert.Equal(t, "wrkflw_test_9", cfg.DBName)
				// parseTime/loc are required for correct DATETIME scanning and
				// multiStatements for goose's multi-statement migration files;
				// the container path hardcodes all three, so the env path must
				// not depend on the operator having spelled them out.
				assert.True(t, cfg.ParseTime, "parseTime")
				assert.Equal(t, time.UTC, cfg.Loc)
				assert.True(t, cfg.MultiStatements, "multiStatements")
			},
		},
		{
			name:   "overrides operator-supplied parameters that would break migrations",
			base:   "root:pw@tcp(db:3306)/base?parseTime=false&multiStatements=false&loc=Local",
			dbName: "wrkflw_test_10",
			assert: func(t *testing.T, dsn string, err error) {
				require.NoError(t, err)
				cfg := requireParsedDSN(t, dsn)
				assert.True(t, cfg.ParseTime, "parseTime must be forced back to true")
				assert.True(t, cfg.MultiStatements, "multiStatements must be forced back to true")
				assert.Equal(t, time.UTC, cfg.Loc, "loc must be forced back to UTC")
			},
		},
		{
			name:   "keeps credentials, protocol and address",
			base:   "bob:hunter2@tcp(mysql.internal:13306)/anything?charset=utf8mb4",
			dbName: "wrkflw_test_11",
			assert: func(t *testing.T, dsn string, err error) {
				require.NoError(t, err)
				assert.Contains(t, dsn, "bob:hunter2@tcp(mysql.internal:13306)")
				assert.Contains(t, dsn, "charset=utf8mb4")
			},
		},
		{
			name:   "rejects an unparsable DSN and names the env var",
			base:   "not-a-mysql-dsn",
			dbName: "wrkflw_test_12",
			assert: func(t *testing.T, _ string, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "workflow-dbtest")
				assert.Contains(t, err.Error(), dbtest.EnvMySQLDSN)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dsn, err := dbtest.MySQLDSNForDB(tc.base, tc.dbName)
			tc.assert(t, dsn, err)
		})
	}
}
