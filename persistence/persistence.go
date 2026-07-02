// Package persistence is the consumer-facing façade over the neutral
// persistence store (ADR-0008). It exposes constructors, stable port/interface
// types, options, and re-exported sentinels so library consumers never have to
// import internal persistence packages directly.
//
// # Usage
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	if err := persistence.Migrate(ctx, pool); err != nil { ... }
//
//	store, err := persistence.OpenPostgres(ctx, pool)
//	runner := runtime.NewRunner(cat, store)
//
// # Relay (transactional outbox drain)
//
//	relay := persistence.NewRelay(pool, myPublisher)
//	go relay.Run(ctx)
package persistence

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Store is the stable public interface for the Postgres-backed store.
// It composes runtime.Store (Create/Load/Commit) and runtime.JournalReader
// (Entries) so consumers never need to reference internal package paths.
// OpenPostgres returns this interface; internal churn never affects this type.
type Store interface {
	runtime.Store
	runtime.JournalReader
}

// DefinitionStore is the stable public interface for the Postgres-backed
// process-definition store. It is satisfied by the internal DefinitionStore
// implementation; consumers interact with it only through this interface.
type DefinitionStore interface {
	// PutDefinition upserts a process definition (idempotent on (ID, Version)).
	PutDefinition(ctx context.Context, def *model.ProcessDefinition) error
	// Lookup resolves a DefRef string ("defID:version" or "defID") to a definition.
	// Returns runtime.ErrDefinitionNotFound when no matching row exists.
	// ctx is propagated to the underlying SQL query for cancellation support.
	Lookup(ctx context.Context, defRef string) (*model.ProcessDefinition, error)
}

// Relay is the stable public interface for the transactional outbox drain.
// It is satisfied by the internal Relay implementation. NewRelay returns this
// interface so consumers are not bound to the concrete *postgres.Relay.
type Relay interface {
	// Run drains the outbox on each poll tick until ctx is cancelled.
	// Publish failures are absorbed per-row: a failing row is retried with
	// exponential backoff and quarantined to a dead-letter status after
	// MaxDeliveryAttempts; Run continues polling. Only infrastructure errors
	// (claim or commit failures) propagate and terminate the loop. Run always
	// returns ctx.Err() on cancellation.
	Run(ctx context.Context) error
	// DrainOnce claims and publishes one batch of outbox rows synchronously.
	// Returns the number of rows published.
	DrainOnce(ctx context.Context) (int, error)
	// ListDeadLettered returns up to limit dead-lettered outbox rows, oldest
	// first. Dead rows were quarantined after exhausting MaxDeliveryAttempts
	// failed publish attempts. Use Redrive to re-queue selected rows.
	ListDeadLettered(ctx context.Context, limit int) ([]runtime.DeadLetter, error)
	// Redrive resets the given dead rows back to pending (retry_count=0,
	// next_attempt_at=now) so they are eligible for a future DrainOnce.
	// Only rows with status='dead' are affected; others are silently skipped.
	// Returns the number of rows successfully re-queued. Passing no ids is a
	// no-op (returns 0, nil).
	Redrive(ctx context.Context, ids ...int64) (int, error)
	// OutboxStats returns aggregate statistics about the wrkflw_outbox table:
	// the count of pending rows (awaiting publication), the count of dead rows
	// (quarantined after exhausting MaxDeliveryAttempts), and the age of the
	// oldest pending row (zero when there are no pending rows).
	//
	// Implements [runtime.OutboxStatsReader] — callers that previously
	// type-asserted to that interface can call this method directly.
	OutboxStats(ctx context.Context) (runtime.OutboxStats, error)
}

// Publisher is the broker-agnostic outbox publisher alias (same as runtime.Publisher).
type Publisher = runtime.Publisher

// Option configures the Store returned by OpenPostgres (alias of the neutral
// store.Option). The same underlying type also backs OpenMySQL's MySQLOption:
// the two backends share one option surface after the store-unification refactor.
type Option = store.Option

// WithHistoryCap bounds the inline instance History persisted in the snapshot
// to every open visit plus at most n most-recent closed visits (ADR-0021).
// Unset / n <= 0 keeps full inline history (current behavior). The journal
// table remains the complete audit source.
func WithHistoryCap(n int) Option { return store.WithHistoryCap(n) }

