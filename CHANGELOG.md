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

### Breaking changes (pre-v0.1.0 — no stability promise)

- **`persistence.NewCachingInstanceStore` now requires a `cache.Provider` argument**
  (previously `runtime/kernel.NewCachingInstanceStore` took no provider). The type also
  moved from `runtime/kernel` to `persistence`. Supply `hotcache.New()` (the default) or any
  other `cache.Provider` from `persistence/cache/{hotcache,ottercache,rediscache,memcache}`.
  Consumers using `NewDurableProvider` / `NewMySQLDurableProvider` / `NewSQLiteDurableProvider`
  are unaffected — caching is wired automatically by the provider constructors.

- **`runtime.NewProcessDriver` is now all-optional.** The two required positional
  arguments (`cat action.Catalog`, `store kernel.InstanceStore`) have been replaced with
  functional options. A zero-argument call — `d, _ := runtime.NewProcessDriver()` — gives
  a fully usable in-memory, non-durable driver backed by `action.DefaultCatalog()`,
  `kernel.NewMemInstanceStore()`, and `runtime.DefaultDefinitionRegistry()`. A DEBUG log
  at construction reports the wired collaborators and advises how to go durable.
  - Supply your own catalog via `runtime.WithActionCatalog(cat)`.
  - Supply a durable store via `runtime.WithInstanceStore(store)`.
  - Supply an explicit definition registry via `runtime.WithDefinitions(reg)` (passing
    `nil` is a no-op — the default stands).
  - Populate the default catalog with `action.Register(name, fn)`,
    `action.RegisterFunc(name, fn)`, `action.MustRegister`, or `action.MustRegisterFunc`.
  - Populate the default definition registry with `runtime.RegisterDefinition(def)` or
    `runtime.MustRegisterDefinition(def)`.

- **`InstanceStore` / `MemInstanceStore` / `CachingInstanceStore` renames (breaking).**
  All references to the old names must be updated:
  - `kernel.Store` → `kernel.InstanceStore`
  - `kernel.MemStore` → `kernel.MemInstanceStore`; `kernel.NewMemStore(` → `kernel.NewMemInstanceStore(`; `kernel.MemStoreOption` → `kernel.MemInstanceStoreOption`
  - `kernel.CachingStore` → `kernel.CachingInstanceStore`; `kernel.NewCachingStore(` → `kernel.NewCachingInstanceStore(`; `kernel.CachingStoreOption` → `kernel.CachingInstanceStoreOption`
  - `persistence.Store` (the façade interface) → `persistence.InstanceStore`

- **`kernel.Token` → `kernel.Version`** — the optimistic-concurrency version scalar is
  now named `Version` throughout the kernel package.

- **`kernel.Outcome` → `kernel.ChainOutcome`** — the chain-outcome type is renamed to
  avoid colliding with the generic word "outcome".

- **`kernel.Ownership` → `kernel.InstanceOwnership`** — the ownership port interface is
  renamed for clarity.

- **`kernel.Publisher` → `kernel.OutboxPublisher`** (and `persistence.Publisher` alias
  → `persistence.OutboxPublisher`) — the outbox-publish port is renamed to be explicit
  about its role.

- **`action.Retryabler` → `action.RetryableError`** — the retry-classification interface
  is renamed to follow Go error-interface naming conventions.

- **`action.Default()` → `action.DefaultCatalog()`** — the zero-argument catalog accessor
  is renamed to be unambiguous.

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

