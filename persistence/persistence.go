// Package persistence is the consumer-facing façade over the internal
// Postgres-backed persistence implementation (ADR-0008). It exposes
// constructors, stable port/interface types, options, and re-exported sentinels
// so library consumers never have to import internal/persistence/postgres directly.
//
// # Usage
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	if err := persistence.Migrate(ctx, pool); err != nil { ... }
//
//	store, err := persistence.OpenPostgres(ctx, pool)
//	runner := runtime.NewRunner(cat, clock.System(), store)
//
// # Relay (transactional outbox drain)
//
//	relay := persistence.NewRelay(pool, myPublisher)
//	go relay.Run(ctx)
package persistence

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
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
	Lookup(defRef string) (*model.ProcessDefinition, error)
}

// Relay is the stable public interface for the transactional outbox drain.
// It is satisfied by the internal Relay implementation. NewRelay returns this
// interface so consumers are not bound to the concrete *postgres.Relay.
type Relay interface {
	// Run drains the outbox on each poll tick until ctx is cancelled.
	// Non-cancel errors from DrainOnce terminate the loop (fail-fast).
	Run(ctx context.Context) error
	// DrainOnce claims and publishes one batch of outbox rows synchronously.
	// Returns the number of rows published.
	DrainOnce(ctx context.Context) (int, error)
}

// Publisher is the broker-agnostic outbox publisher alias (same as runtime.Publisher).
type Publisher = runtime.Publisher

// RelayOption configures a Relay (alias of postgres.RelayOption).
type RelayOption = postgres.RelayOption

// Re-exported sentinel errors so consumers can do errors.Is(err, persistence.ErrInstanceNotFound)
// without importing the runtime or internal packages.
var (
	// ErrInstanceNotFound is returned by Store.Load when no instance exists for the id.
	ErrInstanceNotFound = runtime.ErrInstanceNotFound

	// ErrConcurrentUpdate is returned by Store.Commit when the expected token is stale.
	ErrConcurrentUpdate = runtime.ErrConcurrentUpdate
)

// Compile-time checks: internal concrete types must satisfy the public interfaces.
var (
	_ Store                 = (*postgres.Store)(nil)
	_ DefinitionStore       = (*postgres.DefinitionStore)(nil)
	_ Relay                 = (*postgres.Relay)(nil)
	_ runtime.InstanceLister = (*postgres.Lister)(nil)
)

// OpenPostgres constructs a Postgres-backed runtime.Store + JournalReader over pool.
//
// The returned Store satisfies both runtime.Store and runtime.JournalReader.
// Migrate must be called before OpenPostgres so the required tables exist.
//
// Example:
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	persistence.Migrate(ctx, pool)
//	store, _ := persistence.OpenPostgres(ctx, pool)
//	runner := runtime.NewRunner(nil, clock.System(), store)
func OpenPostgres(_ context.Context, pool *pgxpool.Pool) (Store, error) {
	return postgres.NewStore(pool), nil
}

// Migrate applies the embedded schema migrations to pool. It is idempotent:
// goose's version table ensures re-runs are no-ops.
//
// Migrate is intended to be called explicitly by the consumer during application
// startup — it is never auto-invoked on import.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return postgres.Migrate(ctx, pool)
}

// NewDefinitionStore constructs the durable Postgres-backed definition store.
// It satisfies runtime.DefinitionRegistry via its Lookup method.
//
// Use this together with NewCachingDefinitionRegistry to cache hot definitions.
func NewDefinitionStore(pool *pgxpool.Pool) DefinitionStore {
	return postgres.NewDefinitionStore(pool)
}

// NewCachingDefinitionRegistry wraps backing with a TTL-bounded, single-flight
// read-through cache. ttl is the maximum age of a cached definition; clk is
// the time source (use clock.System() in production, a fake clock in tests).
//
// Definitions are immutable per (defID, version), so caching without invalidation
// is safe. The only eviction mechanism is TTL expiry.
func NewCachingDefinitionRegistry(backing runtime.DefinitionRegistry, ttl time.Duration, clk clock.Clock) *runtime.CachingDefinitionRegistry {
	return runtime.NewCachingDefinitionRegistry(backing, ttl, clk)
}

// NewRelay constructs an outbox relay over pool that publishes each event via pub.
//
// Call relay.Run(ctx) in a goroutine to start continuous polling, or call
// relay.DrainOnce(ctx) to drain a single batch synchronously.
//
// Available options: persistence.WithPollInterval, persistence.WithBatchSize.
func NewRelay(pool *pgxpool.Pool, pub runtime.Publisher, opts ...RelayOption) Relay {
	return postgres.NewRelay(pool, pub, opts...)
}

// WithPollInterval sets the interval between DrainOnce calls in Relay.Run.
// Default: 1s.
func WithPollInterval(d time.Duration) RelayOption {
	return postgres.WithPollInterval(d)
}

// WithBatchSize sets the maximum number of outbox rows claimed per DrainOnce call.
// Default: 100.
func WithBatchSize(n int) RelayOption {
	return postgres.WithBatchSize(n)
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
	return postgres.NewLister(pool)
}
