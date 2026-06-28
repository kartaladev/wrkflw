package persistence

// mysql.go contains the consumer-facing façade over the MySQL persistence
// backend (internal/persistence/mysql). It mirrors the Postgres façade
// constructors: OpenMySQL, MigrateMySQL, NewMySQLTimerStore.
//
// MySQLOption is a distinct type from Option (which aliases postgres.StoreOption)
// because the two backends have incompatible concrete option function signatures.
// MySQL-specific option constructors (MySQLWith*) map 1:1 to internal/persistence/mysql
// option constructors, exactly as the Postgres façade option constructors map to
// internal/persistence/postgres option constructors.

import (
	"context"
	"database/sql"
	"log/slog"

	_ "github.com/go-sql-driver/mysql" // register "mysql" driver
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	mysqlstore "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

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

// Compile-time checks: MySQL internal concrete types must satisfy the same
// public interfaces as their Postgres analogs.
var (
	_ Store              = (*mysqlstore.Store)(nil)
	_ runtime.TimerStore = (*mysqlstore.TimerStore)(nil)
)
