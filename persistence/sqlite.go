package persistence

// sqlite.go — consumer-facing façade over the SQLite persistence backend.
// SQLite is an in-process, single-node database intended for lightweight and
// test-oriented deployments. It does not support distributed advisory locking
// (dialect.ErrUnsupported is returned from any ownership-based flow), and has
// no LISTEN/NOTIFY mechanism (no relay notifier). Outbox relay and timer stores
// work via poll-only paths identical to the MySQL backend.
//
// Consumers who need multi-replica exclusivity (kernel.CachingStore +
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
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// SQLiteRelayOption configures a SQLite Relay returned by NewSQLiteRelay. It is
// an alias of the facade RelayOption. SQLite has no LISTEN/NOTIFY; its relay is
// poll-only (there is no SQLiteWithListenNotify). The same option values used
// with MySQL (e.g. MySQLWithPollInterval, MySQLWithBatchSize) are directly
// compatible because RelayOption is a shared type.
type SQLiteRelayOption = RelayOption

// SQLiteCallLinkOption configures a CallLinkStore returned by NewSQLiteCallLinkStore.
// It aliases the single store.CallLinkOption (same type as CallLinkOption and
// MySQLCallLinkOption).
type SQLiteCallLinkOption = store.CallLinkOption

// OpenSQLite constructs a SQLite-backed kernel.Store + JournalReader over db.
//
// The returned Store satisfies both kernel.Store and kernel.JournalReader,
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
//	r, err := runtime.NewProcessDriver(action.NewMapCatalog(nil), store)
//	if err != nil { log.Fatal(err) }
func OpenSQLite(ctx context.Context, db *sql.DB, opts ...Option) (Store, error) {
	q, err := database.From(db)
	if err != nil {
		return nil, err
	}
	if err := database.ProbeUTC(ctx, q, database.SQLite); err != nil {
		return nil, err
	}
	s, err := store.New(db, dialect.NewSQLite(), opts...)
	if err != nil {
		return nil, err
	}
	return s, nil
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

// NewSQLiteAdvisoryLockOwnership returns a fail-loud [kernel.Ownership] for
// SQLite deployments. SQLite provides no distributed advisory locking
// mechanism: [kernel.Ownership.Acquire] returns [dialect.ErrUnsupported] on
// every call; [kernel.Ownership.Release] is a no-op (returns nil) for a lock
// that was never held.
//
// This constructor exists so SQLite consumers can satisfy the ownership
// parameter required by [kernel.NewCachingStore] while making the
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
//	cachingStore, err := kernel.NewCachingStore(store, owner)
//	// Acquire will return (false, dialect.ErrUnsupported) — guard accordingly.
func NewSQLiteAdvisoryLockOwnership() (kernel.Ownership, io.Closer, error) {
	o, err := store.NewSQLiteOwnership()
	if err != nil {
		return nil, nil, err
	}
	return o, o, nil
}

// NewSQLiteTimerStore returns a kernel.TimerStore backed by SQLite, for
// Runner.RehydrateTimers. The db must already have migrations applied.
// Mirrors [NewMySQLTimerStore] for MySQL and [NewTimerStore] for Postgres.
//
// SQLite is single-node and in-process; this constructor is well-suited for
// embedded deployments, CLI tools, integration tests, and local development.
//
// Example:
//
//	db, _ := sql.Open("sqlite", "file:app.db?_pragma=journal_mode(WAL)")
//	persistence.MigrateSQLite(ctx, db)
//	ts := persistence.NewSQLiteTimerStore(db)
//	armed, err := ts.ListArmed(ctx)
func NewSQLiteTimerStore(db *sql.DB) (kernel.TimerStore, error) {
	return store.NewTimerStore(db, dialect.NewSQLite())
}

// NewSQLiteRelay constructs an outbox relay over db that publishes each event via pub.
// SQLite has no LISTEN/NOTIFY; the relay is poll-only: Run loops on the poll interval
// calling DrainOnce until the context is cancelled.
//
// Call relay.Run(ctx) in a goroutine to start continuous polling, or call
// relay.DrainOnce(ctx) to drain a single batch synchronously.
//
// Returns the same Relay interface as [NewMySQLRelay] and [NewRelay] (the Postgres
// analog) so the three backends are interchangeable at the consumer site.
//
// Available options: [MySQLWithPollInterval], [MySQLWithBatchSize],
// [MySQLWithRelayClock], [MySQLWithMaxDeliveryAttempts], [MySQLWithRelayBackoff],
// [MySQLWithRelayLogger], [MySQLWithRelayTracerProvider], [MySQLWithRelayMeterProvider].
// Note: there is no SQLiteWithListenNotify — SQLite is poll-only. Passing the
// Postgres-only [WithListenNotify] has no effect (SQLite provides no notifier).
//
// SQLite enforces a single writer (db.SetMaxOpenConns(1)); the relay's claim+publish
// cycle is compatible with this constraint.
//
// Example:
//
//	db, _ := sql.Open("sqlite", "file:app.db?_pragma=journal_mode(WAL)")
//	db.SetMaxOpenConns(1)
//	persistence.MigrateSQLite(ctx, db)
//	relay := persistence.NewSQLiteRelay(db, myPublisher,
//	    persistence.MySQLWithPollInterval(500*time.Millisecond),
//	)
//	go relay.Run(ctx)
func NewSQLiteRelay(db *sql.DB, pub kernel.Publisher, opts ...SQLiteRelayOption) (Relay, error) {
	var cfg relayConfig
	for _, o := range opts {
		o(&cfg)
	}
	// SQLite has no LISTEN/NOTIFY; cfg.listenNotify is intentionally ignored.
	return store.NewRelay(db, dialect.NewSQLite(), pub, cfg.opts...)
}

// NewSQLiteCallLinkStore constructs the SQLite-backed kernel.CallLinkStore (read/claim
// side). It provides ClaimPending, MarkNotified, LookupChild, and ListRunningChildren
// over the wrkflw_call_links table. The write side is fused into Store.Create /
// Store.Commit (ADR-0025); use [OpenSQLite] for that.
//
// Pass [WithCallLinkLease] and [WithCallLinkClock] to opt in to lease-based
// exclusivity. Existing zero-option call sites compile unchanged.
//
// MigrateSQLite must have been applied before the first call to any method.
//
// Mirrors [NewMySQLCallLinkStore] for MySQL and [NewCallLinkStore] for Postgres.
//
// SQLite is single-node; lease-based exclusivity is rarely needed but is
// supported for parity.
//
// Example:
//
//	db, _ := sql.Open("sqlite", "file:app.db?_pragma=journal_mode(WAL)")
//	persistence.MigrateSQLite(ctx, db)
//	cls := persistence.NewSQLiteCallLinkStore(db)
//	pending, err := cls.ClaimPending(ctx, 100)
func NewSQLiteCallLinkStore(db *sql.DB, opts ...SQLiteCallLinkOption) (kernel.CallLinkStore, error) {
	return store.NewCallLinkStore(db, dialect.NewSQLite(), opts...)
}

// NewSQLiteChainLinkStore constructs the SQLite-backed kernel.ChainLinkStore for
// process-instance chaining lineage (ADR-0045): Record persists one
// predecessor->successor hop; LookupBySuccessor and ListByPredecessor serve
// ancestry/audit queries. MigrateSQLite must have been applied before the first call.
//
// Mirrors [NewMySQLChainLinkStore] for MySQL and [NewChainLinkStore] for Postgres.
//
// Example:
//
//	db, _ := sql.Open("sqlite", "file:app.db?_pragma=journal_mode(WAL)")
//	persistence.MigrateSQLite(ctx, db)
//	links := persistence.NewSQLiteChainLinkStore(db)
//	chainer, err := runtime.NewChainer(runner, policy, runtime.WithChainLinks(links))
func NewSQLiteChainLinkStore(db *sql.DB) (kernel.ChainLinkStore, error) {
	return store.NewChainLinkStore(db, dialect.NewSQLite())
}

// NewSQLiteLister constructs the SQLite-backed kernel.InstanceLister for
// admin-list and monitoring use-cases. It executes a keyset-cursor-paginated
// query over wrkflw_instances and projects only the columns in
// kernel.InstanceSummary (no full snapshot read).
//
// MigrateSQLite must have been applied before the first call to List.
//
// Mirrors [NewMySQLLister] for MySQL and [NewLister] for Postgres.
//
// Example:
//
//	db, _ := sql.Open("sqlite", "file:app.db?_pragma=journal_mode(WAL)")
//	persistence.MigrateSQLite(ctx, db)
//	lister := persistence.NewSQLiteLister(db)
//	page, err := lister.List(ctx, kernel.InstanceFilter{Limit: 20})
func NewSQLiteLister(db *sql.DB) (kernel.InstanceLister, error) {
	return store.NewLister(db, dialect.NewSQLite())
}

// NewSQLiteCallNotifier builds a durable call-activity notifier over db using the
// SQLite call-link store: it claims terminal call links and resumes parked parents
// (SubInstanceCompleted/Failed) idempotently. Run it in a goroutine (notifier.Run)
// or drain manually (DrainOnce).
//
// This is the SQLite analog of [NewMySQLCallNotifier] and [NewCallNotifier] (the
// Postgres facade constructor). The underlying runtime.CallNotifier is
// dialect-agnostic: this constructor simply builds the SQLite-backed CallLinkStore
// and passes it to runtime.NewCallNotifier. opts are forwarded to
// runtime.NewCallNotifier; use runtime.WithCallNotifierClock to inject a fake clock
// in tests.
//
// SQLite is single-node and in-process; the notifier is well-suited for embedded
// deployments where a single process both runs and delivers call-link notifications.
//
// Example:
//
//	db, _ := sql.Open("sqlite", "file:app.db?_pragma=journal_mode(WAL)")
//	persistence.MigrateSQLite(ctx, db)
//	notifier := persistence.NewSQLiteCallNotifier(db, deliverFn, reg)
//	go notifier.Run(ctx)
func NewSQLiteCallNotifier(db *sql.DB, deliver runtime.CallDeliverFunc, reg kernel.DefinitionRegistry, opts ...runtime.CallNotifierOption) (*runtime.CallNotifier, error) {
	cls, err := store.NewCallLinkStore(db, dialect.NewSQLite())
	if err != nil {
		return nil, err
	}
	return runtime.NewCallNotifier(cls, deliver, reg, opts...)
}

// NewSQLiteDefinitionStore constructs the durable SQLite-backed definition store.
// It satisfies kernel.DefinitionRegistry via its Lookup method, which resolves
// a DefRef of the form "defID:version" (exact match) or "defID" (latest version).
//
// Use this together with [NewCachingDefinitionRegistry] to cache hot definitions.
// It returns the same [DefinitionStore] interface as [NewMySQLDefinitionStore] and
// [NewDefinitionStore] (the Postgres analog) so the three backends are
// interchangeable at the consumer site.
//
// Mirrors [NewMySQLDefinitionStore] for MySQL and [NewDefinitionStore] for Postgres.
//
// Example:
//
//	db, _ := sql.Open("sqlite", "file:app.db?_pragma=journal_mode(WAL)")
//	persistence.MigrateSQLite(ctx, db)
//	ds := persistence.NewSQLiteDefinitionStore(db)
//	cached := persistence.NewCachingDefinitionRegistry(ds, 5*time.Minute)
func NewSQLiteDefinitionStore(db *sql.DB) (DefinitionStore, error) {
	return store.NewDefinitionStore(db, dialect.NewSQLite())
}

// NewSQLitePruner constructs a Pruner over db (returns the stable [Pruner] interface).
// MigrateSQLite must have been applied before calling any method.
//
// It returns the same [Pruner] interface as [NewMySQLPruner] and [NewPruner] (the
// Postgres analog) so the three backends are interchangeable at the consumer site.
//
// Wire it into a scheduled job the consumer owns, e.g.:
//
//	db, _ := sql.Open("sqlite", "file:app.db?_pragma=journal_mode(WAL)")
//	persistence.MigrateSQLite(ctx, db)
//	pruner := persistence.NewSQLitePruner(db)
//	// every hour, drop outbox events published more than 7 days ago:
//	_, err := pruner.PruneOutbox(ctx, time.Now().Add(-7*24*time.Hour))
func NewSQLitePruner(db *sql.DB) (Pruner, error) {
	return store.NewPruner(db, dialect.NewSQLite())
}
