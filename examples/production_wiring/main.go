// Package main is the reference production-wiring example (ADR-0054): it shows a
// consumer embedding the engine and mounting its transports in their OWN HTTP
// server, with full graceful shutdown — signal handling, a bounded HTTP drain,
// and a single aggregated release of every background worker and resource holder.
//
// What it demonstrates end to end:
//
//   - construct the engine (ProcessDriver + Service), a metric provider, and the
//     transactional-outbox Relay;
//   - make timers DURABLE: wire a TimerStore so armed timers survive a restart,
//     start the driver, and re-arm the persisted timers with RehydrateTimers;
//   - mount the REST handler, a liveness/readiness health handler
//     (/healthz, /readyz), AND the admin routes behind an auth guard, on the
//     consumer's own *http.Server;
//   - start the background worker the consumer owns — relay.Run(ctx) — stopped
//     by cancelling its context;
//   - on SIGINT/SIGTERM: cancel the worker context, gracefully Shutdown the
//     HTTP server with a deadline, then call runtime.ShutdownGroup.Shutdown to
//     release the resource holders in reverse registration order, joining errors.
//
// It runs with or without Postgres: set DATABASE_URL to wire the Postgres store,
// durable timer store, relay, and a DB-ping readiness check; unset, it falls back
// to in-memory stores and an always-ready probe so the example still builds and
// runs — but with NO timer durability, which is the whole point of the
// DATABASE_URL branch.
//
// On the scheduler: this example deliberately does NOT pass runtime.WithScheduler.
// NewProcessDriver then creates and OWNS an in-process gocron scheduler, and —
// only on that owned path — auto-registers the durable timer JobStore that
// RehydrateTimers reads. driver.Start starts it and driver.Shutdown drains it.
//
// This is reference wiring — NOT a shipped binary. The product is the importable
// library; this file only illustrates how to assemble it.
package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonboulle/clockwork"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/eventing"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/persistence"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/service"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
	"github.com/kartaladev/wrkflw/transport/http/stdlib"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// requireAdminToken is the example's stand-in for real admin authentication. The
