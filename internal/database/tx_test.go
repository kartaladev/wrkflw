package database_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
)

// mustExec executes a SQL statement via database.From and fails the test on error.
func mustExec(t *testing.T, conn any, query string, args ...any) {
	t.Helper()
	q, err := database.From(conn)
	require.NoError(t, err, "From(%T)", conn)
	_, err = q.Exec(t.Context(), query, args...)
	require.NoError(t, err, "Exec(%q)", query)
}

// assertCount queries a single-integer scalar and compares it to want.
func assertCount(t *testing.T, conn any, query string, want int64) {
	t.Helper()
	q, err := database.From(conn)
	require.NoError(t, err, "From(%T) for count", conn)
	var n int64
	require.NoError(t, q.QueryRow(t.Context(), query).Scan(&n), "count query")
	assert.Equal(t, want, n, "row count")
}

// toTableName converts a test-case name to a SQL-safe identifier.
func toTableName(name string) string {
	r := strings.NewReplacer("/", "_", "-", "_", " ", "_")
	return strings.Trim(r.Replace(name), "_")
}

// TestBeginTxCommitRollback verifies that BeginTx opens a real transaction for
// both the pgx (*pgxpool.Pool) and database/sql (*sql.DB) adapters, and that
// Commit persists rows while Rollback discards them.
//
// Table form is mandatory: four cases (two dialects × two outcomes) share the
// same BeginTx → Exec → Commit/Rollback call shape; the assert closure handles
// per-case verification as required by the table-test skill.
func TestBeginTxCommitRollback(t *testing.T) {
	type dialect struct {
		newConn   func(t *testing.T) any
		createDDL func(tbl string) string
		insertSQL func(tbl string) string
		countSQL  func(tbl string) string
	}

	pgx := dialect{
		newConn: func(t *testing.T) any { return database.RunTestDatabase(t) },
		createDDL: func(tbl string) string {
			return fmt.Sprintf(`CREATE TABLE %s (id int)`, tbl)
		},
		insertSQL: func(tbl string) string {
			return fmt.Sprintf(`INSERT INTO %s VALUES ($1)`, tbl)
		},
		countSQL: func(tbl string) string {
			return fmt.Sprintf(`SELECT count(*) FROM %s`, tbl)
		},
	}

	sql := dialect{
		newConn: func(t *testing.T) any { return database.RunTestMySQL(t) },
		createDDL: func(tbl string) string {
			return fmt.Sprintf("CREATE TABLE `%s` (id int)", tbl)
		},
		insertSQL: func(tbl string) string {
			return fmt.Sprintf("INSERT INTO `%s` VALUES (?)", tbl)
		},
		countSQL: func(tbl string) string {
			return fmt.Sprintf("SELECT count(*) FROM `%s`", tbl)
		},
	}

	type testCase struct {
		name     string
		d        dialect
		commitTx bool
		assert   func(t *testing.T, conn any, countSQL string, err error)
	}

	cases := []testCase{
		{
			name:     "pgx/commit",
			d:        pgx,
			commitTx: true,
			assert: func(t *testing.T, conn any, countSQL string, err error) {
				require.NoError(t, err)
				assertCount(t, conn, countSQL, 1)
			},
		},
		{
			name:     "pgx/rollback",
			d:        pgx,
			commitTx: false,
			assert: func(t *testing.T, conn any, countSQL string, err error) {
				require.NoError(t, err)
				assertCount(t, conn, countSQL, 0)
			},
		},
		{
			name:     "sql/commit",
			d:        sql,
			commitTx: true,
			assert: func(t *testing.T, conn any, countSQL string, err error) {
				require.NoError(t, err)
				assertCount(t, conn, countSQL, 1)
			},
		},
		{
			name:     "sql/rollback",
			d:        sql,
			commitTx: false,
			assert: func(t *testing.T, conn any, countSQL string, err error) {
				require.NoError(t, err)
				assertCount(t, conn, countSQL, 0)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Each subtest gets its own per-test database (via the shared
			// container helpers) so subtests don't share tables. The table
			// name is deterministic and safe to reuse because each subtest
			// has its own isolated database.
			conn := tc.d.newConn(t)
			tbl := "tx_t_" + toTableName(tc.name)

			// Create the table outside the transaction so its existence is
			// independent of the commit/rollback outcome.
			mustExec(t, conn, tc.d.createDDL(tbl))

			tx, err := database.BeginTx(t.Context(), conn)
			require.NoError(t, err, "BeginTx")

			_, err = tx.Exec(t.Context(), tc.d.insertSQL(tbl), 1)
			require.NoError(t, err, "Exec inside tx")

			var txErr error
			if tc.commitTx {
				txErr = tx.Commit(t.Context())
			} else {
				txErr = tx.Rollback(t.Context())
			}

			tc.assert(t, conn, tc.d.countSQL(tbl), txErr)
		})
	}
}
