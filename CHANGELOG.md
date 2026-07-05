# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

> **Pre-1.0 notice.** Until a `v1.0.0` tag is cut, the public API may change between
> minor versions. See [`STABILITY.md`](STABILITY.md) for the stability and deprecation policy.

## [Unreleased]

The first tagged release (`v0.1.0`) will be cut from this section: it is the inaugural
feature set, built across ADRs 0001–0095. Because nothing has shipped yet, everything below
is **Added** — the list describes the engine as it stands today, not a delta from a prior
release.

### Added

- **Token-based, BPMN-inspired engine core** — process definitions across 19 typed node
  kinds (start/end/terminate/error events, service/user/business-rule/send/receive tasks,
  exclusive/parallel/inclusive/event-based gateways, sub-process, call activity, boundary
  and intermediate events, event sub-processes), token execution, and `expr-lang`-driven
  gateway routing. The vocabulary is BPMN-inspired, not BPMN-compatible; there is no BPMN2
  XML loader.

- **Authoring** — the `definition` root package is a thin aggregator with two entry points:
  `definition.NewBuilder(id, version)` (fluent Go builder with one `Add<Kind>` per node kind)
  and `definition.NewLoader(r io.Reader)` (YAML). Types, validation and serialization live in
  `definition/model`; sequence flows in `definition/flow`; node constructors in
  `definition/{event,gateway,activity}`; the fluent builder in `definition/build`; the
  deserialization registration bundle in `definition/kinds`.

- **Service actions** — a name-resolved catalog (`action.Catalog`, `action.Action`,
  `action.ActionFunc`, `action.MapCatalog`, `action.Registry`) with definition-scoped and
  node-inline registration (three-tier resolution: inline → scoped → global). Built-in
  actions: `httpcall` (10 MiB body cap by default via `WithMaxResponseSize`), `email`,
  `transform`, and `logaction`. Service-action invocations time out after 30s by default
  (`runtime.WithActionTimeout`); a timeout surfaces as a retryable failure.

- **Persistence** — SQL backends for **PostgreSQL 17**, **MySQL 8.0+**, and **SQLite**
  (`modernc.org/sqlite`, pure-Go, WAL, single-writer; single-node/test/embedded only) behind
  ONE neutral `internal/persistence/store` parametrized by `internal/persistence/dialect`
  (ADR-0081/0082). Capability interfaces `Notifier` (LISTEN/NOTIFY) and `Locker` (distributed
  advisory lock) are opt-in per dialect. Facade constructors `persistence.Open{Postgres,MySQL,SQLite}`
  and `persistence.Migrate{Postgres,MySQL,SQLite}` (plus a public `persistence.Migrator`).
  Optimistic-concurrency (CAS) writes, a transactional **outbox** relay with poison isolation +
  DLQ + redrive, hot-path caching (`kernel.CachingStore`, `kernel.CachingDefinitionRegistry`),
  and data-retention pruners.

- **Runtime driver** — `runtime.ProcessDriver` wires the engine to persistence, scheduling,
  and actions; supporting pieces live in `runtime/{kernel,view,chain,task,signal,calllink,monitor}`.
  Stateful constructors fail fast, returning `(T, error)` and wrapping `kernel.ErrNilDependency`
  on a nil required dependency rather than panicking later.

- **Scheduling / waits** — `gocron`-driven timers, deadlines, and in-wait reminder actions;
  multi-replica timer exclusivity via advisory-lock leader election.

- **Resilience** — engine-modeled retry with backoff/jitter, incident creation on exhaustion,
  catch-flow handling, and a retryable-error contract (`action.IsRetryable` / `action.NonRetryable`).

- **Compensation** — optional per-node compensation actions, scope-targeted rollback, and
  best-effort cancel actions on instance cancellation.

- **Authorization** — pluggable `authz.Authorizer` with a casbin baseline (role,
  resource-privilege, and attribute/variable-based evaluation), a DB-backed policy adapter,
  and a runtime policy admin.

- **Eventing** — vendor-neutral eventing abstraction over watermill (in-process GoChannel
  publisher by default; broker wiring documented in `docs/eventing-brokers.md`), transactional
  `SendTask` messaging via the outbox (`message.*` topics), and event-driven process-instance
  chaining (`chain.Chainer`).

- **HTTP transports** — mountable route groups over a shared pure root:
  `transport/http/httpcore` (pure per-endpoint functions, DTOs with `go-playground/validator/v10`
  validation, `ClassifyError` with 5xx body redaction, `NewInstanceView`, health-probe
  evaluation, static-route-template observability, and the generic `RouteCustomizer[R]` /
  `CustomizeOption[R]` / `CustomizeConfig[R]` seam), plus three native adapters —
  `transport/http/stdlib` (`*http.ServeMux`), `transport/http/gin`, and `transport/http/fiber`
  (fiber v3). Each adapter exposes `InstanceRoutes`, `TaskRoutes`, `MessageRoutes`,
  `AdminRoutes`, and `HealthRoutes` structs plus `Mount`/`MountHealth` conveniences. Admin
  routes are **default-absent by composition** — they exist only when a consumer mounts
  `AdminRoutes` (with the desired admin-port fields set) on a router group their own auth
  middleware already protects. Import isolation: stdlib pulls no third-party transport
  dependency; gin/fiber consumers pull only their respective framework.

- **Service façade** — `service.Service` is the single transport-neutral application seam;
  the HTTP adapters are thin translators over it.

- **Observability** — OpenTelemetry metrics + traces and `slog` logging across runtime,
  transports, scheduling, eventing, and the persistence relay; SLI gauges/counters
  (`wrkflw_outbox_*`, `wrkflw_timers_armed`, `wrkflw_timer_fired_total`,
  `wrkflw_action_failures_total{action,retryable}`), `/healthz` + `/readyz` handlers, and
  reference `docs/dashboards/`, `docs/runbooks/`, and `docs/observability.md`.

- **Operability** — graceful `runtime.ShutdownGroup`, opt-in `persistence.WarnUnsafeConfig`,
  a `processtest` consumer test harness, example reference wiring under `examples/`, and
  `STABILITY.md` / `docs/production-checklist.md`.

- **Project** — Apache-2.0 license, contributor and security policies, and a GitHub Actions
  CI pipeline (build, race tests, lint, `gosec`/`bodyclose`/`errorlint`, vulnerability scan,
  CodeQL).

[Unreleased]: https://github.com/zakyalvan/krtlwrkflw/commits/main
