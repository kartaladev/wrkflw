// Package main is the reference production-wiring example (ADR-0054): it shows a
// consumer embedding the engine and mounting its transports in their OWN HTTP
// server, with full graceful shutdown — signal handling, a bounded HTTP drain,
// and a single aggregated release of every background worker and resource holder.
//
// What it demonstrates end to end:
//
//   - construct the engine (Runner + Service), a gocron Scheduler, and the
//     transactional-outbox Relay;
//   - mount the REST handler AND a liveness/readiness health handler
//     (/healthz, /readyz) on the consumer's own *http.Server;
//   - start the background workers the consumer owns — relay.Run(ctx),
//     notifier.Run(ctx) — each stopped by cancelling their context;
//   - on SIGINT/SIGTERM: cancel the worker context, gracefully Shutdown the
//     HTTP server with a deadline, then call runtime.ShutdownGroup.Shutdown to
//     release the resource holders (scheduler, eventing closer, pool) in reverse
//     order, joining any errors.
//
// It runs with or without Postgres: set DATABASE_URL to wire the Postgres store,
// relay, and a DB-ping readiness check; unset, it falls back to in-memory stores
// and an always-ready probe so the example still builds and runs.
//
// This is reference wiring — NOT a shipped binary. The product is the importable
// library; this file only illustrates how to assemble it.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonboulle/clockwork"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/eventing"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime"
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

// run wires everything, serves until a termination signal, then tears down
// gracefully. It returns the first non-nil error from serving or shutdown.
func run(logger *slog.Logger) error {
	// workerCtx is cancelled first on shutdown to stop the Run(ctx) background
	// workers (relay, notifier). The HTTP server and the resource holders are
	// drained/closed AFTER, via their own deadline-bounded paths.
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()

	// One clock drives both the engine and the scheduler (ADR-0003); a single
	// fake-clock advance moves both under test. Production uses the real clock.
	clk := clockwork.NewRealClock()

	// shutdown aggregates every resource holder; Shutdown closes them in reverse
	// registration order and joins errors (ADR-0054).
	var shutdown runtime.ShutdownGroup

	// --- Eventing: in-process publisher (GoChannel; no broker needed) ---
	publisher, _, evClose := eventing.NewGoChannelPublisher(eventing.WithLogger(logger))
	shutdown.AddCloser(evClose)

	// --- Scheduler: gocron-backed timer/deadline driver ---
	scheduler, err := scheduling.NewScheduler(scheduling.WithSchedulerClock(clk), scheduling.WithLogger(logger))
	if err != nil {
		return err
	}
	shutdown.AddCloser(scheduler) // *Scheduler is an io.Closer

	// --- Store, relay, and readiness probe (Postgres when DATABASE_URL is set) ---
	memStore, merr := kernel.NewMemStore()
	if merr != nil {
		return merr
	}
	var (
		store       kernel.Store = memStore
		lister                   = memStore
		readyChecks []httpcore.HealthCheck
	)
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		pool, perr := pgxpool.New(workerCtx, dsn)
		if perr != nil {
			return perr
		}
		// The pool is the lowest-level resource: closed LAST (registered first).
		shutdown.Add(func(context.Context) error { pool.Close(); return nil })

		if merr := persistence.Migrate(workerCtx, pool); merr != nil {
			return merr
		}
		pgStore, oerr := persistence.OpenPostgres(workerCtx, pool)
		if oerr != nil {
			return oerr
		}
		store = pgStore

		// Relay drains the transactional outbox; the consumer owns its goroutine.
		relay, rerr := persistence.NewRelay(pool, publisher, persistence.WithRelayLogger(logger))
		if rerr != nil {
			return rerr
		}
		go func() {
			if rerr := relay.Run(workerCtx); rerr != nil && !errors.Is(rerr, context.Canceled) {
				logger.Error("relay run", "err", rerr)
			}
		}()

		// Readiness is wired to a real Postgres ping.
		readyChecks = append(readyChecks, persistence.NewPingCheck(pool))
		logger.Info("wired Postgres store + outbox relay + DB readiness probe")
	} else {
		logger.Info("DATABASE_URL unset — using in-memory store; readiness probe is static")
	}

	// --- A demo definition + catalog so the engine can actually run instances ---
	def, err := definition.NewBuilder("order", 1).
		Add(event.NewStart("s")).
		Add(activity.NewServiceTask("charge", activity.WithActionName("charge-card"))).
		Add(event.NewEnd("e")).
		Connect("s", "charge").
		Connect("charge", "e").
		Build()
	if err != nil {
		return err
	}
	cat := action.NewMapCatalog(map[string]action.Action{
		"charge-card": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
			return map[string]any{"charged": true}, nil
		}),
	})
	reg := kernel.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		"order":   def,
		"order:1": def,
	})

	// --- Engine + human-task plumbing + Service facade ---
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	az := authz.RoleAuthorizer{}
	runner, err := runtime.NewProcessDriver(cat, store,
		runtime.WithHumanTasks(resolver, taskStore, az),
	)
	if err != nil {
		return err
	}
	tasks, err := task.NewTaskService(taskStore, az)
	if err != nil {
		return err
	}
	svc := service.New(runner, tasks, reg, store, lister, taskStore)

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

	// 3. Release every resource holder (scheduler, eventing, pool) in reverse
	//    order, joining any errors. Bound this drain too.
	releaseCtx, cancelRelease := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelRelease()
	releaseErr := shutdown.Shutdown(releaseCtx)

	return errors.Join(httpErr, releaseErr)
}
