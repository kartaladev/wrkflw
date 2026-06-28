package persistence

// mysql.go contains the consumer-facing façade over the MySQL persistence
// backend (internal/persistence/mysql). It mirrors the Postgres façade
// constructors: OpenMySQL, MigrateMySQL, NewMySQLTimerStore, NewMySQLRelay,
// and NewMySQLDeduper.
//
// MySQLOption is a distinct type from Option (which aliases postgres.StoreOption)
// because the two backends have incompatible concrete option function signatures.
// MySQL-specific option constructors (MySQLWith*) map 1:1 to internal/persistence/mysql
// option constructors, exactly as the Postgres façade option constructors map to
// internal/persistence/postgres option constructors.
//
// MySQLRelayOption is similarly distinct from RelayOption (which aliases
// postgres.RelayOption); use MySQLWith* constructors to configure it.
//
// MySQLDeduper is a separate interface from Deduper because the Postgres Deduper
// operates over pgx.Tx (a pgx-specific transaction type) while the MySQL Deduper
// operates over *sql.Tx (the standard library transaction type). They cannot share
// a single interface without coupling one backend to the other's driver.

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	_ "github.com/go-sql-driver/mysql" // register "mysql" driver
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/clock"
	mysqlstore "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// MySQLDeduper is the stable public interface for idempotent-consumer
// deduplication against a MySQL backend (ADR-0018). It is analogous to
// Deduper (which uses pgx.Tx) but operates over *sql.Tx so it remains
// driver-agnostic with respect to the standard library.
//
// NewMySQLDeduper returns this interface; consumers never need to import
// internal/persistence/mysql directly.
type MySQLDeduper interface {
	// Seen records (subscriber, messageID) within tx and reports whether this is
	// the FIRST time the pair was seen. firstTime==false means the message is a
	// duplicate and the caller should skip the side effect. Uses INSERT IGNORE so
	// concurrent inserts of the same pair resolve without error.
	Seen(ctx context.Context, tx *sql.Tx, subscriber, messageID string) (firstTime bool, err error)

	// Prune deletes all processed-message records with a processed_at strictly
	// before before. Returns the number of rows deleted.
	Prune(ctx context.Context, before time.Time) (int64, error)
}

// MySQLOption configures the MySQL Store returned by OpenMySQL.
// It is distinct from Option (which aliases postgres.StoreOption) because the
// MySQL and Postgres store implementations carry incompatible option types.
type MySQLOption = mysqlstore.StoreOption

// MySQLWithHistoryCap bounds the inline instance History persisted in the
// snapshot to every open visit plus at most n most-recent closed visits
// (ADR-0021). n <= 0 keeps full inline history. Mirrors WithHistoryCap for Postgres.
func MySQLWithHistoryCap(n int) MySQLOption { return mysqlstore.WithHistoryCap(n) }

// MySQLWithStoreLogger sets the structured logger used by the MySQL Store.
// Default: slog.Default(). Mirrors WithStoreLogger for Postgres.
func MySQLWithStoreLogger(l *slog.Logger) MySQLOption { return mysqlstore.WithStoreLogger(l) }

// MySQLWithStoreTracerProvider sets the OTel TracerProvider for MySQL Store
// operation spans. Default: the OTel global provider. Mirrors WithStoreTracerProvider.
func MySQLWithStoreTracerProvider(tp trace.TracerProvider) MySQLOption {
	return mysqlstore.WithStoreTracerProvider(tp)
}

// MySQLWithStoreMeterProvider sets the OTel MeterProvider for MySQL Store
// metrics. Default: the OTel global provider. Mirrors WithStoreMeterProvider.
func MySQLWithStoreMeterProvider(mp metric.MeterProvider) MySQLOption {
	return mysqlstore.WithStoreMeterProvider(mp)
}

// OpenMySQL constructs a MySQL-backed runtime.Store + JournalReader over db.
//
// The returned Store satisfies both runtime.Store and runtime.JournalReader,
// identical to the interface returned by OpenPostgres. MigrateMySQL must be
// called before OpenMySQL so the required tables exist (or use RunTestMySQL in
// tests which auto-migrates).
//
// Example:
//
//	db, _ := sql.Open("mysql", dsn)
//	persistence.MigrateMySQL(ctx, db)
//	store, _ := persistence.OpenMySQL(ctx, db, persistence.MySQLWithHistoryCap(50))
//	runner := runtime.NewRunner(nil, store)
func OpenMySQL(_ context.Context, db *sql.DB, opts ...MySQLOption) (Store, error) {
	return mysqlstore.NewStore(db, opts...), nil
}

// MigrateMySQL applies the embedded schema migrations to the MySQL database
// reachable through db. It is idempotent: goose's version table ensures
// re-runs are no-ops. Mirrors Migrate for Postgres.
//
// MigrateMySQL is intended to be called explicitly by the consumer during
// application startup — it is never auto-invoked on import.
func MigrateMySQL(ctx context.Context, db *sql.DB) error {
	return mysqlstore.Migrate(ctx, db)
}

// NewMySQLTimerStore returns a runtime.TimerStore backed by MySQL, for
// Runner.RehydrateTimers. The db must already have migrations applied.
// Mirrors NewTimerStore for Postgres.
//
// Example:
//
//	db, _ := sql.Open("mysql", dsn)
//	persistence.MigrateMySQL(ctx, db)
//	ts := persistence.NewMySQLTimerStore(db)
//	armed, err := ts.ListArmed(ctx)
func NewMySQLTimerStore(db *sql.DB) runtime.TimerStore {
	return mysqlstore.NewTimerStore(db)
}

