// Package dialect_test is the black-box test suite for the dialect package.
package dialect_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
)

// TestPostgresRebind exercises the ?→$n placeholder rewrite for varying
// numbers of parameters.
func TestPostgresRebind(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		input  string
		assert func(t *testing.T, got string)
	}

	cases := []testCase{
		{
			name:  "two placeholders",
			input: `INSERT INTO t (a,b) VALUES (?,?) ON CONFLICT DO NOTHING`,
			assert: func(t *testing.T, got string) {
				t.Helper()
				assert.Equal(t, `INSERT INTO t (a,b) VALUES ($1,$2) ON CONFLICT DO NOTHING`, got)
			},
		},
		{
			name:  "single placeholder",
			input: `SELECT * FROM t WHERE id = ?`,
			assert: func(t *testing.T, got string) {
				t.Helper()
				assert.Equal(t, `SELECT * FROM t WHERE id = $1`, got)
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

	d := dialect.NewPostgres()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, d.Rebind(tc.input))
		})
	}
}

// TestPostgresErrorClassification verifies IsUniqueViolation and
// IsRetryableConflict against known Postgres SQLSTATE codes.
func TestPostgresErrorClassification(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		assert func(t *testing.T, d dialect.Dialect)
	}

	cases := []testCase{
		{
			name: "23505 is unique violation",
			assert: func(t *testing.T, d dialect.Dialect) {
				t.Helper()
				require.True(t, d.IsUniqueViolation(&pgconn.PgError{Code: "23505"}))
			},
		},
		{
			name: "other code is not unique violation",
			assert: func(t *testing.T, d dialect.Dialect) {
				t.Helper()
				require.False(t, d.IsUniqueViolation(&pgconn.PgError{Code: "40001"}))
			},
		},
		{
			name: "nil error is not unique violation",
			assert: func(t *testing.T, d dialect.Dialect) {
				t.Helper()
				require.False(t, d.IsUniqueViolation(nil))
			},
		},
		{
			name: "40001 is retryable conflict (serialization failure)",
			assert: func(t *testing.T, d dialect.Dialect) {
				t.Helper()
				require.True(t, d.IsRetryableConflict(&pgconn.PgError{Code: "40001"}))
			},
		},
		{
			name: "other code is not retryable conflict",
			assert: func(t *testing.T, d dialect.Dialect) {
				t.Helper()
				require.False(t, d.IsRetryableConflict(&pgconn.PgError{Code: "23505"}))
			},
		},
		{
			name: "nil error is not retryable conflict",
			assert: func(t *testing.T, d dialect.Dialect) {
				t.Helper()
				require.False(t, d.IsRetryableConflict(nil))
			},
		},
	}

	d := dialect.NewPostgres()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, d)
		})
	}
}

// TestPostgresCapabilities verifies all boolean/string capability methods on
// the Postgres dialect in a single table so new methods can be added as rows.
func TestPostgresCapabilities(t *testing.T) {
	t.Parallel()

	d := dialect.NewPostgres()

	type testCase struct {
		name   string
		assert func(t *testing.T)
	}

	cases := []testCase{
		{
			name: "Name returns postgres",
			assert: func(t *testing.T) {
				t.Helper()
				assert.Equal(t, "postgres", d.Name())
			},
		},
		{
			name: "SupportsReturning is true",
			assert: func(t *testing.T) {
				t.Helper()
				assert.True(t, d.SupportsReturning())
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
			name: "JournalTriggerColumn returns trigger",
			assert: func(t *testing.T) {
				t.Helper()
				assert.Equal(t, "trigger", d.JournalTriggerColumn())
			},
		},
		{
			name: "NotifyStatement produces NOTIFY <channel>",
			assert: func(t *testing.T) {
				t.Helper()
				assert.Equal(t, "NOTIFY wrkflw_outbox", d.NotifyStatement("wrkflw_outbox"))
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

// TestPostgresUpsertClauses verifies that UpsertTimer and UpsertDefinition
// return the conflict-target clauses that match the real Postgres store.
func TestPostgresUpsertClauses(t *testing.T) {
	t.Parallel()

	d := dialect.NewPostgres()

	type testCase struct {
		name   string
		assert func(t *testing.T, got string)
	}

	cases := []testCase{
		{
			// Exact conflict clause from internal/persistence/postgres/store.go upsertTimer.
			name: "UpsertTimer conflict target matches real store",
			assert: func(t *testing.T, got string) {
				t.Helper()
				const want = " ON CONFLICT (instance_id, timer_id)" +
					" DO UPDATE SET fire_at = EXCLUDED.fire_at, kind = EXCLUDED.kind," +
					" def_id = EXCLUDED.def_id, def_version = EXCLUDED.def_version"
				assert.Equal(t, want, got)
			},
		},
		{
			// Exact conflict clause from internal/persistence/postgres/definitions.go PutDefinition.
			name: "UpsertDefinition conflict target matches real store",
			assert: func(t *testing.T, got string) {
				t.Helper()
				const want = " ON CONFLICT (def_id, version) DO UPDATE SET definition = EXCLUDED.definition"
				assert.Equal(t, want, got)
			},
		},
	}

	results := []string{d.UpsertTimer(), d.UpsertDefinition()}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, results[i])
		})
	}
}

// TestPostgresOutboxStatsQuery verifies that OutboxStatsQuery returns the exact
// SQL used by internal/persistence/postgres/relay.go OutboxStats.
func TestPostgresOutboxStatsQuery(t *testing.T) {
	t.Parallel()

	d := dialect.NewPostgres()
	got := d.OutboxStatsQuery()

	const want = `SELECT count(*) FILTER (WHERE status = 'pending'),
	        count(*) FILTER (WHERE status = 'dead'),
	        COALESCE(EXTRACT(EPOCH FROM now()-min(created_at) FILTER (WHERE status = 'pending')), 0)
	   FROM wrkflw_outbox`

	assert.Equal(t, want, got)
}
