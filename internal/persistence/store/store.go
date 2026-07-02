package store

import (
	"context"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
)

// Store is the vendor-neutral, dialect-parametrised persistence store. It holds
// a raw driver connection (either *pgxpool.Pool for Postgres or *sql.DB for
// MySQL/SQLite) and the matching [dialect.Dialect] value. Later tasks extend
// Store with port-specific methods (definitions, instances, timers, …) that
// share this conn + dialect pair.
//
// Store is safe for concurrent use: it carries no mutable state beyond the
// dialect-provided capabilities, which are themselves immutable and concurrency-safe.
type Store struct {
	conn    any // *pgxpool.Pool or *sql.DB
	dialect dialect.Dialect
	notify  dialect.Notifier // optional; nil by default
}

// Option is a functional option that configures a [Store] built by [New].
type Option func(*Store)

// WithNotifier injects the LISTEN receive-side capability. Only the
// (pgx, Postgres) combination provides a meaningful [dialect.Notifier];
// for MySQL and SQLite pass nil or omit this option.
func WithNotifier(n dialect.Notifier) Option {
	return func(s *Store) { s.notify = n }
}

// New constructs a [Store] over conn using dialect d. conn must be either a
// *pgxpool.Pool (Postgres) or a *sql.DB (MySQL, SQLite); any other type will
// cause [database.From] to return an error when the first query is issued.
//
// Example (Postgres):
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	s := store.New(pool, dialect.NewPostgres())
//
// Example (SQLite, tests):
//
//	db := dbtest.RunTestSQLite(t)
//	s := store.New(db, dialect.NewSQLite())
func New(conn any, d dialect.Dialect, opts ...Option) *Store {
	s := &Store{conn: conn, dialect: d}
	for _, o := range opts {
		o(s)
	}
	return s
}

// querier returns a pool-backed [database.Querier] over s.conn. It is used by
// standalone read methods that do not participate in an ambient transaction.
//
// Design note (controller decision): investigation confirmed there is no
// read-after-write-in-same-tx pattern in the current stores — reads never need
// to observe an uncommitted ambient write. Therefore the read path is wired
// directly to the pool/conn, keeping querier simple and free of context-key
// lookups. Multi-statement write methods obtain their Querier via
// transaction.JoinOrBegin and never call this helper.
func (s *Store) querier(ctx context.Context) database.Querier {
	_ = ctx // retained for API stability; callers pass ctx to the returned Querier's methods
	q, _ := database.From(s.conn)
	return q
}
