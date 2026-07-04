# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

> **Pre-1.0 notice.** Until a `v1.0.0` tag is cut, the public API may change between
> minor versions. See [`STABILITY.md`](STABILITY.md) for the stability and deprecation policy.

## [Unreleased]

The first tagged release (`v0.1.0`) will be cut from this section. It captures the engine as
built across ADRs 0001–0095.

### Breaking

- **BREAKING: gRPC transport removed (ADR-0094).** `transport/grpc/` and the generated
  `transport/grpc/workflowpb/` stubs are deleted. `RegisterWorkflowServiceServer`,
  `NewSecureServer`, `NewMethodAuthInterceptor`, and the full proto/buf toolchain are gone.
  The `google.golang.org/grpc`, `google.golang.org/protobuf`, and
  `google.golang.org/genproto/googleapis/rpc` dependencies are removed from `go.mod`.
  Migrate to the HTTP surface (see below).

- **BREAKING: `transport/rest` package removed (ADR-0095).** `rest.NewHandler`,
  `rest.NewHealthHandler`, `rest.WithAdminMiddleware`, and all `rest.*` option/type names
  are gone. Replace with the three new adapter subpackages (see Added section below).

### Added

- **`transport/http/{httpcore,stdlib,gin,fiber}` — composable multi-framework HTTP adapters
  (ADR-0095, ADR-0094).** The transport layer is redesigned as three native adapter
  subpackages over a shared pure root:
  - `transport/http/httpcore` — shared pure-endpoint functions, DTOs (with
    `go-playground/validator/v10` struct-tag validation), `ClassifyError`, `NewInstanceView`,
    `EvaluateReady`/`EvaluateLive`, and `Instrumentation.Observe` (static route template, no
    `r.Pattern` dependency). Exposes the generic `RouteCustomizer[R]` seam and
    `CustomizeOption[R]` / `CustomizeConfig[R]`.
  - `transport/http/stdlib` — native `net/http` adapter.
    Groups: `InstanceRoutes`, `TaskRoutes`, `MessageRoutes`, `AdminRoutes`, `HealthRoutes`.
    Convenience: `Mount(mux, svc)`, `MountHealth(mux, checks...)`.
  - `transport/http/gin` — native gin adapter (same group set + `WithMiddleware`).
  - `transport/http/fiber` — native fiber v3 adapter (same group set + `WithMiddleware`).

  Key properties of the new design:
  - Each group is an **exported struct** that carries only its dependencies; all
    mount-time customisation flows through `Customize(router, opts...)`.
  - **Admin-by-composition** — admin endpoints are **default-absent** (not default-deny).
    They appear only if the consumer mounts `AdminRoutes` on a consumer-secured router
    group. Safer than the old `WithAdminMiddleware` 403 gate and idiomatic per framework.
  - **5xx error-body redaction** — `ClassifyError` returns only a sentinel code
    (`{"error":"internal_error"}`) for 5xx responses; the raw error is logged, never
    included in the body. 4xx responses retain their descriptive `message`.
  - **Static route template observability** — span/metric labels use the template known
    at registration time; no router-populated request field; identical labels across all
    three frameworks.
  - **Dependency isolation** — stdlib consumers pull no third-party transport dep.
    gin consumers add only gin; fiber consumers add only fiber.

  Migration:
  ```go
  // Before
  mux.Handle("/workflow/", http.StripPrefix("/workflow", rest.NewHandler(svc)))

  // After (stdlib)
  stdlib.Mount(mux, svc)
  stdlib.MountHealth(mux, dbCheck)
  ```

- **`github.com/gin-gonic/gin` v1.12.0** added as a dependency of `transport/http/gin`.
- **`github.com/gofiber/fiber/v3` v3.4.0** added as a dependency of `transport/http/fiber`.
- **`github.com/go-playground/validator/v10`** added as a direct dependency of
  `transport/http/httpcore` (was already an indirect dep of gin).

