package persistence

// sqlite.go — consumer-facing façade over the SQLite persistence backend.
// SQLite is an in-process, single-node database intended for lightweight and
// test-oriented deployments. It does not support distributed advisory locking
// (dialect.ErrUnsupported is returned from any ownership-based flow), and has
// no LISTEN/NOTIFY mechanism (no relay notifier). Outbox relay and timer stores
// work via poll-only paths identical to the MySQL backend.
//
// Consumers who need multi-replica exclusivity (runtime.CachingStore +
// Ownership) must use the Postgres or MySQL backend. The SQLite backend is
// well-suited for embedded single-process deployments, CLI tools, integration
// tests, and local development where a network database is unavailable.

import (
	"context"
	"database/sql"
	"io"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// OpenSQLite constructs a SQLite-backed runtime.Store + JournalReader over db.
//
// The returned Store satisfies both runtime.Store and runtime.JournalReader,
// identical to the interface returned by [OpenPostgres] and [OpenMySQL].
// [MigrateSQLite] must be called before OpenSQLite so the required tables exist
// (or use [dbtest.RunTestSQLite] in tests, which auto-migrates).
//
// SQLite is a single-node, in-process backend. It is not suitable for
// multi-replica deployments that require distributed advisory locking — use
// [NewSQLiteAdvisoryLockOwnership] to obtain a fail-loud ownership value that
// returns [dialect.ErrUnsupported] on every lock attempt. Use [OpenPostgres] or
// [OpenMySQL] for multi-process deployments.
//
// The caller is responsible for registering the SQLite driver before opening the
// db (import _ "modernc.org/sqlite") and for setting db.SetMaxOpenConns(1) to
// enforce single-writer serialisation.
//
// Example:
//
//	import _ "modernc.org/sqlite"
//
//	db, _ := sql.Open("sqlite", "file:app.db?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
//	db.SetMaxOpenConns(1)
//	persistence.MigrateSQLite(ctx, db)
//	store, _ := persistence.OpenSQLite(ctx, db, persistence.WithHistoryCap(50))
//	runner := runtime.NewRunner(nil, store)
func OpenSQLite(ctx context.Context, db *sql.DB, opts ...Option) (Store, error) {
	q, err := database.From(db)
	if err != nil {
		return nil, err
	}
	if err := database.ProbeUTC(ctx, q, database.SQLite); err != nil {
		return nil, err
	}
	return store.New(db, dialect.NewSQLite(), opts...), nil
}

// MigrateSQLite applies the embedded SQLite schema migrations to db. It is
// idempotent: goose's version table ensures re-runs are no-ops.
//
// MigrateSQLite is intended to be called explicitly by the consumer during
// application startup — it is never auto-invoked on import.
//
// Example:
//
//	db, _ := sql.Open("sqlite", "file:app.db?_pragma=journal_mode(WAL)")
//	if err := persistence.MigrateSQLite(ctx, db); err != nil { ... }
//	store, _ := persistence.OpenSQLite(ctx, db)
func MigrateSQLite(ctx context.Context, db *sql.DB) error {
	return store.MigrateSQLite(ctx, db)
}

// NewSQLiteAdvisoryLockOwnership returns a fail-loud [runtime.Ownership] for
// SQLite deployments. SQLite provides no distributed advisory locking
// mechanism: [runtime.Ownership.Acquire] and [runtime.Ownership.Release] both
// return [dialect.ErrUnsupported] on every call.
//
// This constructor exists so SQLite consumers can satisfy the ownership
// parameter required by [runtime.NewCachingStore] while making the
// unsupported-locking contract explicit. Ownership-dependent flows must guard
// against [dialect.ErrUnsupported] and skip the exclusivity path when running
// on SQLite.
//
// No connection or context is required: the underlying locker is stateless.
// Close the returned [io.Closer] at shutdown (it is a no-op for SQLite, but
// mirrors the shutdown contract of [NewAdvisoryLockOwnership] and
// [NewMySQLAdvisoryLockOwnership]).
//
// Example:
//
//	owner, closer, _ := persistence.NewSQLiteAdvisoryLockOwnership()
//	defer closer.Close()
//	store, _ := persistence.OpenSQLite(ctx, db)
//	cachingStore := runtime.NewCachingStore(store, owner)
//	// Acquire will return (false, dialect.ErrUnsupported) — guard accordingly.
func NewSQLiteAdvisoryLockOwnership() (runtime.Ownership, io.Closer, error) {
	o, err := store.NewSQLiteOwnership()
	if err != nil {
		return nil, nil, err
	}
	return o, o, nil
}
