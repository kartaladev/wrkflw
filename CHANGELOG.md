# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

> **Pre-1.0 notice.** Until a `v1.0.0` tag is cut, the public API may change between
> minor versions. See [`STABILITY.md`](STABILITY.md) for the stability and deprecation policy.

## [Unreleased]

The first tagged release (`v0.1.0`) will be cut from this section. It captures the engine as
built across ADRs 0001–0082.

### Changed
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
  - Admin endpoints (REST + gRPC) behind the default-deny gate: relay stats, armed timers, instance
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
- **Transports** — mountable REST (`http.Handler` factories) and gRPC (`ServiceRegistrar`) surfaces
  with request validation, structured error mapping, admin/DLQ/policy endpoints, keyset-paginated
  listing, instance snapshot/actionable projections, and fail-closed auth helpers.
- **Observability** — OpenTelemetry metrics + traces and `slog` logging across runtime, transports,
  scheduling, eventing, and the persistence relay; `/healthz` + `/readyz` handlers.
- **Operability** — graceful `ShutdownGroup`, example reference wiring under `examples/`, and a
  `STABILITY.md` policy.
- **Project** — Apache-2.0 license, contributor and security policies, and a GitHub Actions CI
  pipeline (build, race tests, lint, vulnerability scan, CodeQL).

[Unreleased]: https://github.com/zakyalvan/krtlwrkflw/commits/main