### Changed
- **BREAKING: stateful/service constructors now fail fast with `(T, error)` (ADR-0083).**
  Constructors that own state and take a required, non-nilable dependency validate their
  arguments and return a wrapped `ErrNilDependency` instead of accepting a nil and panicking
  later. Affected in `runtime`: `NewRunner`, `NewTaskService`, `NewCachingStore`,
  `NewCachingDefinitionRegistry`, `NewSignalBus`, `NewCallNotifier`, `NewLineageReader`
  (each now returns `(*T, error)`); the `persistence` facade wrappers and the neutral
  `internal/persistence/store` constructors (`New`, `NewCallLinkStore`, `NewChainLinkStore`,
  `NewDeduper`, `NewDefinitionStore`, `NewLister`, `NewPruner`, `NewTimerStore`, `NewRelay`)
  likewise gained an `error` result and reject a nil `conn`/`dialect`. Two package-scoped
  sentinels back this: `runtime.ErrNilDependency` and `internal/persistence/store.ErrNilDependency`
  (each wrapped with the offending argument name). Pure value/DTO/trigger constructors and
  `model.NewDefinition` are intentionally unchanged. Migration: capture and handle the new
  `error` at each construction site.
- **BREAKING: `runtime.NewChainer` no longer panics on a nil `starter`/`policy`** — it returns
  `(*Chainer, error)` wrapping `ErrNilDependency` (ADR-0083). Migration: handle the error rather
  than recovering a panic.
- **BREAKING: the three `runtime.NewMemStore*` constructors collapsed into one options
  constructor** `NewMemStore(opts ...MemStoreOption) (*MemStore, error)` (ADR-0083).
  `NewMemStoreWithCallLinks`/`NewMemStoreWithTimers` were REMOVED; use
  `runtime.WithCallLinks(cl)` / `runtime.WithTimers(mts)` (each returns an error if passed nil),
  which also closes the previous can't-set-both gap.
- **BREAKING: the `runtime.Runner` option `WithCallLinks` was renamed to `WithCallLinkStore`**
  (parallels `WithTimerStore`), freeing the `WithCallLinks` name for the new `MemStore` option
  above. Migration: rename `runtime.WithCallLinks(cl)` in Runner wiring to
  `runtime.WithCallLinkStore(cl)`.
- **BREAKING: `engine.NewActionFailedJittered` was removed; `engine.NewActionFailed` gained a
  variadic option** `NewActionFailed(at, commandID, errMsg, retryable, opts ...ActionFailedOption)`
  with `engine.WithJitter(fraction)` (ADR-0083). `ActionFailed` remains a value type (no error).
  Migration: `NewActionFailedJittered(…, j)` → `NewActionFailed(…, engine.WithJitter(j))`; drop the
  option when `j == 0`.
- **BREAKING: `casbinauthz` collapsed to a single source-options constructor**
  `NewCasbinAuthorizer(opts ...Option) (authz.Authorizer, io.Closer, error)` with
  `FromEnforcer` / `FromStrings` / `FromDB` (ADR-0083). The old
  `NewCasbinAuthorizer(e)` / `NewCasbinAuthorizerFromStrings` / `NewCasbinAuthorizerFromDB` were
  REMOVED. Exactly one source must be supplied — zero sources returns `ErrNoAuthorizerSource`,
  two or more returns `ErrMultipleAuthorizerSources`. Migration: wrap the source in the matching
  `From*` option (e.g. `NewCasbinAuthorizer(FromStrings(model, policy))`).
