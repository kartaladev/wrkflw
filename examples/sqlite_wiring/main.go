// Package main is the reference SQLite-wiring example: it shows a consumer
// embedding the engine and mounting its transports in their own HTTP server,
// using SQLite (via modernc.org/sqlite — CGo-free, in-process) as the
// persistence backend.
//
// SQLite characteristics relevant to this wiring:
//
//   - In-process, no Docker, no network database — ideal for local development,
//     CLI tools, integration tests, and embedded single-process deployments.
//   - Single-writer: db.SetMaxOpenConns(1) is REQUIRED. Without it, concurrent
//     writes race on the WAL file and produce "database is locked" errors.
//   - Poll-only outbox relay: SQLite has no LISTEN/NOTIFY; NewSQLiteRelay loops
//     on a configurable poll interval (do NOT pass WithListenNotify).
//   - No distributed advisory locking: NewSQLiteAdvisoryLockOwnership returns a
//     fail-loud Ownership whose Acquire always returns (false, ErrUnsupported).
//     For a single-process deployment, kernel.AlwaysOwn{} is the correct
//     ownership value for kernel.NewCachingStore — it avoids the error path and
//     gives the in-process cache its full benefit.
//   - No multi-replica timer elector: scheduling.NewScheduler with no elector
//     option (unlike the MySQL/Postgres examples, which pass WithMySQLTimerElector
//     or WithTimerElector). SQLite is inherently single-process.
//
// WAL + foreign-key pragmas are set in the DSN so they apply for every connection
// opened from the pool (even though MaxOpenConns(1) means one at a time).
//
// This is reference wiring ONLY — NOT a shipped binary. The product is the
// importable library; this file illustrates how to assemble it with SQLite.
// Compare examples/mysql_wiring/main.go (MySQL) and
// examples/production_wiring/main.go (Postgres) for alternative backends.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "modernc.org/sqlite" // register "sqlite" driver (CGo-free)

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
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/calllink"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
	"github.com/zakyalvan/krtlwrkflw/scheduling"
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

