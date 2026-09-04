package persistence

// options.go — the facade-owned functional-option surface shared by the
// Postgres, MySQL and SQLite backends.
//
// Every option type here is DEFINED in this package rather than aliased to the
// module-internal store option it configures. An alias compiles, but it
// publishes an internal type as the public contract: `go doc persistence.Option`
// renders `type Option = store.Option`, the public signature then moves
// whenever the internal one does, and — the concrete cost this file was written
// to repay — an internal option that nobody remembered to forward is
// unreachable off-module while still shipping a doc comment telling consumers
// to use it. The four clock options shipped in exactly that state:
// store.WithStoreClock's own comment said "inject a clockwork.FakeClock in
// tests", and no consumer could name it.
//
// This is the package-owned option shape the rest of the module already uses
// (runtime.Option, runtime/monitor.Option, calllink.CallNotifierOption):
// nothing under internal/ appears in an exported signature. The per-family
// config structs mirror relayConfig in persistence.go, including its
// no-nil-guard fold: a nil option panics on apply, exactly as it did when these
// types were aliases.

import (
	"log/slog"
	"time"

	"github.com/jonboulle/clockwork"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/kartaladev/wrkflw/internal/persistence/store"
)

// Option configures the InstanceStore returned by [OpenPostgres], [OpenMySQL]
// and [OpenSQLite]. The three backends share one option surface.
type Option func(*storeConfig)

// storeConfig stages the neutral store options an InstanceStore was built with.
type storeConfig struct{ opts []store.Option }

// storeOption lifts a neutral store.Option into the facade Option type.
func storeOption(o store.Option) Option {
	return func(c *storeConfig) { c.opts = append(c.opts, o) }
}

// buildStoreOptions folds opts into the neutral store option slice.
func buildStoreOptions(opts []Option) []store.Option {
	var c storeConfig
	for _, o := range opts {
		o(&c)
	}
	return c.opts
}

// WithHistoryCap bounds the inline instance History persisted in the snapshot
// to every open visit plus at most n most-recent closed visits.
// Unset / n <= 0 keeps full inline history (current behavior). The journal
// table remains the complete audit source.
func WithHistoryCap(n int) Option { return storeOption(store.WithHistoryCap(n)) }

// WithOutboxNotify makes the Store emit a transactional NOTIFY wrkflw_outbox
// when a step inserts outbox rows, so a relay started with WithListenNotify
// drains with sub-poll-interval latency. Opt-in; default off.
// Only Postgres emits a NOTIFY; MySQL and SQLite silently skip it.
func WithOutboxNotify() Option { return storeOption(store.WithOutboxNotify()) }

// WithStoreLogger sets the structured logger used by the Store for operation logs.
// Default: [slog.Default]. A nil logger is ignored.
func WithStoreLogger(l *slog.Logger) Option { return storeOption(store.WithStoreLogger(l)) }

// WithStoreTracerProvider sets the OTel TracerProvider for Store operation spans
// (wrkflw.store.load, wrkflw.store.commit). Default: the OTel global provider.
// A nil provider is ignored.
func WithStoreTracerProvider(tp trace.TracerProvider) Option {
	return storeOption(store.WithStoreTracerProvider(tp))
}

// WithStoreMeterProvider sets the OTel MeterProvider for Store metrics
// (wrkflw_store_duration_seconds histogram). Default: the OTel global provider.
// A nil provider is ignored.
func WithStoreMeterProvider(mp metric.MeterProvider) Option {
	return storeOption(store.WithStoreMeterProvider(mp))
}

// WithStoreClock sets the time source for the wall-clock timestamps the Store
// persists — today wrkflw_instances.updated_at on Create and Commit.
// Default: [clockwork.NewRealClock]. A nil clock is ignored.
//
// ⚠ In tests use [clockwork.NewFakeClockAt] with an explicit instant, not
// clockwork.NewFakeClock(): the latter seeds itself from the wall clock, so a
// timestamp assertion written against it passes even against a store that
// never consults the injected clock.
func WithStoreClock(clk clockwork.Clock) Option {
	return storeOption(store.WithStoreClock(clk))
}

// CallLinkOption configures a CallLinkStore returned by [NewCallLinkStore],
// [NewMySQLCallLinkStore] and [NewSQLiteCallLinkStore]. The three backends
// share one option surface.
type CallLinkOption func(*callLinkConfig)

// callLinkConfig stages the neutral store options a CallLinkStore was built with.
type callLinkConfig struct{ opts []store.CallLinkOption }

// storeCallLinkOption lifts a neutral store.CallLinkOption into the facade type.
func storeCallLinkOption(o store.CallLinkOption) CallLinkOption {
	return func(c *callLinkConfig) { c.opts = append(c.opts, o) }
}