// WithOutboxNotify makes the Store emit a transactional NOTIFY wrkflw_outbox
// when a step inserts outbox rows, so a relay started with WithListenNotify
// drains with sub-poll-interval latency (ADR-0022). Opt-in; default off.
// Only Postgres emits a NOTIFY; MySQL silently skips it.
func WithOutboxNotify() Option { return store.WithOutboxNotify() }

// WithStoreLogger sets the structured logger used by the Store for operation logs.
// Default: slog.Default().
func WithStoreLogger(l *slog.Logger) Option { return store.WithStoreLogger(l) }

// WithStoreTracerProvider sets the OTel TracerProvider for Store operation spans
// (wrkflw.store.load, wrkflw.store.commit). Default: the OTel global provider.
func WithStoreTracerProvider(tp trace.TracerProvider) Option {
	return store.WithStoreTracerProvider(tp)
}

// WithStoreMeterProvider sets the OTel MeterProvider for Store metrics
// (wrkflw_store_duration_seconds histogram). Default: the OTel global provider.
func WithStoreMeterProvider(mp metric.MeterProvider) Option {
	return store.WithStoreMeterProvider(mp)
}

// RelayOption configures a Relay returned by NewRelay / NewMySQLRelay.
//
// It is a facade-local function type (not a bare alias of store.RelayOption)
// because WithListenNotify needs the Postgres pool to build the pgx notifier,
// and a plain store.RelayOption cannot capture the pool. NewRelay folds every
// RelayOption into a relayConfig, then translates it into []store.RelayOption at
// construction time (injecting store.NewPgxNotifier(pool) when listenNotify is set).
type RelayOption func(*relayConfig)

// relayConfig accumulates the store-level relay options plus the Postgres-only
// listen-notify flag until NewRelay/NewMySQLRelay assemble the final relay.
type relayConfig struct {
	opts         []store.RelayOption
	listenNotify bool
}

// storeRelayOption lifts a store.RelayOption into the facade RelayOption type.
func storeRelayOption(o store.RelayOption) RelayOption {
	return func(c *relayConfig) { c.opts = append(c.opts, o) }
}

// WithListenNotify makes the relay LISTEN on wrkflw_outbox and drain on each
// NOTIFY (emitted by a Store configured with WithOutboxNotify), keeping the poll
// interval as a fallback (ADR-0022). Opt-in; default off. Postgres-only — MySQL
// has no LISTEN/NOTIFY and its relay is poll-only.
func WithListenNotify() RelayOption {
	return func(c *relayConfig) { c.listenNotify = true }
}

// Re-exported sentinel errors so consumers can do errors.Is(err, persistence.ErrInstanceNotFound)
// without importing the runtime or internal packages.
var (
	// ErrInstanceNotFound is returned by Store.Load when no instance exists for the id.
	ErrInstanceNotFound = runtime.ErrInstanceNotFound

	// ErrConcurrentUpdate is returned by Store.Commit when the expected token is stale.
	ErrConcurrentUpdate = runtime.ErrConcurrentUpdate
)

// Compile-time checks: the neutral store concrete types must satisfy the public
// interfaces so the facade constructors can return them.
var (
	_ Store                  = (*store.Store)(nil)
	_ DefinitionStore        = (*store.DefinitionStore)(nil)
	_ Relay                  = (*store.Relay)(nil)
	_ runtime.InstanceLister = (*store.Lister)(nil)
	_ runtime.CallLinkStore  = (*store.CallLinkStore)(nil)
	_ runtime.TimerStore     = (*store.TimerStore)(nil)
	_ runtime.ChainLinkStore = (*store.ChainLinkStore)(nil)
)

// ErrInstanceExists is returned by Store.Create when an instance id already
// exists (re-exported so consumers can errors.Is without importing runtime).
var ErrInstanceExists = runtime.ErrInstanceExists

