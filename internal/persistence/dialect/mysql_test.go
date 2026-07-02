// Package dialect_test is the black-box test suite for the dialect package.
package dialect_test

import (
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
)

// TestMySQLRebind verifies that MySQL Rebind is an identity function because
// MySQL uses ? as its native placeholder style.
func TestMySQLRebind(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		input  string
		assert func(t *testing.T, got string)
	}

	cases := []testCase{
		{
			name:  "two placeholders unchanged",
			input: `INSERT INTO t (a,b) VALUES (?,?)`,
			assert: func(t *testing.T, got string) {
				t.Helper()
				assert.Equal(t, `INSERT INTO t (a,b) VALUES (?,?)`, got)
			},
		},
		{
			name:  "single placeholder unchanged",
			input: `SELECT * FROM t WHERE id = ?`,
			assert: func(t *testing.T, got string) {
				t.Helper()
				assert.Equal(t, `SELECT * FROM t WHERE id = ?`, got)
			},
		},
		{
			name:  "no placeholders passes through unchanged",
			input: `SELECT 1`,
			assert: func(t *testing.T, got string) {
				t.Helper()
				assert.Equal(t, `SELECT 1`, got)
			},
		},
	}

	d := dialect.NewMySQL()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, d.Rebind(tc.input))
		})
	}
}

// TestMySQLErrorClassification verifies IsUniqueViolation and
// IsRetryableConflict against known MySQL error numbers.
func TestMySQLErrorClassification(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		assert func(t *testing.T, d dialect.Dialect)
	}

	cases := []testCase{
		{
			name: "1062 is unique violation",
			assert: func(t *testing.T, d dialect.Dialect) {
				t.Helper()
				require.True(t, d.IsUniqueViolation(&mysqldriver.MySQLError{Number: 1062}))
			},
		},
		{
			name: "other error number is not unique violation",
			assert: func(t *testing.T, d dialect.Dialect) {
				t.Helper()
				require.False(t, d.IsUniqueViolation(&mysqldriver.MySQLError{Number: 1213}))
			},
		},
		{
			name: "nil is not unique violation",
			assert: func(t *testing.T, d dialect.Dialect) {
				t.Helper()
				require.False(t, d.IsUniqueViolation(nil))
			},
		},
		{
			name: "1213 is retryable conflict (deadlock)",
			assert: func(t *testing.T, d dialect.Dialect) {
				t.Helper()
				require.True(t, d.IsRetryableConflict(&mysqldriver.MySQLError{Number: 1213}))
			},
		},
		{
			name: "1205 is retryable conflict (lock wait timeout)",
			assert: func(t *testing.T, d dialect.Dialect) {
				t.Helper()
				require.True(t, d.IsRetryableConflict(&mysqldriver.MySQLError{Number: 1205}))
			},
		},
		{
			name: "other error number is not retryable conflict",
			assert: func(t *testing.T, d dialect.Dialect) {
				t.Helper()
				require.False(t, d.IsRetryableConflict(&mysqldriver.MySQLError{Number: 1062}))
			},
		},
		{
			name: "nil is not retryable conflict",
			assert: func(t *testing.T, d dialect.Dialect) {
				t.Helper()
				require.False(t, d.IsRetryableConflict(nil))
			},
		},
	}

	d := dialect.NewMySQL()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, d)
		})
	}
}

// TestMySQLCapabilities verifies all boolean/string capability methods on the
// MySQL dialect in a single table so new methods can be added as rows.
func TestMySQLCapabilities(t *testing.T) {
	t.Parallel()

	d := dialect.NewMySQL()

	type testCase struct {
		name   string
		assert func(t *testing.T)
	}

	cases := []testCase{
		{
			name: "Name returns mysql",
			assert: func(t *testing.T) {
				t.Helper()
				assert.Equal(t, "mysql", d.Name())
			},
		},
		{
			name: "SupportsReturning is false",
			assert: func(t *testing.T) {
				t.Helper()
				assert.False(t, d.SupportsReturning())
			},
		},
		{
			name: "SupportsSkipLocked is true",
			assert: func(t *testing.T) {
				t.Helper()
				assert.True(t, d.SupportsSkipLocked())
			},
		},
		{
			name: "InsertIgnorePrefix returns INSERT IGNORE",
			assert: func(t *testing.T) {
				t.Helper()
				assert.Equal(t, "INSERT IGNORE", d.InsertIgnorePrefix())
			},
		},
		{
			name: "InsertIgnoreDedup returns empty string",
			assert: func(t *testing.T) {
				t.Helper()
				assert.Equal(t, "", d.InsertIgnoreDedup())
			},
		},
		{
			name: `JournalTriggerColumn returns trigger_`,
			assert: func(t *testing.T) {
				t.Helper()
				assert.Equal(t, "trigger_", d.JournalTriggerColumn())
			},
		},
		{
			name: "NotifyStatement returns empty string",
			assert: func(t *testing.T) {
				t.Helper()
				assert.Equal(t, "", d.NotifyStatement("wrkflw_outbox"))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t)
		})
	}
}

// TestMySQLUpsertClauses verifies that UpsertTimer and UpsertDefinition return
// the ON DUPLICATE KEY UPDATE clauses that match the real MySQL store files.
func TestMySQLUpsertClauses(t *testing.T) {
	t.Parallel()

	d := dialect.NewMySQL()

	type testCase struct {
		name   string
		assert func(t *testing.T)
	}

	cases := []testCase{
		{
			// Copied verbatim from internal/persistence/mysql/store.go mysqlUpsertTimer
			// (the conflict clause that follows the base INSERT … VALUES line).
			name: "UpsertTimer matches real MySQL store",
			assert: func(t *testing.T) {
				t.Helper()
				want := "\n\t\t\tON DUPLICATE KEY UPDATE fire_at=VALUES(fire_at), kind=VALUES(kind)," +
					"\n\t\t\t                        def_id=VALUES(def_id), def_version=VALUES(def_version)"
				assert.Equal(t, want, d.UpsertTimer())
			},
		},
		{
			// Copied verbatim from internal/persistence/mysql/definitions.go PutDefinition
			// (the conflict clause that follows the base INSERT … VALUES line).
			name: "UpsertDefinition matches real MySQL store",
			assert: func(t *testing.T) {
				t.Helper()
				const want = "\n\t\t\t ON DUPLICATE KEY UPDATE definition = VALUES(definition)"
				assert.Equal(t, want, d.UpsertDefinition())
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t)
		})
	}
}

// TestMySQLOutboxStatsQuery verifies that OutboxStatsQuery returns the exact
// SQL used by internal/persistence/mysql/relay.go OutboxStats.
func TestMySQLOutboxStatsQuery(t *testing.T) {
	t.Parallel()

	d := dialect.NewMySQL()
	got := d.OutboxStatsQuery()

	// Copied verbatim from internal/persistence/mysql/relay.go OutboxStats.
	// Note: uses status='pending'/'dead' — NOT dead_lettered_at IS NULL/NOT NULL.
	const want = `SELECT COALESCE(SUM(status='pending'), 0),
		        COALESCE(SUM(status='dead'), 0),
		        COALESCE(TIMESTAMPDIFF(SECOND, MIN(CASE WHEN status='pending' THEN created_at END), NOW()), 0)
		   FROM wrkflw_outbox`

	assert.Equal(t, want, got)
}
