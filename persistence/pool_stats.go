package persistence

// pool_stats.go — optional OTel collectors for database connection-pool
// saturation. The consumer owns the pool handle (OpenPostgres/OpenMySQL/OpenSQLite
// all take an already-open handle), so these collectors are a convenience over
// the stats the consumer could read itself — opt-in by construction, no
// background goroutine, and no change to pool ownership.

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/metric"

	"github.com/kartaladev/wrkflw/internal/observability"
)

const poolStatsInstrumentationName = "github.com/kartaladev/wrkflw/persistence/poolstats"

// poolStatsConfig holds the resolved observability options for a
// [PoolStatsCollector]. The internal observability options are kept in an
// unexported field so no internal type appears in a public signature.
type poolStatsConfig struct {
	obsOpts []observability.Option
}

// PoolStatsOption configures a [PoolStatsCollector] built by
// [NewPoolStatsCollector] or [NewPostgresPoolStatsCollector].
type PoolStatsOption func(*poolStatsConfig)

// WithPoolStatsMeterProvider sets the OTel MeterProvider the pool instruments are
// registered on. Default: the OTel global provider. A nil value is ignored.
func WithPoolStatsMeterProvider(mp metric.MeterProvider) PoolStatsOption {
	return func(c *poolStatsConfig) {
		if mp != nil {
			c.obsOpts = append(c.obsOpts, observability.WithMeterProvider(mp))
		}
	}
}

// WithPoolStatsLogger sets the logger used to report instrument-registration
// failures. Default: slog.Default(). A nil value is ignored.
func WithPoolStatsLogger(l *slog.Logger) PoolStatsOption {
	return func(c *poolStatsConfig) {
		if l != nil {
			c.obsOpts = append(c.obsOpts, observability.WithLogger(l))
		}
	}
}

// poolStats is the backend-neutral snapshot the collector observes. Both
// *sql.DB.Stats() and *pgxpool.Pool.Stat() project onto it.
type poolStats struct {
	inUse   int64
	idle    int64
	maxOpen int64
	waits   int64
}

// PoolStatsCollector is an OTel collector for database connection-pool
// saturation. It registers three observable gauges and one observable counter,
// and reads the pool's stats at each OTel collection cycle (no background
// goroutine):
//
//   - wrkflw_db_pool_in_use     — connections currently checked out
//   - wrkflw_db_pool_idle       — connections currently idle in the pool
//   - wrkflw_db_pool_max_open   — the pool's configured ceiling (the saturation denominator)
//   - wrkflw_db_pool_waits_total — cumulative acquires that had to wait for a free connection
//
// Saturation is in_use / max_open; a rising waits_total is the signal that the
// ceiling is already being hit.
//
// Construct it with [NewPoolStatsCollector] (any *sql.DB — MySQL or SQLite) or
// [NewPostgresPoolStatsCollector] (pgx). The zero value is not useful.
type PoolStatsCollector struct {
	tel  observability.Telemetry
	read func() poolStats
}

// NewPoolStatsCollector returns a [PoolStatsCollector] over a *sql.DB (MySQL or
// SQLite), reading [database/sql.DB.Stats] once per collection cycle.
//
// The pool stays owned by the caller: the collector only reads statistics and
// never opens, closes, or reconfigures a connection. A nil db is accepted and
// yields a collector that observes zeroes, so a mis-wired probe cannot panic a
// scrape.
//
// Example:
//
//	db, _ := sql.Open("sqlite", dsn)
//	db.SetMaxOpenConns(1)
//	persistence.NewPoolStatsCollector(db, persistence.WithPoolStatsMeterProvider(mp))
func NewPoolStatsCollector(db *sql.DB, opts ...PoolStatsOption) *PoolStatsCollector {
	return newPoolStatsCollector(func() poolStats {
		if db == nil {
			return poolStats{}
		}
		s := db.Stats()
		return poolStats{
			inUse:   int64(s.InUse),
			idle:    int64(s.Idle),
			maxOpen: int64(s.MaxOpenConnections),
			waits:   s.WaitCount,
		}
	}, opts...)
}

// NewPostgresPoolStatsCollector returns a [PoolStatsCollector] over a
// *pgxpool.Pool, reading [pgxpool.Pool.Stat] once per collection cycle. It is
// the Postgres analogue of [NewPoolStatsCollector] and registers the same four
// instruments, so a dashboard is backend-independent.
//
// pgx's EmptyAcquireCount is reported as wrkflw_db_pool_waits_total: it counts
// acquires that found no idle connection and had to wait, which is the same
// saturation signal as database/sql's WaitCount.
//
// A nil pool is accepted and yields a collector that observes zeroes.
func NewPostgresPoolStatsCollector(pool *pgxpool.Pool, opts ...PoolStatsOption) *PoolStatsCollector {
	return newPoolStatsCollector(func() poolStats {
		if pool == nil {
			return poolStats{}
		}
		s := pool.Stat()
		return poolStats{
			inUse:   int64(s.AcquiredConns()),
			idle:    int64(s.IdleConns()),
			maxOpen: int64(s.MaxConns()),
			waits:   s.EmptyAcquireCount(),
		}
	}, opts...)
}

// newPoolStatsCollector registers the four instruments and the shared callback
// that projects read() onto them.
func newPoolStatsCollector(read func() poolStats, opts ...PoolStatsOption) *PoolStatsCollector {
	var cfg poolStatsConfig
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	tel := observability.New(poolStatsInstrumentationName, cfg.obsOpts...)

	c := &PoolStatsCollector{tel: tel, read: read}

	inUse, err := tel.Meter.Int64ObservableGauge(
		"wrkflw_db_pool_in_use",
		metric.WithDescription("Database connections currently checked out of the pool."),
	)
	if err != nil {
		tel.Logger.Error("failed to register wrkflw_db_pool_in_use gauge", slog.String("err", err.Error()))
		return c
	}

	idle, err := tel.Meter.Int64ObservableGauge(
		"wrkflw_db_pool_idle",
		metric.WithDescription("Database connections currently idle in the pool."),
	)
	if err != nil {
		tel.Logger.Error("failed to register wrkflw_db_pool_idle gauge", slog.String("err", err.Error()))
		return c
	}

	maxOpen, err := tel.Meter.Int64ObservableGauge(
		"wrkflw_db_pool_max_open",
		metric.WithDescription("Configured maximum number of open connections; the saturation denominator."),
	)
	if err != nil {
		tel.Logger.Error("failed to register wrkflw_db_pool_max_open gauge", slog.String("err", err.Error()))
		return c
	}

	waits, err := tel.Meter.Int64ObservableCounter(
		"wrkflw_db_pool_waits_total",
		metric.WithDescription("Cumulative connection acquisitions that had to wait for a free connection."),
	)
	if err != nil {
		tel.Logger.Error("failed to register wrkflw_db_pool_waits_total counter", slog.String("err", err.Error()))
		return c
	}

	_, regErr := tel.Meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		s := c.read()
		o.ObserveInt64(inUse, s.inUse)
		o.ObserveInt64(idle, s.idle)
		o.ObserveInt64(maxOpen, s.maxOpen)
		o.ObserveInt64(waits, s.waits)
		return nil
	}, inUse, idle, maxOpen, waits)
	if regErr != nil {
		tel.Logger.Error("PoolStatsCollector: failed to register callback", slog.String("err", regErr.Error()))
	}

	return c
}