// OpenPostgres constructs a Postgres-backed runtime.Store + JournalReader over pool.
//
// The returned Store satisfies both runtime.Store and runtime.JournalReader.
// Migrate must be called before OpenPostgres so the required tables exist.
//
// Example:
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	persistence.Migrate(ctx, pool)
//	store, _ := persistence.OpenPostgres(ctx, pool, persistence.WithHistoryCap(50))
//	runner := runtime.NewRunner(nil, store)
func OpenPostgres(ctx context.Context, pool *pgxpool.Pool, opts ...Option) (Store, error) {
	q, err := database.From(pool)
	if err != nil {
		return nil, err
	}
	if err := database.ProbeUTC(ctx, q, database.Postgres); err != nil {
		return nil, err
	}
	return store.New(pool, dialect.NewPostgres(), opts...), nil
}

// Migrate applies the embedded schema migrations to pool. It is idempotent:
// goose's version table ensures re-runs are no-ops.
//
// Migrate is intended to be called explicitly by the consumer during application
// startup — it is never auto-invoked on import.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return store.MigratePostgres(ctx, pool)
}

// NewDefinitionStore constructs the durable Postgres-backed definition store.
// It satisfies runtime.DefinitionRegistry via its Lookup method.
//
// Use this together with NewCachingDefinitionRegistry to cache hot definitions.
func NewDefinitionStore(pool *pgxpool.Pool) DefinitionStore {
	return store.NewDefinitionStore(pool, dialect.NewPostgres())
}

// NewCachingDefinitionRegistry wraps backing with a TTL-bounded, single-flight
// read-through cache. ttl is the maximum age of a cached definition.
// opts are forwarded to runtime.NewCachingDefinitionRegistry; use
// runtime.WithCachingDefinitionRegistryClock to inject a fake clock in tests.
//
// Definitions are immutable per (defID, version), so caching without invalidation
// is safe. The only eviction mechanism is TTL expiry.
//
// Example:
//
//	cached := persistence.NewCachingDefinitionRegistry(ds, 5*time.Minute)
//	// inject a fake clock in tests:
//	cached := persistence.NewCachingDefinitionRegistry(ds, 5*time.Minute,
//	    runtime.WithCachingDefinitionRegistryClock(fakeClock))
func NewCachingDefinitionRegistry(backing runtime.DefinitionRegistry, ttl time.Duration, opts ...runtime.CachingDefinitionRegistryOption) (*runtime.CachingDefinitionRegistry, error) {
	return runtime.NewCachingDefinitionRegistry(backing, ttl, opts...)
}

// NewRelay constructs an outbox relay over pool that publishes each event via pub.
//
// Call relay.Run(ctx) in a goroutine to start continuous polling, or call
// relay.DrainOnce(ctx) to drain a single batch synchronously.
//
// Available options: persistence.WithPollInterval, persistence.WithBatchSize,
// persistence.WithRelayClock, persistence.WithMaxDeliveryAttempts, and
// persistence.WithRelayBackoff. The relay isolates publish failures per row
// (a poison event never blocks healthy peers) and quarantines a row to a
// dead-letter status after MaxDeliveryAttempts (ADR-0017).
func NewRelay(pool *pgxpool.Pool, pub runtime.Publisher, opts ...RelayOption) Relay {
	var cfg relayConfig
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.listenNotify {
		// WithListenNotify: wire the pgx notifier so Run wakes on NOTIFY. The pool
		// is only available here (not in the option), so the notifier is injected
		// at construction time rather than as a plain store.RelayOption.
		cfg.opts = append(cfg.opts, store.WithRelayNotifier(store.NewPgxNotifier(pool)))
	}
	return store.NewRelay(pool, dialect.NewPostgres(), pub, cfg.opts...)
}

// WithPollInterval sets the interval between DrainOnce calls in Relay.Run.
// Default: 1s.
func WithPollInterval(d time.Duration) RelayOption {
	return storeRelayOption(store.WithRelayPollInterval(d))
}

// WithBatchSize sets the maximum number of outbox rows claimed per DrainOnce call.
// Default: 100.
func WithBatchSize(n int) RelayOption {
	return storeRelayOption(store.WithRelayBatchSize(n))
}

// WithRelayClock sets the clock the relay uses to stamp published_at /
// next_attempt_at and to evaluate which rows are due. Default: clock.System().
func WithRelayClock(clk clock.Clock) RelayOption {
	return storeRelayOption(store.WithRelayClock(clk))
}

