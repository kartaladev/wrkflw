// Package store_test contains the 3-dialect conformance harness for the neutral
// store package. Every later store-port task adds its conformance assertions
// to this file by calling forEachDialect.
package store_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
	"github.com/zakyalvan/krtlwrkflw/persistence"
)

// backend carries the three pieces a conformance test needs for one dialect:
// a migrated connection, the dialect value, and a stable name for t.Run labels
// and capability gates.
type backend struct {
	name    string
	conn    any
	dialect dialect.Dialect
}

// forEachDialect runs fn against Postgres (pgx pool), MySQL (*sql.DB), and
// SQLite (*sql.DB), each with a fresh migrated database. Subtests are labelled
// by backend.name so capability-gated tests can skip with t.Skip when
// b.name != "postgres".
//
// Postgres: RunTestDatabase returns a bare pool; migrations are applied via the
// public persistence.Migrate facade so the harness stays independent of internal
// postgres package details.
// MySQL and SQLite: the helpers already migrate on construction.
func forEachDialect(t *testing.T, fn func(t *testing.T, b backend)) {
	t.Helper()

	t.Run("postgres", func(t *testing.T) {
		t.Parallel()
		pool := dbtest.RunTestDatabase(t) // bare pool — no migrations yet
		require.NoError(t, persistence.Migrate(t.Context(), pool), "migrate postgres")
		fn(t, backend{name: "postgres", conn: pool, dialect: dialect.NewPostgres()})
	})

	t.Run("mysql", func(t *testing.T) {
		t.Parallel()
		db := dbtest.RunTestMySQL(t) // already migrated
		fn(t, backend{name: "mysql", conn: db, dialect: dialect.NewMySQL()})
	})

	t.Run("sqlite", func(t *testing.T) {
		t.Parallel()
		db := dbtest.RunTestSQLite(t) // already migrated via store.MigrateSQLite
		fn(t, backend{name: "sqlite", conn: db, dialect: dialect.NewSQLite()})
	})
}

// TestStoreQuerierRoundTrip verifies that store.New + QuerierForTest returns a
// working Querier on every supported backend by executing a trivial SELECT 1.
func TestStoreQuerierRoundTrip(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s := store.New(b.conn, b.dialect)
		q := s.QuerierForTest(t.Context())

		var one int
		require.NoError(t,
			q.QueryRow(t.Context(), b.dialect.Rebind(`SELECT 1`)).Scan(&one),
			"QueryRow SELECT 1 on %s", b.name,
		)
		require.Equal(t, 1, one, "dialect %s: expected SELECT 1 = 1", b.name)
	})
}
