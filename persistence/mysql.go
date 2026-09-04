package persistence

// mysql.go contains the consumer-facing façade over the MySQL persistence
// backend. Every MySQL constructor is built on the neutral
// internal/persistence/store package parametrised with dialect.NewMySQL();
// OpenMySQL's ProbeUTC guard and the MigrateMySQL entry point are the only
// MySQL-specific pieces.
//
// The MySQL constructors take the facade's own Option / RelayOption /
// CallLinkOption types directly: all three backends share one option surface,
// so every With… constructor in options.go applies here unchanged.
//
// NewMySQLDeduper (in dedup.go) returns the unified persistence.Deduper, whose
// Seen joins the ambient transaction in ctx.

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/kartaladev/wrkflw/internal/database"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/runtime/calllink"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// OpenMySQL constructs a MySQL-backed kernel.InstanceStore + JournalReader over db.
//
// The returned InstanceStore satisfies both kernel.InstanceStore and kernel.JournalReader,
// identical to the interface returned by OpenPostgres. MigrateMySQL must be
// called before OpenMySQL so the required tables exist (or use RunTestMySQL in
// tests which auto-migrates).
//
// Example:
//
//	db, _ := sql.Open("mysql", dsn)
//	persistence.MigrateMySQL(ctx, db)
//	store, _ := persistence.OpenMySQL(ctx, db, persistence.WithHistoryCap(50))
//	r, err := runtime.NewProcessDriver(runtime.WithInstanceStore(store))
//	if err != nil { log.Fatal(err) }
func OpenMySQL(ctx context.Context, db *sql.DB, opts ...Option) (InstanceStore, error) {
	q, err := database.From(db)
	if err != nil {
		return nil, err
	}
	if err := database.ProbeUTC(ctx, q, database.MySQL); err != nil {
		return nil, err
	}
	s, err := store.New(db, dialect.NewMySQL(), buildStoreOptions(opts)...)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// MigrateMySQL applies the embedded schema migrations to the MySQL database
// reachable through db. It is idempotent: goose's version table ensures
// re-runs are no-ops. Mirrors Migrate for Postgres.
//
// MigrateMySQL is intended to be called explicitly by the consumer during
// application startup — it is never auto-invoked on import.
func MigrateMySQL(ctx context.Context, db *sql.DB) error {
	return store.MigrateMySQL(ctx, db)
}

// NewMySQLTimerStore returns a kernel.TimerStore backed by MySQL. It backs
// ProcessDriver.RehydrateTimers (explicit re-arm) and, via runtime.NewJobStore's
// Load, the scheduler's own automatic self-rehydration on Start. The
// db must already have migrations applied. Mirrors NewTimerStore for Postgres.
//
// Alongside ListArmed, the returned store serves kernel.TimerStore's ArmedTimer
// point lookup — a primary-key-exact read of one (instanceID, timerID) pair, used
// by the timer-fire path to test recurrence without reading the whole armed
// table; a missing row is (zero, false, nil), not an error. The concrete
// store additionally satisfies service.TimerAdmin (Stats plus the keyset-paged
// ListArmedPage) for admin listing; neither method is on the returned
// kernel.TimerStore interface, so reach them via `admin, ok := ts.(service.TimerAdmin)`.
//
// Example:
//
//	db, _ := sql.Open("mysql", dsn)
//	persistence.MigrateMySQL(ctx, db)
//	ts, err := persistence.NewMySQLTimerStore(db)
//	if err != nil { /* handle */ }
//	armed, err := ts.ListArmed(ctx)
func NewMySQLTimerStore(db *sql.DB) (kernel.TimerStore, error) {
	return store.NewTimerStore(db, dialect.NewMySQL())
}

// NewMySQLRelay constructs an outbox relay over db that publishes each event via pub.
// MySQL has no LISTEN/NOTIFY; the relay is poll-only: Run loops on the poll interval
// calling DrainOnce until the context is cancelled.
//
// Call relay.Run(ctx) in a goroutine to start continuous polling, or call
// relay.DrainOnce(ctx) to drain a single batch synchronously.
//
// Returns the same Relay interface as NewRelay (the Postgres analog) so the two
// backends are interchangeable at the consumer site.
//
// Available options: [WithPollInterval], [WithBatchSize], [WithRelayClock],
// [WithMaxDeliveryAttempts], [WithRelayBackoff], [WithRelayLogger],
// [WithRelayTracerProvider], [WithRelayMeterProvider].
// Note: MySQL is poll-only. Passing the Postgres-only [WithListenNotify] has no
// effect (MySQL provides no notifier).
//
// Example:
//
//	db, _ := sql.Open("mysql", dsn)
//	persistence.MigrateMySQL(ctx, db)
//	relay := persistence.NewMySQLRelay(db, myPublisher,
//	    persistence.WithPollInterval(500*time.Millisecond),
//	)
//	go relay.Run(ctx)
func NewMySQLRelay(db *sql.DB, pub kernel.OutboxPublisher, opts ...RelayOption) (Relay, error) {
	var cfg relayConfig
	for _, o := range opts {
		o(&cfg)
	}
	// MySQL has no LISTEN/NOTIFY; cfg.listenNotify is intentionally ignored.
	return store.NewRelay(db, dialect.NewMySQL(), pub, cfg.opts...)
}

// NewMySQLCallLinkStore constructs the MySQL-backed kernel.CallLinkStore (read/claim
// side). It provides ClaimPending, MarkNotified, LookupChild, and ListRunningChildren
// over the wrkflw_call_links table. The write side is fused into Store.Create /
// Store.Commit; use OpenMySQL for that.
//
// Pass [WithCallLinkLease] and [WithCallLinkClock] to opt in to lease-based
// multi-replica exclusivity. Existing zero-option call sites compile unchanged.
//
// MigrateMySQL must have been applied before the first call to any method.
//
// Mirrors NewCallLinkStore for the Postgres facade.
func NewMySQLCallLinkStore(db *sql.DB, opts ...CallLinkOption) (kernel.CallLinkStore, error) {
	return store.NewCallLinkStore(db, dialect.NewMySQL(), buildCallLinkOptions(opts)...)
}

// NewMySQLAdvisoryLockOwnership constructs a multi-process [kernel.InstanceOwnership]
// backed by MySQL GET_LOCK advisory locks, for use with [NewCachingInstanceStore]
// across multiple replicas sharing one database.
//
// It holds a dedicated *sql.Conn for its lifetime; close the returned [io.Closer]
// at shutdown to release every held lock and return the connection.
//
// Mirrors NewAdvisoryLockOwnership for the Postgres facade.
//
// Example:
//
//	db, _ := sql.Open("mysql", dsn)
//	persistence.MigrateMySQL(ctx, db)
//	owner, closer, _ := persistence.NewMySQLAdvisoryLockOwnership(ctx, db)
//	defer closer.Close()
//	store, _ := persistence.OpenMySQL(ctx, db)
//	cachingStore, err := persistence.NewCachingInstanceStore(store, owner, hotcache.New())
func NewMySQLAdvisoryLockOwnership(ctx context.Context, db *sql.DB) (kernel.InstanceOwnership, io.Closer, error) {
	o, err := store.NewMySQLOwnership(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	return o, o, nil
}

// NewMySQLChainLinkStore constructs the MySQL-backed kernel.ChainLinkStore for
// process-instance chaining lineage: Record persists one
// predecessor->successor hop; LookupBySuccessor and ListByPredecessor serve
// ancestry/audit queries. MigrateMySQL must have been applied before the first call.
//
// Mirrors NewChainLinkStore for the Postgres facade.
//
// Example:
//
//	db, _ := sql.Open("mysql", dsn)
//	persistence.MigrateMySQL(ctx, db)
//	links := persistence.NewMySQLChainLinkStore(db)
//	chainer, err := chain.NewChainer(runner, policy, chain.WithChainLinks(links))
//
// Pass [WithChainLinkClock] to control the created_at fallback Record uses when
// a link carries a zero CreatedAt. Zero-option call sites compile
// unchanged.
func NewMySQLChainLinkStore(db *sql.DB, opts ...ChainLinkOption) (kernel.ChainLinkStore, error) {
	return store.NewChainLinkStore(db, dialect.NewMySQL(), buildChainLinkOptions(opts)...)
}

// NewMySQLLister constructs the MySQL-backed kernel.InstanceLister for
// admin-list and monitoring use-cases. It executes a keyset-cursor-paginated
// query over wrkflw_instances and projects only the columns in
// kernel.InstanceSummary (no full snapshot read).
//
// MigrateMySQL must have been applied before the first call to List.
//
// Mirrors NewLister for the Postgres facade.
//
// Example:
//
//	db, _ := sql.Open("mysql", dsn)
//	persistence.MigrateMySQL(ctx, db)
//	lister := persistence.NewMySQLLister(db)
//	page, err := lister.List(ctx, kernel.InstanceFilter{Limit: 20})
func NewMySQLLister(db *sql.DB) (kernel.InstanceLister, error) {
	return store.NewLister(db, dialect.NewMySQL())
}

// NewMySQLCallNotifier builds a durable call-activity notifier over db using the
// MySQL call-link store: it claims terminal call links and resumes parked parents
// (SubInstanceCompleted/Failed) idempotently. Run it in a goroutine (notifier.Run)
// or drain manually (DrainOnce).
//
// This is the MySQL analog of [NewCallNotifier] (the Postgres facade constructor).
// The underlying runtime.CallNotifier is dialect-agnostic: this constructor simply
// builds the MySQL-backed CallLinkStore and passes it to calllink.NewCallNotifier.
// opts are forwarded to calllink.NewCallNotifier; use calllink.WithClock
// to inject a fake clock in tests.
//
// Example:
//
//	db, _ := sql.Open("mysql", dsn)
//	persistence.MigrateMySQL(ctx, db)
//	notifier := persistence.NewMySQLCallNotifier(db, deliverFn, reg)
//	go notifier.Run(ctx)
func NewMySQLCallNotifier(db *sql.DB, deliver calllink.CallDeliverFunc, reg kernel.DefinitionRegistry, opts ...calllink.CallNotifierOption) (*calllink.CallNotifier, error) {
	cls, err := store.NewCallLinkStore(db, dialect.NewMySQL())
	if err != nil {
		return nil, err
	}
	return calllink.NewCallNotifier(cls, deliver, reg, opts...)
}

// NewMySQLDefinitionStore constructs the durable MySQL-backed definition store.
// It satisfies kernel.DefinitionRegistry via its Lookup method, which accepts a
// model.Qualifier: Latest(id) returns the highest version; Version(id,v) is exact.
//
// Use this together with NewCachingDefinitionRegistry to cache hot definitions.
// It returns the same DefinitionStore interface as NewDefinitionStore (the Postgres
// analog) so the two backends are interchangeable at the consumer site.
//
// Example:
//
//	db, _ := sql.Open("mysql", dsn)
//	persistence.MigrateMySQL(ctx, db)
//	ds := persistence.NewMySQLDefinitionStore(db)
//	cached := persistence.NewCachingDefinitionRegistry(ds, 5*time.Minute)
//
// Pass [WithDefinitionClock] to control the created_at stamp PutDefinition
// writes. Zero-option call sites compile unchanged.
func NewMySQLDefinitionStore(db *sql.DB, opts ...DefinitionOption) (DefinitionStore, error) {
	return store.NewDefinitionStore(db, dialect.NewMySQL(), buildDefinitionOptions(opts)...)
}

// NewMySQLPruner constructs a Pruner over db (returns the stable Pruner interface).
// MigrateMySQL must have been applied before calling any method.
//
// It returns the same Pruner interface as NewPruner (the Postgres analog) so the
// two backends are interchangeable at the consumer site.
//
// Wire it into a scheduled job the consumer owns, e.g.:
//
//	db, _ := sql.Open("mysql", dsn)
//	persistence.MigrateMySQL(ctx, db)
//	pruner := persistence.NewMySQLPruner(db)
//	// every hour, drop outbox events published more than 7 days ago:
//	_, err := pruner.PruneOutbox(ctx, time.Now().Add(-7*24*time.Hour))
func NewMySQLPruner(db *sql.DB) (Pruner, error) {
	return store.NewPruner(db, dialect.NewMySQL())
}

// MySQLDSN returns base with the parameters required for correct DATETIME(6)
// time handling: parseTime=true, loc=UTC, and time_zone='+00:00' (applied as a
// session SET on every connection by go-sql-driver). Existing values are
// overridden so the result is idempotent regardless of what base contains.
//
// Example:
//
//	dsn, err := persistence.MySQLDSN("user:pass@tcp(127.0.0.1:3306)/wrkflw")
//	if err != nil { ... }
//	db, _ := sql.Open("mysql", dsn)
func MySQLDSN(base string) (string, error) {
	cfg, err := mysql.ParseDSN(base)
	if err != nil {
		return "", fmt.Errorf("workflow-persistence-mysql: parse dsn: %w", err)
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["time_zone"] = "'+00:00'"
	return cfg.FormatDSN(), nil
}

// Compile-time checks: the neutral store concrete types must satisfy the same
// public interfaces as their Postgres analogs.
var (
	_ InstanceStore             = (*store.Store)(nil)
	_ kernel.TimerStore         = (*store.TimerStore)(nil)
	_ Relay                     = (*store.Relay)(nil)
	_ Deduper                   = (*store.Deduper)(nil)
	_ kernel.CallLinkStore      = (*store.CallLinkStore)(nil)
	_ kernel.ChainLinkStore     = (*store.ChainLinkStore)(nil)
	_ kernel.InstanceLister     = (*store.Lister)(nil)
	_ kernel.InstanceOwnership  = (*store.AdvisoryLockOwnership)(nil)
	_ DefinitionStore           = (*store.DefinitionStore)(nil)
	_ Pruner                    = (*store.Pruner)(nil)
	_ kernel.DefinitionRegistry = (*store.DefinitionStore)(nil)
)