// WithMaxDeliveryAttempts sets how many failed publish attempts a row tolerates
// before it is quarantined to a dead-letter status. Default: 10.
func WithMaxDeliveryAttempts(n int) RelayOption {
	return storeRelayOption(store.WithRelayMaxDeliveryAttempts(n))
}

// WithRelayBackoff sets the base and maximum interval of the capped exponential
// backoff applied to a row's next retry after a failed publish.
// Defaults: base 1s, max 1m.
func WithRelayBackoff(base, maxInterval time.Duration) RelayOption {
	return storeRelayOption(store.WithRelayBackoff(base, maxInterval))
}

// WithRelayLogger sets the structured logger used by the relay for drain logs.
// Default: slog.Default().
func WithRelayLogger(l *slog.Logger) RelayOption {
	return storeRelayOption(store.WithRelayLogger(l))
}

// WithRelayTracerProvider sets the OTel TracerProvider for relay batch spans.
// Default: the OTel global provider.
func WithRelayTracerProvider(tp trace.TracerProvider) RelayOption {
	return storeRelayOption(store.WithRelayTracerProvider(tp))
}

// WithRelayMeterProvider sets the OTel MeterProvider for relay metrics.
// Default: the OTel global provider.
func WithRelayMeterProvider(mp metric.MeterProvider) RelayOption {
	return storeRelayOption(store.WithRelayMeterProvider(mp))
}

// NewLister constructs the Postgres-backed runtime.InstanceLister for
// admin-list and monitoring use-cases. It executes a keyset-cursor-paginated
// query over wrkflw_instances and projects only the columns in
// runtime.InstanceSummary (no full snapshot read).
//
// Migrate must have been applied before the first call to List.
//
// Example:
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	persistence.Migrate(ctx, pool)
//	lister := persistence.NewLister(pool)
//	page, err := lister.List(ctx, runtime.InstanceFilter{Limit: 20})
func NewLister(pool *pgxpool.Pool) runtime.InstanceLister {
	return store.NewLister(pool, dialect.NewPostgres())
}

// NewAdvisoryLockOwnership constructs a multi-process [runtime.Ownership]
// backed by Postgres session advisory locks (ADR-0020), for use with
// [runtime.NewCachingStore] across multiple replicas sharing one database.
//
// It holds a dedicated pool connection for its lifetime; close the returned
// [io.Closer] at shutdown to release every held lock and return the connection.
//
// When used with a [runtime.CachingStore], always relinquish ownership through
// [runtime.CachingStore.Release] (not the bare [runtime.Ownership.Release]), so
// the cache evicts the instance's state on hand-off and a re-acquiring process
// does not serve a stale cached entry.
//
// Example:
//
//	owner, closer, _ := persistence.NewAdvisoryLockOwnership(ctx, pool)
//	defer closer.Close()
//	store, _ := persistence.OpenPostgres(ctx, pool)
//	cachingStore := runtime.NewCachingStore(store, owner)
func NewAdvisoryLockOwnership(ctx context.Context, pool *pgxpool.Pool) (runtime.Ownership, io.Closer, error) {
	o, err := store.NewPostgresOwnership(ctx, pool)
	if err != nil {
		return nil, nil, err
	}
	return o, o, nil
}

// CallLinkOption configures a CallLinkStore returned by NewCallLinkStore
// (alias of the neutral store.CallLinkOption). The same underlying type backs
// NewMySQLCallLinkStore's MySQLCallLinkOption after store unification.
type CallLinkOption = store.CallLinkOption

// WithCallLinkLease configures opt-in lease-based multi-replica exclusivity on
// the CallLinkStore (ADR-0031). When ttl > 0, ClaimPending stamps claimed_at /
// claimed_by on each row, hiding it from concurrent replicas until the lease
// expires. When ttl <= 0 (the default), the original plain SELECT is used
// unchanged (backward-compatible).
func WithCallLinkLease(owner string, ttl time.Duration) CallLinkOption {
	return store.WithCallLinkLease(owner, ttl)
}

// WithCallLinkClock sets the clock the CallLinkStore uses for lease timestamps.
// Default: clock.System(). Inject a fake clock in tests for deterministic
// behaviour (ADR-0003, ADR-0031).
func WithCallLinkClock(clk clock.Clock) CallLinkOption {
	return store.WithCallLinkClock(clk)
}

