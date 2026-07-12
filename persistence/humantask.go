package persistence

import (
	"database/sql"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
)

// NewTaskStore returns a durable PostgreSQL-backed [humantask.TaskStore] over
// pool. Run [Migrate] first to create the wrkflw_human_task table.
//
// Example:
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	persistence.Migrate(ctx, pool)
//	ts, err := persistence.NewTaskStore(pool)
func NewTaskStore(pool *pgxpool.Pool) (humantask.TaskStore, error) {
	return store.NewHumanTaskStore(pool, dialect.NewPostgres())
}

// NewMySQLTaskStore returns a durable MySQL-backed [humantask.TaskStore] over db.
// Run [MigrateMySQL] first to create the wrkflw_human_task table.
//
// Mirrors [NewTaskStore] for Postgres and [NewSQLiteTaskStore] for SQLite.
//
// Example:
//
//	db, _ := sql.Open("mysql", dsn)
//	persistence.MigrateMySQL(ctx, db)
//	ts, err := persistence.NewMySQLTaskStore(db)
func NewMySQLTaskStore(db *sql.DB) (humantask.TaskStore, error) {
	return store.NewHumanTaskStore(db, dialect.NewMySQL())
}

// NewSQLiteTaskStore returns a durable SQLite-backed [humantask.TaskStore] over db.
// Run [MigrateSQLite] first to create the wrkflw_human_task table (or use
// [dbtest.RunTestSQLite] in tests, which auto-migrates).
//
// SQLite is single-node and in-process; this constructor is well-suited for
// embedded deployments, CLI tools, integration tests, and local development.
//
// Mirrors [NewTaskStore] for Postgres and [NewMySQLTaskStore] for MySQL.
//
// Example:
//
//	db, _ := sql.Open("sqlite", "file:app.db?_pragma=journal_mode(WAL)")
//	db.SetMaxOpenConns(1)
//	persistence.MigrateSQLite(ctx, db)
//	ts, err := persistence.NewSQLiteTaskStore(db)
func NewSQLiteTaskStore(db *sql.DB) (humantask.TaskStore, error) {
	return store.NewHumanTaskStore(db, dialect.NewSQLite())
}

// Compile-time check: *store.HumanTaskStore must satisfy humantask.TaskStore so
// the facade constructors can return it as the interface.
var _ humantask.TaskStore = (*store.HumanTaskStore)(nil)
