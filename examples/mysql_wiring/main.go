// Package main is the reference MySQL-wiring example: it shows a consumer
// embedding the engine and mounting its transports in their own HTTP server,
// using MySQL 8.0+ as the persistence backend instead of PostgreSQL.
//
// This is reference wiring ONLY — NOT a shipped binary. The product is the
// importable library; this file illustrates how to assemble it with MySQL.
// Compare examples/production_wiring/main.go (Postgres) for a full lifecycle
// walk-through; this file focuses on the MySQL-specific constructor calls.
//
// DSN requirements:
//
//	parseTime=true&loc=UTC          — always required for correct DATETIME(6) round-trips
//	multiStatements=true            — required only during migration (goose runs multi-stmt SQL)
//
// The scheduler is wired with a MySQL-backed leader elector (built from
// scheduling/backend/mysql and passed via scheduling.WithElector) so exactly one
// replica runs timer fires across a multi-replica deployment (ADR-0059, ADR-0072,
// ADR-0102). The on-leadership-acquired callback rehydrates persisted timers on
// the new leader so timers armed at runtime are not lost on failover (Option A).
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql" // register "mysql" driver

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/eventing"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/persistence/cache/hotcache"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/calllink"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/scheduling"
	mysqlbackend "github.com/zakyalvan/krtlwrkflw/scheduling/backend/mysql"
	"github.com/zakyalvan/krtlwrkflw/service"
	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
	"github.com/zakyalvan/krtlwrkflw/transport/http/stdlib"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("server exited with error", "err", err)
		os.Exit(1)
	}
	logger.Info("clean shutdown complete")
}