- **BREAKING: `model.DefinitionBuilder` is now an interface, and `model.DefinitionLoader` was
  introduced (ADR-0084).** `DefinitionBuilder` is the full authoring surface; `DefinitionLoader`
  is the reduced surface (everything except `Add`/`AddX`/`Connect`) for a definition whose
  structure is already declared. `NewDefinition` now returns `DefinitionBuilder` (was a
  `*DefinitionBuilder` struct pointer). `ParseYAML`/`LoadYAML` now return
  `(DefinitionLoader, error)` with structural validation deferred to `Build()` — a YAML-loaded
  definition can register its (non-serializable) scoped actions and then `Build()`. The
  established actions-first fluent idiom continues to compile. Migration: a YAML caller now does
  `ld, err := model.LoadYAML(r); def, err := ld.Build()`; code that stored `*model.DefinitionBuilder`
  uses the interface `model.DefinitionBuilder`.
- **BREAKING: `Deduper.Seen` dropped its explicit driver-transaction parameter.** The new
  signature is `Seen(ctx, subscriber, messageID)` — it joins the ambient transaction from
  ctx, or commits its own leaf tx when none is present. The separate `MySQLDeduper` interface
  was REMOVED; `NewMySQLDeduper` is retained but now returns the unified `persistence.Deduper`
  (ADR-0081). Migration: drop the `tx` argument at every `Seen` call site.
- **BREAKING (default behaviour): service actions now time out after 30s by default.** New
  `runtime.WithActionTimeout(d)` bounds each action invocation; pass a larger `d` for legitimately
  long actions or `runtime.WithActionTimeout(0)` to disable. A timed-out action surfaces as a
  retryable failure (ADR-0076).
- **`action/httpcall` now caps response/request bodies at 10 MiB by default.** New
  `httpcall.WithMaxResponseSize(n)` raises/lowers the cap; `n <= 0` disables it. Over-cap reads fail
  non-retryable with the new `httpcall.ErrBodyTooLarge` (ADR-0076).

### Security
- Enabled `gosec`, `bodyclose`, and `errorlint` in CI; triaged all findings to zero with documented
  rationale for each suppression (ADR-0077).

### Added
- **model builder: `WithEligibilityPrivileges` sets casbin resource-privilege authz on UserTasks.**
  `model.WithEligibilityPrivileges(privs ...string)` is a new `userTaskOption` that populates
  `AuthzSpec.Privileges` on the `AwaitHuman` command. Previously this field was only settable by
  bypassing the builder (hand-inserting a `humantask.HumanTask`). Privilege tokens are space-separated
  "object action" pairs (e.g. `"finance-task claim"`) evaluated by a casbin-backed `Authorizer` at
  `TaskService.Claim` time. YAML authoring uses the `eligibilityPrivileges` key.
- **`persistence.Relay` now exposes `OutboxStats`.** The method
  `OutboxStats(ctx context.Context) (runtime.OutboxStats, error)` is part of the
  `persistence.Relay` interface; callers no longer need a `runtime.OutboxStatsReader`
  type assertion to read pending/dead/age stats from a relay obtained through the facade.
- **SQLite backend (ADR-0082).** `persistence.OpenSQLite(ctx, db *sql.DB, opts ...Option) (Store, error)`,
  `persistence.MigrateSQLite(ctx, db)`, and `persistence.NewSQLiteAdvisoryLockOwnership()` (fail-loud —
  every acquire returns `dialect.ErrUnsupported`). Backend uses `modernc.org/sqlite` (pure-Go), WAL mode,
  single-writer serialisation (`db.SetMaxOpenConns(1)` required), and poll-only relay (no LISTEN/NOTIFY).
  Single-node/test/embedded use only; use Postgres or MySQL for multi-replica.
  The facade also exposes `NewSQLite*` constructors (relay/timer/lister/call-link/chain-link/call-notifier/definition/pruner).
- **Store unification + dialect abstraction (ADR-0081).** The former `internal/persistence/{postgres,mysql}`
  packages are replaced by ONE neutral `internal/persistence/store` parametrized by
  `internal/persistence/dialect` (Postgres/MySQL/SQLite). Capability interfaces `Notifier` (LISTEN/NOTIFY)
  and `Locker` (distributed advisory lock) are opt-in so each dialect declares only what it supports.
  Two-axis model: access mechanism (pgx vs database/sql) × SQL dialect.
