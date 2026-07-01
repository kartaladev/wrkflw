// Package main demonstrates the database toolkit (internal/database + internal/database/transaction)
// against both a real PostgreSQL 17 and a real MySQL 8.0 database, each spun up via
// testcontainers. It is reference wiring ONLY — NOT a shipped binary.
//
// Requires a running Docker daemon.
//
// # What it shows
//
//   - database.ProbeUTC — fail-fast UTC correctness check at open time.
//   - transaction.Begin — start an ambient transaction, stash it in context.
//   - database.Querier — driver-agnostic DML, identical code for PG and MySQL.
//   - transaction.MarkRollback — mark the ambient tx rollback-only (simulates an error path).
//   - Commit — honours the rollback mark and rolls back instead of committing.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/database/transaction"
	"github.com/zakyalvan/krtlwrkflw/persistence"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx := context.Background()

	logger.Info("starting real_db_transaction demo")

	if err := runPostgres(ctx, logger); err != nil {
		log.Fatalf("postgres demo failed: %v", err)
	}

	if err := runMySQL(ctx, logger); err != nil {
		log.Fatalf("mysql demo failed: %v", err)
	}

	logger.Info("demo complete: no drift detected, rollback-only demonstrated on both dialects")
}

// runPostgres starts a Postgres 17 testcontainer and runs the transaction demo.
func runPostgres(ctx context.Context, logger *slog.Logger) error {
	logger.Info("starting postgres:17-alpine container")
	c, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("demo"),
		tcpostgres.WithUsername("demo"),
		tcpostgres.WithPassword("demo"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		return fmt.Errorf("start postgres container: %w", err)
	}
	defer func() { _ = c.Terminate(ctx) }()

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return fmt.Errorf("postgres connection string: %w", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open pgxpool: %w", err)
	}
	defer pool.Close()

	ph := func(n int) string { return fmt.Sprintf("$%d", n) }
	return run(ctx, logger, pool, ph, database.Postgres, "postgres")
}

// runMySQL starts a MySQL 8.0 testcontainer and runs the transaction demo.
func runMySQL(ctx context.Context, logger *slog.Logger) error {
	logger.Info("starting mysql:8.0 container")
	c, err := tcmysql.Run(ctx, "mysql:8.0",
		tcmysql.WithDatabase("demo"),
		tcmysql.WithUsername("demo"),
		tcmysql.WithPassword("demo"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("port: 3306  MySQL Community Server").
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		return fmt.Errorf("start mysql container: %w", err)
	}
	defer func() { _ = c.Terminate(ctx) }()

	host, err := c.Host(ctx)
	if err != nil {
		return fmt.Errorf("mysql container host: %w", err)
	}
	port, err := c.MappedPort(ctx, "3306/tcp")
	if err != nil {
		return fmt.Errorf("mysql container port: %w", err)
	}

	baseDSN := fmt.Sprintf("demo:demo@tcp(%s:%s)/demo", host, port.Port())
	dsn, err := persistence.MySQLDSN(baseDSN)
	if err != nil {
		return fmt.Errorf("build mysql dsn: %w", err)
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("open mysql db: %w", err)
	}
	defer func() { _ = db.Close() }()

	ph := func(int) string { return "?" }
	return run(ctx, logger, db, ph, database.MySQL, "mysql")
}

// run is the shared demo logic: ProbeUTC, create table, commit path, rollback-only path.
// conn must be a *pgxpool.Pool (Postgres) or *sql.DB (MySQL).
func run(ctx context.Context, logger *slog.Logger, conn any, ph func(int) string, dialect database.Dialect, label string) error {
	q, err := database.From(conn)
	if err != nil {
		return fmt.Errorf("[%s] From: %w", label, err)
	}

	// Fail fast if the connection is not UTC-correct.
	if err := database.ProbeUTC(ctx, q, dialect); err != nil {
		return fmt.Errorf("[%s] ProbeUTC: %w", label, err)
	}
	logger.Info("UTC probe passed", "dialect", label)

	// Create a minimal demo table with a UTC timestamp column.
	var createSQL string
	switch dialect {
	case database.Postgres:
		createSQL = `CREATE TABLE demo_events (id INT, created_at TIMESTAMPTZ DEFAULT NOW())`
	case database.MySQL:
		createSQL = `CREATE TABLE demo_events (id INT, created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6))`
	}
	if _, err := q.Exec(ctx, createSQL); err != nil {
		return fmt.Errorf("[%s] create table: %w", label, err)
	}

	// --- Commit path: Begin → insert → Commit ---
	tx, txCtx, err := transaction.Begin(ctx, conn)
	if err != nil {
		return fmt.Errorf("[%s] Begin (commit path): %w", label, err)
	}
	if _, err := tx.Exec(txCtx, `INSERT INTO demo_events (id) VALUES (`+ph(1)+`)`, 1); err != nil {
		_ = tx.Rollback(txCtx)
		return fmt.Errorf("[%s] insert (commit path): %w", label, err)
	}
	if err := tx.Commit(txCtx); err != nil {
		return fmt.Errorf("[%s] commit: %w", label, err)
	}

	// Read back the round-tripped timestamp.
	var insertedAt time.Time
	if err := q.QueryRow(ctx, `SELECT created_at FROM demo_events WHERE id = `+ph(1), 1).Scan(&insertedAt); err != nil {
		return fmt.Errorf("[%s] read back: %w", label, err)
	}
	logger.Info("committed row round-tripped", "dialect", label, "created_at", insertedAt.UTC().Format(time.RFC3339Nano))

	// --- MarkRollback path: Begin → insert → MarkRollback → Commit (rolls back) ---
	tx2, txCtx2, err := transaction.Begin(ctx, conn)
	if err != nil {
		return fmt.Errorf("[%s] Begin (rollback path): %w", label, err)
	}
	if _, err := tx2.Exec(txCtx2, `INSERT INTO demo_events (id) VALUES (`+ph(1)+`)`, 2); err != nil {
		_ = tx2.Rollback(txCtx2)
		return fmt.Errorf("[%s] insert (rollback path): %w", label, err)
	}
	transaction.MarkRollback(txCtx2) // simulate a downstream error signalling rollback-only
	if err := tx2.Commit(txCtx2); err != nil {
		return fmt.Errorf("[%s] commit (rollback path): %w", label, err)
	}

	// Only row 1 must survive.
	var count int
	if err := q.QueryRow(ctx, `SELECT COUNT(*) FROM demo_events`).Scan(&count); err != nil {
		return fmt.Errorf("[%s] count: %w", label, err)
	}
	if count != 1 {
		return fmt.Errorf("[%s] expected 1 committed row; got %d (MarkRollback did not roll back)", label, count)
	}
	logger.Info("MarkRollback honoured: only the committed row persists", "dialect", label, "rows", count)
	return nil
}
