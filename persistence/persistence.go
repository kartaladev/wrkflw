// Package persistence is the consumer-facing façade over the internal
// Postgres-backed persistence implementation (ADR-0008). It exposes
// constructors, options, and re-exported sentinels so library consumers
// never have to import internal/persistence/postgres directly.
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
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Re-exported type aliases — consumers use these names; internal types never leak.

// Store is the Postgres-backed runtime.Store + JournalReader. The concrete type
// is *postgres.Store; it is aliased here so the public API does not mention
// internal package paths.
type Store = postgres.Store

// Publisher is the broker-agnostic outbox publisher alias (same as runtime.Publisher).
type Publisher = runtime.Publisher

// Relay drains the transactional outbox to a Publisher (alias of postgres.Relay).
type Relay = postgres.Relay

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

// config holds optional OpenPostgres configuration. Reserved for future options
// (e.g. custom JSON marshal/unmarshal, metrics hooks).
type config struct{}

// Option configures the Postgres store. Additional options will be added as the
// library evolves; the zero-value config is always a safe default.
type Option func(*config)

// OpenPostgres constructs a Postgres-backed runtime.Store + JournalReader over pool.
//
// The returned *Store satisfies both runtime.Store and runtime.JournalReader.
// Migrate must be called before OpenPostgres so the required tables exist.
//
// Example:
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	persistence.Migrate(ctx, pool)
//	store, _ := persistence.OpenPostgres(ctx, pool)
//	runner := runtime.NewRunner(nil, clock.System(), store)
func OpenPostgres(_ context.Context, pool *pgxpool.Pool, opts ...Option) (*Store, error) {
	var c config
	for _, o := range opts {
		o(&c)
	}
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
func NewDefinitionStore(pool *pgxpool.Pool) *postgres.DefinitionStore {
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
func NewRelay(pool *pgxpool.Pool, pub runtime.Publisher, opts ...RelayOption) *Relay {
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