- **Ops-visibility surface (ADR-0078).**
  - SLI metrics: observable gauges `wrkflw_outbox_pending`, `wrkflw_outbox_dead`,
    `wrkflw_outbox_oldest_pending_age_seconds`, `wrkflw_timers_armed` (via consumer-wired
    `runtime.NewOutboxStatsCollector` / `NewTimerStatsCollector`); counters `wrkflw_timer_fired_total`
    and `wrkflw_action_failures_total{action,retryable}`.
  - `persistence.NewRelayBacklogCheck` readiness probe (DLQ/pending thresholds, default-disabled).
  - Admin endpoints (REST, behind the default-deny gate): relay stats, armed timers, instance
    lineage, and a failure `category` on dead-letters (`runtime.ClassifyDeadLetter`).
  - `OutboxStats`/`TimerStats` reads and single-hop instance-lineage reads on both the Postgres and
    MySQL backends; `runtime.NewLineageReader` assembler.
  - Reference `docs/dashboards/` (Grafana + Prometheus alerts), `docs/runbooks/`, and `docs/observability.md`.
- The engine as built across ADRs 0001–0078 (inaugural feature set):
- **Token-based BPMN-inspired engine core** — process definitions (19 typed node kinds:
  start/end events, service/user/business-rule/send/receive tasks, exclusive/parallel/inclusive
  and event-based gateways, sub-process, call activity, boundary and intermediate events,
  event sub-processes), token execution, and `expr-lang`-driven gateway routing.
- **Authoring** — Go `DefinitionBuilder` (with per-kind `AddX` fluent methods) and a YAML loader.
- **Persistence** — SQL backends for **PostgreSQL 17**, **MySQL 8.0+**, and **SQLite** (`modernc.org/sqlite`, single-node/test/embedded) behind shared ports via the neutral store + dialect abstraction (ADR-0081/0082),
  optimistic-concurrency (CAS) writes, transactional **outbox** relay with poison isolation + DLQ +
  redrive, hot-path caching (`CachingStore`, `CachingDefinitionRegistry`), and data-retention pruners.
- **Scheduling** — `gocron`-driven timers, deadlines (SLA), and in-wait actions; multi-replica timer
  exclusivity via advisory-lock leader election.
- **Resilience** — engine-modeled retry with backoff/jitter, incident creation on exhaustion,
  catch-flow handling, and a retryable-error contract (`action.IsRetryable`).
- **Compensation** — optional per-node compensation actions and scope-targeted rollback.
- **Authorization** — pluggable `Authorizer` with a casbin baseline (role, resource-privilege, and
  attribute/variable-based evaluation) and a DB-backed policy adapter + policy admin.
- **Eventing** — vendor-neutral eventing abstraction over watermill (in-process GoChannel publisher),
  transactional `SendTask` messaging, and event-driven process-instance chaining.
- **Service actions** — a name-resolved catalog plus built-in actions: `httpcall`, `email`,
  `transform`, and `logaction`; definition-scoped and inline action registration.
- **Transports** — mountable HTTP route groups (stdlib `*http.ServeMux`, gin, fiber v3)
  with request validation, structured error mapping, admin/DLQ/policy endpoints, keyset-paginated
  listing, and instance snapshot/actionable projections (see ADR-0094/0095 for the new shape).
- **Observability** — OpenTelemetry metrics + traces and `slog` logging across runtime, transports,
  scheduling, eventing, and the persistence relay; `/healthz` + `/readyz` handlers.
- **Operability** — graceful `ShutdownGroup`, example reference wiring under `examples/`, and a
  `STABILITY.md` policy.
- **Project** — Apache-2.0 license, contributor and security policies, and a GitHub Actions CI
  pipeline (build, race tests, lint, vulnerability scan, CodeQL).

[Unreleased]: https://github.com/zakyalvan/krtlwrkflw/commits/main
