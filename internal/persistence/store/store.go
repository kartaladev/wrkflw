package store

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/observability"
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
	notify  dialect.Notifier // optional LISTEN receive-side; nil by default

	// historyCap bounds the inline snapshot History (ADR-0021). <= 0 (default)
	// keeps full inline history; the wrkflw_journal table is always complete.
	historyCap int
	// emitNotify makes Create/Commit emit the dialect's NOTIFY wake statement
	// inside the committing transaction when the step produced outbox events and
	// the dialect supports native pub/sub (Postgres only). Default: false.
	emitNotify bool

	// staged telemetry option values; assembled into tel after all Options have
	// been applied in New.
	logOpt observability.Option
	tpOpt  observability.Option
	mpOpt  observability.Option

	tel           observability.Telemetry
	storeDuration metric.Float64Histogram
}

// Option is a functional option that configures a [Store] built by [New].
type Option func(*Store)

// WithNotifier injects the LISTEN receive-side capability. Only the
// (pgx, Postgres) combination provides a meaningful [dialect.Notifier];
// for MySQL and SQLite pass nil or omit this option.
func WithNotifier(n dialect.Notifier) Option {
	return func(s *Store) { s.notify = n }
}

// WithHistoryCap bounds the inline History retained in the snapshot to every
// open visit plus at most n most-recent closed visits (ADR-0021). n <= 0 (the
// default) keeps full inline history. The wrkflw_journal table is unaffected
// and remains the complete audit source.
func WithHistoryCap(n int) Option { return func(s *Store) { s.historyCap = n } }

// WithOutboxNotify makes Create/Commit emit the dialect's NOTIFY wake statement
// inside the committing transaction whenever the step inserted at least one
// outbox row, so a listening relay wakes immediately instead of waiting for its
// next poll tick. Only Postgres emits a statement (via
// [dialect.Dialect.NotifyStatement]); MySQL and SQLite silently skip it. Steps
// that produce no events emit no notification.
func WithOutboxNotify() Option { return func(s *Store) { s.emitNotify = true } }

// WithStoreLogger sets the structured logger used by the store for operation
// logs. A nil value is ignored and the default (slog.Default()) is kept.
func WithStoreLogger(l *slog.Logger) Option {
	return func(s *Store) { s.logOpt = observability.WithLogger(l) }
}

// WithStoreTracerProvider sets the OTel TracerProvider for store operation
// spans. A nil value is ignored and the OTel global provider is used. Use this
// to inject a test recorder or a real SDK provider from consumer wiring.
func WithStoreTracerProvider(tp trace.TracerProvider) Option {
	return func(s *Store) { s.tpOpt = observability.WithTracerProvider(tp) }
}

// WithStoreMeterProvider sets the OTel MeterProvider for the
// wrkflw_store_duration_seconds histogram and any future store metrics. A nil
// value is ignored and the OTel global provider is used.
func WithStoreMeterProvider(mp metric.MeterProvider) Option {
	return func(s *Store) { s.mpOpt = observability.WithMeterProvider(mp) }
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
	s.tel = observability.New(
		"github.com/zakyalvan/krtlwrkflw/persistence",
		filterNilOpts(s.logOpt, s.tpOpt, s.mpOpt)...,
	)
	s.storeDuration = s.tel.Float64Histogram(
		"wrkflw_store_duration_seconds",
		"Duration of persistence Store operations in seconds",
	)
	return s
}

// filterNilOpts strips nil [observability.Option] values so New does not pass
// nils into [observability.New].
func filterNilOpts(opts ...observability.Option) []observability.Option {
	out := make([]observability.Option, 0, len(opts))
	for _, o := range opts {
		if o != nil {
			out = append(out, o)
		}
	}
	return out
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