// MySQLRelayOption configures a MySQL Relay returned by NewMySQLRelay.
// It is distinct from RelayOption (which aliases postgres.RelayOption) because
// MySQL and Postgres relay implementations carry incompatible option types.
// MySQL has no LISTEN/NOTIFY; its relay is poll-only (no MySQLWithListenNotify).
type MySQLRelayOption = mysqlstore.RelayOption

// MySQLWithPollInterval sets the interval between DrainOnce calls in the MySQL
// Relay's Run loop. Default: 1s. Mirrors WithPollInterval for the Postgres relay.
func MySQLWithPollInterval(d time.Duration) MySQLRelayOption {
	return mysqlstore.WithPollInterval(d)
}

// MySQLWithBatchSize sets the maximum number of outbox rows claimed per
// DrainOnce call. Default: 100. Mirrors WithBatchSize for the Postgres relay.
func MySQLWithBatchSize(n int) MySQLRelayOption {
	return mysqlstore.WithBatchSize(n)
}

// MySQLWithMaxDeliveryAttempts sets how many failed publish attempts a row
// tolerates before it is quarantined to status 'dead'. Default: 10.
// Mirrors WithMaxDeliveryAttempts for the Postgres relay.
func MySQLWithMaxDeliveryAttempts(n int) MySQLRelayOption {
	return mysqlstore.WithMaxDeliveryAttempts(n)
}

// MySQLWithRelayBackoff sets the base and maximum interval of the capped
// exponential backoff applied to a row's next_attempt_at after a failed publish.
// Defaults: base 1s, max 1m. Mirrors WithRelayBackoff for the Postgres relay.
func MySQLWithRelayBackoff(base, maxInterval time.Duration) MySQLRelayOption {
	return mysqlstore.WithRelayBackoff(base, maxInterval)
}

// MySQLWithRelayClock sets the clock the MySQL relay uses to stamp published_at
// / next_attempt_at and to evaluate which rows are due. Default: clock.System().
// Inject a fake clock in tests for deterministic behaviour (ADR-0003).
// Mirrors WithRelayClock for the Postgres relay.
func MySQLWithRelayClock(clk clock.Clock) MySQLRelayOption {
	return mysqlstore.WithRelayClock(clk)
}

// MySQLWithRelayLogger sets the structured logger used by the MySQL relay for
// drain logs. Default: slog.Default(). Mirrors WithRelayLogger for the Postgres relay.
func MySQLWithRelayLogger(l *slog.Logger) MySQLRelayOption {
	return mysqlstore.WithRelayLogger(l)
}

// MySQLWithRelayTracerProvider sets the OTel TracerProvider for MySQL relay batch
// spans. Default: the OTel global provider. Mirrors WithRelayTracerProvider.
func MySQLWithRelayTracerProvider(tp trace.TracerProvider) MySQLRelayOption {
	return mysqlstore.WithRelayTracerProvider(tp)
}

// MySQLWithRelayMeterProvider sets the OTel MeterProvider for MySQL relay metrics.
// Default: the OTel global provider. Mirrors WithRelayMeterProvider.
func MySQLWithRelayMeterProvider(mp metric.MeterProvider) MySQLRelayOption {
	return mysqlstore.WithRelayMeterProvider(mp)
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
// Available options: MySQLWithPollInterval, MySQLWithBatchSize, MySQLWithRelayClock,
// MySQLWithMaxDeliveryAttempts, MySQLWithRelayBackoff, MySQLWithRelayLogger,
// MySQLWithRelayTracerProvider, MySQLWithRelayMeterProvider.
// Note: there is no MySQLWithListenNotify — MySQL is poll-only.
//
// Example:
//
//	db, _ := sql.Open("mysql", dsn)
//	persistence.MigrateMySQL(ctx, db)
//	relay := persistence.NewMySQLRelay(db, myPublisher,
//	    persistence.MySQLWithPollInterval(500*time.Millisecond),
//	)
//	go relay.Run(ctx)
func NewMySQLRelay(db *sql.DB, pub runtime.Publisher, opts ...MySQLRelayOption) Relay {
	return mysqlstore.NewRelay(db, pub, opts...)
}

// NewMySQLDeduper constructs a MySQLDeduper backed by db. It implements the
// idempotent-consumer pattern (ADR-0018) using INSERT IGNORE into
// wrkflw_processed_message.
//
// MigrateMySQL must be called before the first Seen call so the
// wrkflw_processed_message table exists.
//
// Returns MySQLDeduper rather than the Postgres-typed Deduper interface because
// they use incompatible transaction types (pgx.Tx vs *sql.Tx).
//
// Example:
//
//	db, _ := sql.Open("mysql", dsn)
//	persistence.MigrateMySQL(ctx, db)
//	d := persistence.NewMySQLDeduper(db)
//	tx, _ := db.BeginTx(ctx, nil)
//	first, err := d.Seen(ctx, tx, "my-subscriber", msgID)
//	if err != nil { ... }
//	if !first { return nil } // duplicate: skip side effect
//	// ... perform side effect ...
//	tx.Commit()
func NewMySQLDeduper(db *sql.DB) MySQLDeduper {
	return mysqlstore.NewDeduper(db)
}

// Compile-time checks: MySQL internal concrete types must satisfy the same
// public interfaces as their Postgres analogs.
var (
	_ Store              = (*mysqlstore.Store)(nil)
	_ runtime.TimerStore = (*mysqlstore.TimerStore)(nil)
	_ Relay              = (*mysqlstore.Relay)(nil)
	_ MySQLDeduper       = (*mysqlstore.Deduper)(nil)
)