// run wires everything with SQLite, serves until a termination signal, then
// tears down gracefully. Returns the first non-nil error from serving or shutdown.
func run(logger *slog.Logger) error {
	// workerCtx is cancelled on shutdown to stop background goroutines (relay,
	// notifier). The HTTP server and resource holders are drained/closed after.
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()

	// shutdown aggregates every resource holder; Shutdown closes them in reverse
	// registration order and joins errors (ADR-0054).
	var shutdown runtime.ShutdownGroup

	// --- SQLite database connection ---
	//
	// DSN uses WAL journal mode for better concurrent read performance and
	// foreign_keys pragma to enforce referential integrity at the SQLite layer.
	//
	// Use a file-based database (not :memory:) so schema migrations persist and
	// the demo state survives restarts. The file is relative to the working
	// directory; override via SQLITE_DSN for a custom path.
	dsn := os.Getenv("SQLITE_DSN")
	if dsn == "" {
		dsn = "file:app.db?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}

	// REQUIRED: SQLite serialises all writes through the WAL file. With more than
	// one open connection, concurrent write attempts race and return "database is
	// locked". A single open connection (MaxOpenConns=1) is the correct setting;
	// reads block briefly behind writes, which is acceptable for single-process
	// embedded deployments.
	db.SetMaxOpenConns(1)

	// The pool is the lowest-level resource: closed LAST (registered first).
	shutdown.Add(func(context.Context) error { return db.Close() })

	// Apply schema migrations idempotently.  MigrateSQLite uses goose + embedded
	// SQL files; re-runs are safe (version table prevents double-apply).
	if merr := persistence.MigrateSQLite(workerCtx, db); merr != nil {
		return merr
	}

	// Open the SQLite-backed kernel.Store (and JournalReader).
	store, oerr := persistence.OpenSQLite(workerCtx, db)
	if oerr != nil {
		return oerr
	}

	// --- Eventing: in-process publisher (GoChannel; no broker needed) ---
	publisher, _, evClose := eventing.NewGoChannelPublisher(eventing.WithLogger(logger))
	shutdown.AddCloser(evClose)

	// --- Outbox relay: drains wrkflw_outbox and publishes events ---
	// SQLite has no LISTEN/NOTIFY; NewSQLiteRelay is poll-only — do NOT pass
	// any ListenNotify option. The poll interval defaults to 500 ms which is
	// suitable for local/embedded deployments.
	relay, err := persistence.NewSQLiteRelay(db, publisher, persistence.MySQLWithRelayLogger(logger))
	if err != nil {
		return err
	}
	go func() {
		if rerr := relay.Run(workerCtx); rerr != nil && !errors.Is(rerr, context.Canceled) {
			logger.Error("sqlite relay run", "err", rerr)
		}
	}()

	// --- Call-link notifier: resumes parked parent instances ---
	// deliver is the function the notifier calls when a sub-instance completes or
	// fails; wire it to runner.Deliver once the runner is constructed below.
	// The closure captures runner by pointer so the forward-reference is safe:
	// runner is assigned after the notifier is wired up, but the closure only
	// reads it at invocation time (after assignment).
	var runner *runtime.ProcessDriver
	deliver := calllink.CallDeliverFunc(func(ctx context.Context, def *model.ProcessDefinition, instanceID string, trg engine.Trigger) error {
		if runner == nil {
			return nil // not yet wired; should not occur in practice
		}
		_, err := runner.Deliver(ctx, def, instanceID, trg)
		return err
	})
	// The definition store resolves parent-process definitions during notification.
	defStore, err := persistence.NewSQLiteDefinitionStore(db)
	if err != nil {
		return err
	}
	notifier, err := persistence.NewSQLiteCallNotifier(db, deliver, defStore)
	if err != nil {
		return err
	}
	go func() {
		if nerr := notifier.Run(workerCtx); nerr != nil && !errors.Is(nerr, context.Canceled) {
			logger.Error("sqlite call notifier run", "err", nerr)
		}
	}()

	// --- Single-process ownership for the caching store ---
	//
	// NewSQLiteAdvisoryLockOwnership returns a fail-loud Ownership whose Acquire
	// always returns (false, ErrUnsupported) — SQLite has no distributed locking
	// mechanism. For a single-process deployment the correct ownership is
	// kernel.AlwaysOwn{}: this process is always the sole writer, so the caching
	// store can cache every instance it touches without a lock check.
	//
	// If you later switch to a multi-process deployment, replace AlwaysOwn with
	// persistence.NewAdvisoryLockOwnership (Postgres) or
	// persistence.NewMySQLAdvisoryLockOwnership (MySQL).
	//
	// The SQLite ownership closer is a no-op but is registered for shutdown parity
	// with the Postgres/MySQL wiring (makes backend-swap diffs minimal).
	_, ownerCloser, olerr := persistence.NewSQLiteAdvisoryLockOwnership()
	if olerr != nil {
		return olerr
	}
	shutdown.AddCloser(ownerCloser)

	// Use AlwaysOwn for single-process caching — the fail-loud SQLite ownership
	// value is not passed to NewCachingStore.
	cachingStore, err := kernel.NewCachingStore(store, kernel.AlwaysOwn{})
	if err != nil {
		return err
	}

	// --- Scheduler (no elector — SQLite is single-process) ---
	// SQLite is inherently single-process; there is no multi-replica timer leader
	// election. Unlike the MySQL/Postgres examples, we omit WithMySQLTimerElector
	// / WithTimerElector so every process fires its own timers independently
	// (which is correct when only one process exists).
	scheduler, serr := scheduling.NewScheduler(
		scheduling.WithLogger(logger),
	)
	if serr != nil {
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
	cat := action.NewMapCatalog(map[string]action.Action{
		"charge-card": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
			return map[string]any{"charged": true}, nil
		}),
	})
	// Use the durable SQLite definition store (backed by wrkflw_definitions) so
	// definitions survive restarts. For illustrative purposes we also seed a
	// well-known definition via the map registry; in production you would use
	// persistence.NewSQLiteDefinitionStore exclusively.
	reg := kernel.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		"order":   def,
		"order:1": def,
	})

	// --- Timer store for rehydration ---
	timerStore, err := persistence.NewSQLiteTimerStore(db)
	if err != nil {
		return err
	}

	// --- Engine + human-task plumbing + Service facade ---
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	az := authz.RoleAuthorizer{}
	runner, err = runtime.NewProcessDriver(cat, cachingStore,
		runtime.WithHumanTasks(resolver, taskStore, az),
		runtime.WithScheduler(scheduler),
		runtime.WithTimerStore(timerStore),
	)
	if err != nil {
		return err
	}
	tasks, err := task.NewTaskService(taskStore, az)
	if err != nil {
		return err
	}
	lister, err := persistence.NewSQLiteLister(db)
	if err != nil {
		return err
	}
	svc := service.New(runner, tasks, reg, cachingStore, lister, taskStore)

	// --- Health probe (SQLite *sql.DB ping) ---
	readyChecks := []httpcore.HealthCheck{persistence.NewSQLitePingCheck(db)}

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

	// 3. Release every resource holder (scheduler, ownership closer, eventing, db pool)
	//    in reverse registration order, joining any errors.
	releaseCtx, cancelRelease := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelRelease()
	releaseErr := shutdown.Shutdown(releaseCtx)

	return errors.Join(httpErr, releaseErr)
}