// bundled AdminRoutes have none of their own by design (ADR-0095), so SOMETHING
// must sit in front of them.
//
// It is deliberately minimal, and deliberately fails CLOSED: with ADMIN_TOKEN
// unset every /admin/ request is refused, so forgetting to configure it cannot
// silently expose the admin surface. Replace it with your real middleware — a
// constant-time comparison, a session/JWT check, an mTLS peer check — before
// running anything like this outside a laptop.
func requireAdminToken(next http.Handler, token string, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			logger.Warn("admin request refused: ADMIN_TOKEN is not set", "path", r.URL.Path)
			http.Error(w, "admin API disabled", http.StatusServiceUnavailable)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Admin-Token")), []byte(token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

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
	// worker (the outbox relay). The HTTP server and the resource holders are
	// drained/closed AFTER, via their own deadline-bounded paths.
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()

	// One clock drives the engine (ADR-0138); a single fake-clock advance moves it
	// under test. Production uses the real clock.
	clk := clockwork.NewRealClock()

	// shutdown aggregates every resource holder. Shutdown runs them in REVERSE
	// registration order (ADR-0054), so register lowest-level resources FIRST:
	// they are then released LAST, after their users have drained.
	var shutdown runtime.ShutdownGroup

	// --- Metrics: the engine emits through whatever MeterProvider it is given ---
	// A bare SDK provider has no reader attached, so it records without exporting;
	// a real consumer adds one (e.g. sdkmetric.WithReader(prometheusExporter)).
	// The point here is the LIFECYCLE: construct, inject, and flush on shutdown.
	meterProvider := sdkmetric.NewMeterProvider()
	shutdown.Add(meterProvider.Shutdown)

	// --- Eventing: in-process publisher (GoChannel; no broker needed) ---
	publisher, _, evClose := eventing.NewGoChannelPublisher(eventing.WithLogger(logger))
	shutdown.AddCloser(evClose)

	// --- Store, timers, relay, readiness probe (Postgres when DATABASE_URL is set) ---
	memStore, merr := kernel.NewMemInstanceStore()
	if merr != nil {
		return merr
	}
	var (
		store       kernel.InstanceStore = memStore
		lister                           = memStore
		timerStore  kernel.TimerStore
		readyChecks []httpcore.HealthCheck
	)
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		pool, perr := pgxpool.New(workerCtx, dsn)
		if perr != nil {
			return perr
		}
		// The pool is the lowest-level resource, so it is registered BEFORE the
		// driver and is therefore released AFTER it: the driver's shutdown drains
		// in-flight steps and timer fires that still need the database.
		shutdown.Add(func(context.Context) error { pool.Close(); return nil })

		if merr := persistence.Migrate(workerCtx, pool); merr != nil {
			return merr
		}
		pgStore, oerr := persistence.OpenPostgres(workerCtx, pool)
		if oerr != nil {
			return oerr
		}
		store = pgStore

		// THE durability seam. Without a TimerStore the instance state is durable
		// but every armed timer lives only in the scheduler's memory, so a restart
		// silently drops it — the process resumes "running" and the timer never
		// fires. With one, timers are persisted alongside state and re-armed by
		// RehydrateTimers below.
		ts, terr := persistence.NewTimerStore(pool)
		if terr != nil {
			return terr
		}
		timerStore = ts

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
		logger.Info("wired Postgres store + durable timer store + outbox relay + DB readiness probe")
	} else {
		logger.Info("DATABASE_URL unset — in-memory store, static readiness probe, NO timer durability")
	}

	// --- A demo definition + catalog so the engine can actually run instances ---
	def, err := definition.NewBuilder("order", 1).
		Add(event.NewStart("s")).
		Add(activity.NewServiceTask("charge", activity.WithTaskAction("charge-card"))).
		Add(event.NewEnd("e")).
		Connect("s", "charge").
		Connect("charge", "e").
		Build()
	if err != nil {
		return err
	}
	cat := action.NewCatalog(map[string]action.Action{
		"charge-card": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
			return map[string]any{"charged": true}, nil
		}),
	})
	reg := kernel.NewMapDefinitionRegistry(def)

	// --- ProcessEngine + human-task plumbing + Service facade ---
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	az := authz.RoleAuthorizer{}
	driverOpts := []runtime.Option{
		runtime.WithActionCatalog(cat),
		runtime.WithInstanceStore(store),
		runtime.WithHumanTasks(resolver, taskStore, az),
		// RehydrateTimers resolves each persisted timer's definition through this
		// registry; without it, every timer would be skipped as unresolved.
		runtime.WithDefinitions(reg),
		runtime.WithClock(clk),
		runtime.WithLogger(logger),
		runtime.WithMeterProvider(meterProvider),
	}
	// Appended only when durable: passing a nil store would leave the driver
	// believing it has one.
	if timerStore != nil {
		driverOpts = append(driverOpts, runtime.WithTimerStore(timerStore))
	}
	driver, err := runtime.NewProcessDriver(driverOpts...)
	if err != nil {
		return err
	}

	// Start the driver-owned scheduler, and register its drain BEFORE anything
	// that follows so it runs FIRST on shutdown (reverse order) — in-flight timer
	// fires finish while the pool and publisher are still open.
	if serr := driver.Start(workerCtx); serr != nil {
		return serr
	}
	shutdown.Add(driver.Shutdown)

	// Re-arm timers that were armed before the last restart. Only meaningful with
	// a durable timer store; RehydrateTimers refuses without one, so the in-memory
	// branch correctly skips it rather than logging a spurious error.
	if timerStore != nil {
		if rerr := driver.RehydrateTimers(workerCtx); rerr != nil {
			// Non-fatal by design: unresolved definitions skip their timers and are
			// reported here rather than preventing startup.
			logger.Error("rehydrate timers", "err", rerr)
		}
	}

	svc, err := service.NewProcessEngine(
		service.WithProcessDriver(driver),
		service.WithInstanceStore(store),
		service.WithDefinitions(reg),
		service.WithLister(lister),
		service.WithHumanTasks(taskStore, az),
	)
	if err != nil {
		return err
	}

	// --- Mount the workflow REST routes, the health routes, and admin ---
	mux := http.NewServeMux()
	stdlib.Mount(mux, svc,
		httpcore.WithMeterProvider[*http.ServeMux](meterProvider),
		// DEMO ONLY, and deliberately OFF BY DEFAULT.
		//
		// ADR-0189 made the human-task verbs refuse an unauthenticated caller. This
		// example is about durable wiring, not authentication, so it offers a constant
		// actor — but only when WRKFLW_DEMO_INSECURE_ACTOR=1 is set explicitly.
		//
		// ⚠ The env gate is the point. Unset, this resolver refuses and the task routes
		// answer 401, so copy-pasting this file into a real deployment FAILS CLOSED
		// instead of silently authenticating every caller as a manager — which is
		// precisely the self-asserted-actor hole ADR-0189 exists to close.
		//
		// A real deployment resolves the actor from a VERIFIED credential in its own
		// middleware and calls authz.ContextWithActor; see SECURITY.md and
		// examples/authenticated_tasks, which verifies a bearer token.
		stdlib.WithRequestActor(func(context.Context) (authz.Actor, error) {
			if os.Getenv("WRKFLW_DEMO_INSECURE_ACTOR") != "1" {
				return authz.Actor{}, httpcore.ErrUnauthenticated
			}
			return authz.Actor{ID: "demo-user", Roles: []string{"manager"}}, nil
		}),
	)
	stdlib.MountHealth(mux, readyChecks...)

	// AdminRoutes has NO built-in authentication (ADR-0095: admin-by-composition).
	// It is mounted on its own mux so the whole /admin/ subtree can be wrapped in
	// one guard; every route it registers is under /admin/, so a single prefix
	// handler covers them all. The optional dep fields (DeadLetters, Policies,
	// RelayStats, Timers, Lineage) are left nil here — their routes are simply not
	// registered — and a consumer wires the ones they expose.
	adminMux := http.NewServeMux()
	stdlib.AdminRoutes{Svc: svc}.Customize(adminMux, httpcore.WithMeterProvider[*http.ServeMux](meterProvider))
	mux.Handle("/admin/", requireAdminToken(adminMux, os.Getenv("ADMIN_TOKEN"), logger))

	srv := &http.Server{
		Addr:    ":8080",
		Handler: mux,
		// ReadHeaderTimeout bounds the HEADERS only. ⚠ It does NOT bound the
		// body, so on its own it leaves a client free to send headers promptly
		// and then dribble — or simply never finish — the body it declared.
		ReadHeaderTimeout: 5 * time.Second,
		// ReadTimeout bounds the WHOLE request, headers plus body, and is the
		// backstop that makes that hold finite. It matters more since the
		// transport gained its inbound body cap: capping means reading the body
		// to completion before parsing, where the uncapped path let json.Decoder
		// stop at the first complete value and return.
		//
		// The adapters carry their own bound for that read — 30s by default, see
		// stdlib.WithBodyReadTimeout / gin.WithBodyReadTimeout — but it covers
		// only routes that decode a body, and only while the cap is enabled.
		// ReadTimeout covers every route unconditionally, so set both.
		//
		// ⚠ Keep this NO SHORTER than the adapters' BodyReadTimeout. The adapter
		// arms its deadline as now+d when the body read begins, overwriting the
		// whole-request deadline net/http set from this field; a smaller value
		// here would be silently extended for the duration of that read.
		ReadTimeout: 30 * time.Second,
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
	// 1. Stop the background worker (the relay) by cancelling its context.
	stopWorkers()

	// 2. Drain in-flight HTTP requests with a bounded deadline.
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelDrain()
	httpErr := srv.Shutdown(drainCtx)

	// 3. Release every resource holder in reverse registration order — driver
	//    (which drains its owned scheduler), then pool, then eventing, then the
	//    meter provider — joining any errors. Bound this drain too.
	releaseCtx, cancelRelease := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelRelease()
	releaseErr := shutdown.Shutdown(releaseCtx)

	return errors.Join(httpErr, releaseErr)
}