- **Default `DefinitionRegistry` for zero-config call activities (ADR-0097, follows ADR-0096).**
  `runtime.NewProcessDriver()` now wires `runtime.DefaultDefinitionRegistry()` automatically,
  giving `KindCallActivity` nodes a working registry without any `WithDefinitions` call —
  symmetric with how `action.DefaultCatalog()` works for service tasks. New API:
  - `runtime.DefaultDefinitionRegistry() *kernel.MemDefinitionRegistry` — returns the
    process-global mutable registry.
  - `runtime.RegisterDefinition(def *model.ProcessDefinition) error` — registers `def`
    into the global registry under both `"<ID>"` and `"<ID>:<Version>"`. Bare `"<ID>"`
    resolves to the most recently registered version. Returns `ErrDefinitionExists` if the
    exact `"<ID>:<Version>"` is already registered.
  - `runtime.MustRegisterDefinition(def *model.ProcessDefinition)` — panics on error
    (init-time wiring, mirrors `action.MustRegister`).
  - `kernel.MemDefinitionRegistry` — the new concurrency-safe, mutable sibling of the
    immutable `MapDefinitionRegistry`. Obtain with `kernel.NewMemDefinitionRegistry()`.
    New sentinel errors: `kernel.ErrNilDefinition`, `kernel.ErrEmptyDefinitionID`,
    `kernel.ErrDefinitionExists`.
  - **`runtime.WithDefinitions(nil)` is now a no-op** (nil-ignored, matching
    `WithActionCatalog` / `WithInstanceStore`). A nil argument no longer clobbers the
    default registry. Passing a non-nil registry overrides the default, as before. Tests
    needing a fully isolated, empty registry should pass
    `WithDefinitions(kernel.NewMemDefinitionRegistry())`.

- **Persistence** — SQL backends for **PostgreSQL 17**, **MySQL 8.0+**, and **SQLite**
  (`modernc.org/sqlite`, pure-Go, WAL, single-writer; single-node/test/embedded only) behind
  ONE neutral `internal/persistence/store` parametrized by `internal/persistence/dialect`
  (ADR-0081/0082). Capability interfaces `Notifier` (LISTEN/NOTIFY) and `Locker` (distributed
  advisory lock) are opt-in per dialect. Facade constructors `persistence.Open{Postgres,MySQL,SQLite}`
  and `persistence.Migrate{Postgres,MySQL,SQLite}` (plus a public `persistence.Migrator`).
  Optimistic-concurrency (CAS) writes, a transactional **outbox** relay with poison isolation +
  DLQ + redrive, hot-path caching (see below), and data-retention pruners.

- **Persistence caching layer (ADR-0099)** — a neutral `persistence/cache` port (`Cache`,
  optional `ValueCache` capability, `Provider`, generic `Codec[V]`) with **four swappable
  adapter subpackages**: `persistence/cache/hotcache` (`github.com/samber/hot`, **default**,
  in-memory), `persistence/cache/ottercache` (`github.com/maypok86/otter/v2`, in-memory
  alternative), `persistence/cache/rediscache` (`github.com/redis/go-redis/v9`, distributed),
  and `persistence/cache/memcache` (`github.com/bradfitz/gomemcache`, distributed). Each
  adapter lives in its own subpackage so its library dependency is optional. `CachingInstanceStore`
  is relocated from `runtime/kernel` into `persistence` and re-substrated onto the `Cache`
  port (all correctness-bearing behavior preserved: ownership gate, per-instance keyed locks,
  evict-on-`ErrConcurrentUpdate`, `AlwaysOwn` single-replica `Warn`, `Release`-evict-first).
  A new `CachingTaskStore` provides read-through / write-through point-read caching over
  `humantask.TaskStore` (set-wide queries `AssignedTo`/`ClaimableBy` are uncached in v1).
  Caching is **default-on** on all three `DurableProvider` constructors (`NewDurableProvider`,
  `NewMySQLDurableProvider`, `NewSQLiteDurableProvider`) using `hotcache` in-memory, `AlwaysOwn`
  + one-time Warn, instance TTL 5m, human-task TTL 30s. New `DurableOption`s:
  `WithCacheProvider`, `WithInstanceCacheProvider`, `WithHumanTaskCacheProvider`,
  `WithDurableInstanceCacheOwnership`, `WithDurableInstanceCacheTTL`,
  `WithDurableHumanTaskCacheTTL`, and `WithoutCache` (escape hatch). Definition caching is
  deferred; human-task query caching (`AssignedTo`/`ClaimableBy`) is deferred.

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
