# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

> **Pre-1.0 notice.** Until a `v1.0.0` tag is cut, the public API may change between
> minor versions. See [`STABILITY.md`](STABILITY.md) for the stability and deprecation policy.

## [Unreleased]

The first tagged release (`v0.1.0`) will be cut from this section. It captures the engine as
built across ADRs 0001–0075.

### Added
- **Token-based BPMN-inspired engine core** — process definitions (19 typed node kinds:
  start/end events, service/user/business-rule/send/receive tasks, exclusive/parallel/inclusive
  and event-based gateways, sub-process, call activity, boundary and intermediate events,
  event sub-processes), token execution, and `expr-lang`-driven gateway routing.
- **Authoring** — Go `DefinitionBuilder` (with per-kind `AddX` fluent methods) and a YAML loader.
- **Persistence** — SQL backends for **PostgreSQL 17** and **MySQL 8.0+** behind shared ports,
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