// NewCallLinkStore constructs the Postgres-backed runtime.CallLinkStore (read/claim
// side). It provides ClaimPending, MarkNotified, and LookupChild over the
// wrkflw_call_links table. The write side is fused into Store.Create /
// Store.Commit (ADR-0025); use OpenPostgres for that.
//
// Pass [WithCallLinkLease] and [WithCallLinkClock] to opt in to lease-based
// multi-replica exclusivity (ADR-0031). Existing zero-option call sites compile
// unchanged.
//
// Migrate must have been applied before the first call to any method.
//
// Example:
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	persistence.Migrate(ctx, pool)
//	cls := persistence.NewCallLinkStore(pool,
//	    persistence.WithCallLinkLease("replica-1", 30*time.Second),
//	)
//	pending, err := cls.ClaimPending(ctx, 100)
func NewCallLinkStore(pool *pgxpool.Pool, opts ...CallLinkOption) runtime.CallLinkStore {
	return store.NewCallLinkStore(pool, dialect.NewPostgres(), opts...)
}

// NewTimerStore returns a runtime.TimerStore backed by Postgres, for
// Runner.RehydrateTimers. The pool must already have migrations applied.
//
// Example:
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	persistence.Migrate(ctx, pool)
//	ts := persistence.NewTimerStore(pool)
//	armed, err := ts.ListArmed(ctx)
func NewTimerStore(pool *pgxpool.Pool) runtime.TimerStore {
	return store.NewTimerStore(pool, dialect.NewPostgres())
}

// NewChainLinkStore constructs the Postgres-backed runtime.ChainLinkStore for
// process-instance chaining lineage (ADR-0045): Record persists one
// predecessor->successor hop (a unique (predecessor, outcome) is the
// exactly-once backstop), LookupBySuccessor and ListByPredecessor serve
// ancestry/audit queries. Migrate must have been applied before the first call.
//
// Wire it into a runtime.Chainer via runtime.WithChainLinks:
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	persistence.Migrate(ctx, pool)
//	links := persistence.NewChainLinkStore(pool)
//	chainer := runtime.NewChainer(runner, policy, runtime.WithChainLinks(links))
func NewChainLinkStore(pool *pgxpool.Pool) runtime.ChainLinkStore {
	return store.NewChainLinkStore(pool, dialect.NewPostgres())
}

// NewCallNotifier builds a durable call-activity notifier over pool: it claims
// terminal call links and resumes parked parents (SubInstanceCompleted/Failed)
// idempotently. Run it in a goroutine (notifier.Run) or drain manually (DrainOnce).
//
// opts are forwarded to runtime.NewCallNotifier; use runtime.WithCallNotifierClock
// to inject a fake clock in tests.
//
// For lease-based multi-replica exclusivity (ADR-0031), build the CallLinkStore
// explicitly via [NewCallLinkStore] with [WithCallLinkLease] and pass it to
// [runtime.NewCallNotifier] directly:
//
//	cls := persistence.NewCallLinkStore(pool,
//	    persistence.WithCallLinkLease("replica-1", 30*time.Second),
//	)
//	notifier := runtime.NewCallNotifier(cls, deliver, reg)
//
// Typical wiring:
//
//	notifier := persistence.NewCallNotifier(pool,
//	    runtime.CallDeliverFunc(func(ctx context.Context, def *model.ProcessDefinition, id string, trg engine.Trigger) error {
//	        _, err := runner.Deliver(ctx, def, id, trg)
//	        return err
//	    }),
//	    reg,
//	)
//	go notifier.Run(ctx)
//
// reg MUST resolve every parent definition under the exact key "<defID>:<version>";
// an unresolvable parent leaves its parked parent unresumed (see runtime.NewCallNotifier).
func NewCallNotifier(pool *pgxpool.Pool, deliver runtime.CallDeliverFunc, reg runtime.DefinitionRegistry, opts ...runtime.CallNotifierOption) *runtime.CallNotifier {
	return runtime.NewCallNotifier(store.NewCallLinkStore(pool, dialect.NewPostgres()), deliver, reg, opts...)
}