// run wires everything with MySQL, serves until a termination signal, then
// tears down gracefully. Returns the first non-nil error from serving or shutdown.
func run(logger *slog.Logger) error {
	// workerCtx is cancelled on shutdown to stop background goroutines (relay,
	// notifier). The HTTP server and resource holders are drained/closed after.
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()

	// shutdown aggregates every resource holder; Shutdown closes them in reverse
	// registration order and joins errors (ADR-0054).
	var shutdown runtime.ShutdownGroup

	// --- MySQL database connection ---
	// DSN must include parseTime=true&loc=UTC for correct DATETIME(6) semantics.
	// Add multiStatements=true only if you run MigrateMySQL at startup (goose
	// needs it to execute multi-statement migration files).
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		// Example DSN (not connected to a real server when unset):
		dsn = "user:password@tcp(127.0.0.1:3306)/wrkflw?parseTime=true&loc=UTC&multiStatements=true"
	}

	// Open a *sql.DB (connection pool). The DSN must include parseTime=true&loc=UTC.
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	// The pool is the lowest-level resource: closed LAST (registered first).
	shutdown.Add(func(context.Context) error { return db.Close() })

	// Apply schema migrations idempotently.  MigrateMySQL uses goose + embedded
	// SQL files; re-runs are safe (version table prevents double-apply).
	// multiStatements=true in the DSN is required here so goose can execute
	// multi-statement migration files in a single round-trip.
	if merr := persistence.MigrateMySQL(workerCtx, db); merr != nil {
		return merr
	}

	// Open the MySQL-backed kernel.InstanceStore (and JournalReader).
	store, oerr := persistence.OpenMySQL(workerCtx, db)
	if oerr != nil {
		return oerr
	}

	// --- Eventing: in-process publisher (GoChannel; no broker needed) ---
	publisher, _, evClose := eventing.NewGoChannelPublisher(eventing.WithLogger(logger))
	shutdown.AddCloser(evClose)

	// --- Outbox relay: drains wrkflw_outbox and publishes events ---
	relay, err := persistence.NewMySQLRelay(db, publisher, persistence.MySQLWithRelayLogger(logger))
	if err != nil {
		return fmt.Errorf("new mysql relay: %w", err)
	}
	go func() {
		if rerr := relay.Run(workerCtx); rerr != nil && !errors.Is(rerr, context.Canceled) {
			logger.Error("mysql relay run", "err", rerr)
		}
	}()

	// --- Call-link notifier: resumes parked parent instances ---
	// deliver is the function the notifier calls when a sub-instance completes or
	// fails; wire it to driver.ApplyTrigger once the driver is constructed below.
	// The closure captures driver by pointer so the forward-reference is safe:
	// driver is assigned after the notifier is wired up, but the closure only
	// reads it at invocation time (after assignment).
	var driver *runtime.ProcessDriver
	deliver := calllink.CallDeliverFunc(func(ctx context.Context, def *model.ProcessDefinition, instanceID string, trg engine.Trigger) error {
		if driver == nil {
			return nil // not yet wired; should not occur in practice
		}
		_, err := driver.ApplyTrigger(ctx, def, instanceID, trg)
		return err
	})
	// The definition store resolves parent-process definitions during notification.
	defStore, err := persistence.NewMySQLDefinitionStore(db)
	if err != nil {
		return fmt.Errorf("new mysql definition store: %w", err)
	}
	notifier, err := persistence.NewMySQLCallNotifier(db, deliver, defStore)
	if err != nil {
		return fmt.Errorf("call notifier: %w", err)
	}
	go func() {
		if nerr := notifier.Run(workerCtx); nerr != nil && !errors.Is(nerr, context.Canceled) {
			logger.Error("mysql call notifier run", "err", nerr)
		}
	}()

	// --- Multi-replica advisory-lock ownership for the caching store ---
	// NewMySQLAdvisoryLockOwnership returns (Ownership, io.Closer, error); the
	// Closer releases all GET_LOCKs and the dedicated session connection.
	ownership, ownerCloser, olerr := persistence.NewMySQLAdvisoryLockOwnership(workerCtx, db)
	if olerr != nil {
		return olerr
	}
	shutdown.AddCloser(ownerCloser)

	// Wrap the store in the caching store so hot instances are served from memory.
	cachingStore, err := persistence.NewCachingInstanceStore(store, ownership, hotcache.New())
	if err != nil {
		return fmt.Errorf("caching store: %w", err)
	}

	// --- Scheduler with MySQL leader elector (single-leader timer firing) ---
	// We capture driver in a closure; it is assigned after scheduler construction.
	// The closure reads driver at call time (after assignment below), so this
	// forward-reference pattern is safe (mirrors the doc example for Option A).
	//
	// The MySQL-backed leader elector is built from the DB-specific backend package
	// and passed to the neutral scheduling façade via WithElector (ADR-0102). The
	// façade closes it (io.Closer) on scheduler.Close, so no separate closer needed.
	elector, eerr := mysqlbackend.NewElector(workerCtx, db,
		mysqlbackend.WithOnLeadershipAcquired(func(ctx context.Context) {
			// driver is assigned below; the closure reads it at invocation time.
			if driver != nil {
				_ = driver.RehydrateTimers(ctx)
			}
		}),
	)
	if eerr != nil {
		return eerr
	}
	scheduler, serr := scheduling.NewScheduler(
		scheduling.WithLogger(logger),
		scheduling.WithElector(elector),
	)
	if serr != nil {
		_ = elector.Close()
		return serr
	}
	shutdown.AddCloser(scheduler)

	// --- A demo definition + catalog so the engine can actually run instances ---
	def, derr := definition.NewBuilder("order", 1).
		Add(event.NewStart("s")).
		Add(activity.NewServiceTask("charge", activity.WithActionName("charge-card"))).
		Add(event.NewEnd("e")).
		Connect("s", "charge").
		Connect("charge", "e").
		Build()
	if derr != nil {
		return derr
	}
	cat := action.NewCatalog(map[string]action.Action{
		"charge-card": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
			return map[string]any{"charged": true}, nil
		}),
	})
	// Use the durable MySQL definition store (backed by wrkflw_definitions) so
	// definitions survive restarts. For illustrative purposes we also seed a
	// well-known definition via the map registry; in production you would use
	// persistence.NewMySQLDefinitionStore exclusively.
	reg := kernel.NewMapDefinitionRegistry(def)

	// --- Timer store for rehydration ---
	timerStore, err := persistence.NewMySQLTimerStore(db)
	if err != nil {
		return fmt.Errorf("new mysql timer store: %w", err)
	}

	// --- Engine + human-task plumbing + Service facade ---
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	az := authz.RoleAuthorizer{}
	driver, err = runtime.NewProcessDriver(
		runtime.WithActionCatalog(cat),
		runtime.WithInstanceStore(cachingStore),
		runtime.WithHumanTasks(resolver, taskStore, az),
		runtime.WithScheduler(scheduler),
		runtime.WithTimerStore(timerStore),
	)
	if err != nil {
		return err
	}
	lister, err := persistence.NewMySQLLister(db)
	if err != nil {
		return fmt.Errorf("new mysql lister: %w", err)
	}
	svc, err := service.NewEngine(
		service.WithProcessDriver(driver),
		service.WithInstanceStore(cachingStore),
		service.WithDefinitions(reg),
		service.WithLister(lister),
		service.WithHumanTasks(taskStore, az),
	)
	if err != nil {
		return err
	}

	// --- Health probe (MySQL ping) ---
	readyChecks := []httpcore.HealthCheck{persistence.NewMySQLPingCheck(db)}

	// --- Mount BOTH the workflow REST routes and the health routes ---
	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)
	stdlib.MountHealth(mux, readyChecks...)

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// --- Serve until a termination signal arrives ---
	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", srv.Addr)
		if lerr := srv.ListenAndServe(); lerr != nil && !errors.Is(lerr, http.ErrServerClosed) {
			serveErr <- lerr
			return
		}
		serveErr <- nil
	}()

	select {
	case <-signalCtx.Done():
		logger.Info("termination signal received; shutting down")
	case err := <-serveErr:
		if err != nil {
			return err
		}
	}

	// --- Graceful teardown ---
	// 1. Stop background workers (relay, notifier) by cancelling their context.
	stopWorkers()

	// 2. Drain in-flight HTTP requests with a bounded deadline.
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelDrain()
	httpErr := srv.Shutdown(drainCtx)

	// 3. Release every resource holder (scheduler, ownership, eventing, db pool)
	//    in reverse registration order, joining any errors.
	releaseCtx, cancelRelease := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelRelease()
	releaseErr := shutdown.Shutdown(releaseCtx)

	return errors.Join(httpErr, releaseErr)
}
