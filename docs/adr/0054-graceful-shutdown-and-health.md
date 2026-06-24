# 54. Unified graceful shutdown, health/readiness handlers, and an AlwaysOwn caching guard

- Status: Accepted
- Date: 2026-06-25

## Context

The production-readiness audit found three gaps in the library's operational surface.

1. **No aggregated shutdown.** The engine's long-running collaborators stop in two
   different idioms: the background workers — the outbox relay
   (`postgres.Relay.Run(ctx)`), the call-activity notifier
   (`runtime.CallNotifier.Run(ctx)`), and a chainer runner — exit on context
   cancellation, while the resource holders expose `Close()` (`scheduling.Scheduler`,
   the `AdvisoryLockOwnership` connection, the casbin closer, the eventing GoChannel
   closer, the `pgxpool.Pool`). A consumer had to remember every closer and the right
   order by hand; there was no single, error-collecting teardown call. Not one of the
   nine `examples/*/main.go` files demonstrated signal handling or graceful drain.

2. **No health/readiness endpoint.** The library shipped REST handlers for the workflow
   API but nothing for an orchestrator (Kubernetes, a load balancer) to probe liveness
   or wire readiness to the database.

3. **`AlwaysOwn` + caching is a silent multi-replica footgun.** `runtime.AlwaysOwn`
   unconditionally reports ownership, so a `CachingStore` under it caches every instance.
   Across two replicas both would serve their own stale snapshot and could fire a routing
   decision before the version-CAS rejected the write (ADR-0020). The hazard was
   documented only in passing.

The constraints are the usual library-first ones (CLAUDE.md): the consumer owns the
process and goroutine startup; transports are mounted, not served by us; the engine core
stays import-pure; no framework may be imposed.

## Decision

Add three small, composable pieces — no god-object, no framework.

- **`runtime.ShutdownGroup`** (new `runtime/shutdown.go`). A zero-value-ready aggregator.
  `Add(ShutdownFunc)` registers a `func(context.Context) error`; `AddCloser(io.Closer)`
  adapts the many `Close() error` resource holders. `Shutdown(ctx)` invokes every
  registered shutdown in **reverse registration order** (stacked-defer discipline: a
  component registered later usually depends on one registered earlier, so it tears down
  first), runs **all** of them even if one errors, and returns the aggregate via
  `errors.Join`. It is idempotent and safe for concurrent `Add`. It deliberately covers
  only the resource holders; the `Run(ctx)` workers keep their idiomatic stop story —
  the consumer cancels the context they were started with. This respects the
  consumer-owns-lifecycle rule: graceful drain becomes one well-documented call without
  the library seizing goroutine startup.

- **Health/readiness handlers** (new `transport/rest/health.go`). A `HealthCheck`
  interface (`Name() string`, `Check(ctx) error`) and `NewHealthHandler(checks...)`
  returning an `http.Handler` — same http.Handler-factory idiom as `NewHandler`. It
  serves `GET /healthz` (liveness: always `200`, runs no checks, so a degraded
  dependency never makes the orchestrator kill the pod) and `GET /readyz` (readiness:
  runs every check with the request context, `200` when all pass or `503` with a JSON
  body naming each failing check). `HealthCheckFunc(name, fn)` adapts an inline probe.

- **`persistence.NewPingCheck(pool, opts...)`** (new `persistence/health.go`). A
  ready-made readiness probe over `*pgxpool.Pool` that calls `pool.Ping(ctx)`. It is
  defined in `persistence` (not `transport/rest`) and **structurally** satisfies
  `rest.HealthCheck`, so the pgx dependency stays out of the transport package and
  `persistence` keeps no transport import — the consumer wires the two together.

- **`AlwaysOwn` guard.** `NewCachingStore` now logs a one-time `Warn` (via a new
  `WithCacheLogger` option, defaulting to `slog.Default()`) when constructed with an
  `AlwaysOwn` owner, and the doc comments on both `AlwaysOwn` and `CachingStore` state
  loudly that the pairing is single-replica only and that multi-replica needs
  `persistence.NewAdvisoryLockOwnership`. A cheap one-time log was chosen over a hard
  error: `AlwaysOwn` + caching is the *correct* and intended configuration for
  single-replica embedding (the majority case), so failing construction would punish the
  common path; the warning makes a misconfiguration visible without breaking it.

- **Reference example** (`examples/production_wiring/main.go`). Constructs engine +
  scheduler + relay, mounts the REST and health handlers on the consumer's own
  `*http.Server`, starts the relay goroutine, and on `SIGINT`/`SIGTERM` cancels the
  worker context, gracefully `srv.Shutdown(ctx)`s, then `ShutdownGroup.Shutdown(ctx)`s
  the resource holders. It runs with or without `DATABASE_URL` (Postgres store + relay +
  DB-ping readiness when set; in-memory fallback otherwise) so it always builds.

## Consequences

- A consumer now has a one-call graceful teardown and mountable liveness/readiness
  probes, closing the operational gaps without adopting any framework or surrendering
  process ownership.
- The engine core is untouched: all additions live in `runtime`, `transport/rest`,
  `persistence`, `examples`, and docs. `engine/` and `model/` keep a zero diff.
- `NewCachingStore` gained one option and a construction-time side effect (a log line);
  existing call sites compile and behave unchanged. The warning is emitted at most once
  per CachingStore, only under `AlwaysOwn`.
- `ShutdownGroup` does not start or supervise goroutines — a consumer who forgets to
  cancel the worker context will still leak those goroutines. This is intentional:
  owning goroutine startup is the consumer's job, and conflating it into the shutdown
  aggregator would re-impose the lifecycle ownership the library refuses to take.
- `PingCheck` satisfies `rest.HealthCheck` structurally, not by an explicit interface
  assertion across packages, so a future signature change to `HealthCheck` would be
  caught by the consumer's wiring (and the example's compile) rather than at the
  definition site.
