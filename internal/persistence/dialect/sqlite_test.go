// Package dialect_test is the black-box test suite for the dialect package.
package dialect_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
)

// TestSQLiteRebind verifies that SQLite Rebind is an identity function because
// SQLite uses ? as its native placeholder style.
func TestSQLiteRebind(t *testing.T) {
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

	d := dialect.NewSQLite()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, d.Rebind(tc.input))
		})
	}
}

// TestSQLiteCapabilities verifies all boolean/string capability methods on the
// SQLite dialect in a single table so new methods can be added as rows.
func TestSQLiteCapabilities(t *testing.T) {
	t.Parallel()

	d := dialect.NewSQLite()

	type testCase struct {
		name   string
		assert func(t *testing.T)
	}

	cases := []testCase{
		{
			name: "Name returns sqlite",
			assert: func(t *testing.T) {
				t.Helper()
				assert.Equal(t, "sqlite", d.Name())
			},
		},
		{
			name: "SupportsReturning is true (SQLite >= 3.35)",
			assert: func(t *testing.T) {
				t.Helper()
				assert.True(t, d.SupportsReturning())
			},
		},
		{
			name: "SupportsSkipLocked is false (SQLite has no FOR UPDATE SKIP LOCKED)",
			assert: func(t *testing.T) {
				t.Helper()
				assert.False(t, d.SupportsSkipLocked())
			},
		},
		{
			name: "InsertIgnorePrefix returns INSERT",
			assert: func(t *testing.T) {
				t.Helper()
				assert.Equal(t, "INSERT", d.InsertIgnorePrefix())
			},
		},
		{
			name: "InsertIgnoreDedup returns ON CONFLICT DO NOTHING suffix",
			assert: func(t *testing.T) {
				t.Helper()
				assert.Equal(t, " ON CONFLICT DO NOTHING", d.InsertIgnoreDedup())
			},
		},
		{
			name: `JournalTriggerColumn returns "trigger" (not a reserved word in SQLite)`,
			assert: func(t *testing.T) {
				t.Helper()
				assert.Equal(t, "trigger", d.JournalTriggerColumn())
			},
		},
		{
			name: "NotifyStatement returns empty string (SQLite has no pub/sub)",
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

// TestSQLiteUpsertClauses verifies that UpsertTimer and UpsertDefinition return
// the conflict clauses that are semantically equivalent to the Postgres store's
// clauses (same conflict targets and updated columns, lowercase excluded.*).
func TestSQLiteUpsertClauses(t *testing.T) {
	t.Parallel()

	d := dialect.NewSQLite()

	type testCase struct {
		name   string
		assert func(t *testing.T)
	}

	cases := []testCase{
		{
			name: "UpsertTimer mirrors Postgres columns with lowercase excluded",
			assert: func(t *testing.T) {
				t.Helper()
				const want = " ON CONFLICT (instance_id, timer_id)" +
					" DO UPDATE SET fire_at = excluded.fire_at, kind = excluded.kind," +
					" def_id = excluded.def_id, def_version = excluded.def_version"
				assert.Equal(t, want, d.UpsertTimer())
			},
		},
		{
			name: "UpsertDefinition mirrors Postgres conflict target with lowercase excluded",
			assert: func(t *testing.T) {
				t.Helper()
				const want = " ON CONFLICT (def_id, version) DO UPDATE SET definition = excluded.definition"
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

// TestSQLiteOutboxStatsQuery verifies that OutboxStatsQuery returns a SQL
// statement that is semantically equivalent to the Postgres/MySQL versions:
// same column order (pending count, dead count, oldest-pending age in seconds)
// from wrkflw_outbox using status='pending'/'dead'.
func TestSQLiteOutboxStatsQuery(t *testing.T) {
	t.Parallel()

	d := dialect.NewSQLite()
	got := d.OutboxStatsQuery()

	// SQLite has no FILTER aggregate or EXTRACT. We use CASE/WHEN inside SUM
	// and julianday arithmetic for the age in seconds. Column order matches
	// the Postgres version: pending, dead, age.
	const want = `SELECT` +
		` COALESCE(SUM(CASE WHEN status='pending' THEN 1 ELSE 0 END),0),` +
		` COALESCE(SUM(CASE WHEN status='dead' THEN 1 ELSE 0 END),0),` +
		` COALESCE(CAST((julianday('now') - julianday(MIN(CASE WHEN status='pending' THEN created_at END))) * 86400 AS INTEGER), 0)` +
		` FROM wrkflw_outbox`

	assert.Equal(t, want, got)
}

// TestSQLiteErrorClassification_NilAndNonSQLite verifies that nil and
// non-sqlite errors are not classified as unique violations or retryable
// conflicts. Positive classification (real *sqlite.Error with a known code)
// requires a live SQLite connection; those cases are covered by the
// conformance suite in Task 7+.
func TestSQLiteErrorClassification_NilAndNonSQLite(t *testing.T) {
	t.Parallel()

	d := dialect.NewSQLite()

	type testCase struct {
		name   string
		assert func(t *testing.T, d dialect.Dialect)
	}

	otherErr := fmt.Errorf("some generic error")
	wrappedErr := fmt.Errorf("wrapped: %w", errors.New("inner"))

	cases := []testCase{
		{
			name: "nil is not unique violation",
			assert: func(t *testing.T, d dialect.Dialect) {
				t.Helper()
				require.False(t, d.IsUniqueViolation(nil))
			},
		},
		{
			name: "non-sqlite error is not unique violation",
			assert: func(t *testing.T, d dialect.Dialect) {
				t.Helper()
				require.False(t, d.IsUniqueViolation(otherErr))
			},
		},
		{
			name: "wrapped non-sqlite error is not unique violation",
			assert: func(t *testing.T, d dialect.Dialect) {
				t.Helper()
				require.False(t, d.IsUniqueViolation(wrappedErr))
			},
		},
		{
			name: "nil is not retryable conflict",
			assert: func(t *testing.T, d dialect.Dialect) {
				t.Helper()
				require.False(t, d.IsRetryableConflict(nil))
			},
		},
		{
			name: "non-sqlite error is not retryable conflict",
			assert: func(t *testing.T, d dialect.Dialect) {
				t.Helper()
				require.False(t, d.IsRetryableConflict(otherErr))
			},
		},
		{
			name: "wrapped non-sqlite error is not retryable conflict",
			assert: func(t *testing.T, d dialect.Dialect) {
				t.Helper()
				require.False(t, d.IsRetryableConflict(wrappedErr))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, d)
		})
	}
}