// buildCallLinkOptions folds opts into the neutral store option slice.
func buildCallLinkOptions(opts []CallLinkOption) []store.CallLinkOption {
	var c callLinkConfig
	for _, o := range opts {
		o(&c)
	}
	return c.opts
}

// WithCallLinkLease configures opt-in lease-based multi-replica exclusivity on
// the CallLinkStore. When ttl > 0, ClaimPending stamps claimed_at /
// claimed_by on each row, hiding it from concurrent replicas until the lease
// expires. When ttl <= 0 (the default), the original plain SELECT is used
// unchanged (backward-compatible).
func WithCallLinkLease(owner string, ttl time.Duration) CallLinkOption {
	return storeCallLinkOption(store.WithCallLinkLease(owner, ttl))
}

// WithCallLinkClock sets the clock the CallLinkStore uses for lease timestamps.
// Default: [clockwork.NewRealClock]. A nil clock is ignored. Inject a fake clock
// in tests for deterministic behaviour — see the
// [clockwork.NewFakeClockAt] warning on [WithStoreClock].
func WithCallLinkClock(clk clockwork.Clock) CallLinkOption {
	return storeCallLinkOption(store.WithCallLinkClock(clk))
}

// DefinitionOption configures a [DefinitionStore] returned by
// [NewDefinitionStore], [NewMySQLDefinitionStore] and
// [NewSQLiteDefinitionStore]. The three backends share one option surface.
type DefinitionOption func(*definitionConfig)

// definitionConfig stages the neutral store options a DefinitionStore was built with.
type definitionConfig struct{ opts []store.DefinitionOption }

// buildDefinitionOptions folds opts into the neutral store option slice.
func buildDefinitionOptions(opts []DefinitionOption) []store.DefinitionOption {
	var c definitionConfig
	for _, o := range opts {
		o(&c)
	}
	return c.opts
}

// WithDefinitionClock sets the time source for the created_at stamp
// [DefinitionStore.PutDefinition] writes. Default:
// [clockwork.NewRealClock]. A nil clock is ignored. See the
// [clockwork.NewFakeClockAt] warning on [WithStoreClock].
func WithDefinitionClock(clk clockwork.Clock) DefinitionOption {
	return func(c *definitionConfig) {
		c.opts = append(c.opts, store.WithDefinitionClock(clk))
	}
}

// DeduperOption configures a [Deduper] returned by [NewDeduper],
// [NewMySQLDeduper] and [NewSQLiteDeduper]. The three backends share one
// option surface.
type DeduperOption func(*deduperConfig)

// deduperConfig stages the neutral store options a Deduper was built with.
type deduperConfig struct{ opts []store.DeduperOption }

// buildDeduperOptions folds opts into the neutral store option slice.
func buildDeduperOptions(opts []DeduperOption) []store.DeduperOption {
	var c deduperConfig
	for _, o := range opts {
		o(&c)
	}
	return c.opts
}

// WithDeduperClock sets the time source for the processed_at stamp
// [Deduper.Seen] writes. Default: [clockwork.NewRealClock]. A nil
// clock is ignored. [Deduper.Prune] is unaffected: its cutoff is supplied by
// the caller. See the [clockwork.NewFakeClockAt] warning on [WithStoreClock].
func WithDeduperClock(clk clockwork.Clock) DeduperOption {
	return func(c *deduperConfig) {
		c.opts = append(c.opts, store.WithDeduperClock(clk))
	}
}

// ChainLinkOption configures a chain-link store returned by
// [NewChainLinkStore], [NewMySQLChainLinkStore] and [NewSQLiteChainLinkStore].
// The three backends share one option surface.
type ChainLinkOption func(*chainLinkConfig)

// chainLinkConfig stages the neutral store options a ChainLinkStore was built with.
type chainLinkConfig struct{ opts []store.ChainLinkOption }

// buildChainLinkOptions folds opts into the neutral store option slice.
func buildChainLinkOptions(opts []ChainLinkOption) []store.ChainLinkOption {
	var c chainLinkConfig
	for _, o := range opts {
		o(&c)
	}
	return c.opts
}

// WithChainLinkClock sets the clock [kernel.ChainLinkStore.Record] falls back
// to when the supplied link carries a zero CreatedAt. Default:
// [clockwork.NewRealClock]. A nil clock is ignored. See the
// [clockwork.NewFakeClockAt] warning on [WithStoreClock].
func WithChainLinkClock(clk clockwork.Clock) ChainLinkOption {
	return func(c *chainLinkConfig) {
		c.opts = append(c.opts, store.WithChainLinkClock(clk))
	}
}
