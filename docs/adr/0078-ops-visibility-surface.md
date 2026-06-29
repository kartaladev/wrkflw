# 0078. Ops-visibility surface (SLI metrics, health probe, admin endpoints, lineage)

Status: **Accepted — 2026-06-30.**
Design doc: `docs/specs/2026-06-30-ops-visibility-design.md`.
Plan: `docs/plans/2026-06-30-ops-visibility.md`.
Relates to: ADR-0019 (observability boundary), ADR-0052 (pruners), ADR-0054 (health/shutdown),
ADR-0045 (chaining lineage), ADR-0024/0025 (call-activity links).

## Context

The 2026-06-30 production-readiness audit found ops visibility the weakest cluster. The engine emitted
throughput metrics but lacked the SLIs an operator pages on (DLQ depth, outbox backlog, action
failures, timer fires), had no health probe beyond a DB ping, no admin drill-down into the
relay/DLQ/timers/lineage, and shipped no dashboards, alerts, or runbooks. P1-A of the backlog.

## Decision

Add an additive ops-visibility surface; engine/ and model/ stay zero-diff.

1. **Stats reads.** `runtime.OutboxStats{Pending, Dead int64; OldestPendingAge}` and
   `runtime.TimerStats{Armed int64; NextFireAt *time.Time}` with reader ports `OutboxStatsReader` /
   `TimerStatsReader`, satisfied by the Postgres and MySQL `*Relay` / `*TimerStore` via cheap indexed
   aggregates over the existing partial indexes.

2. **SLI metrics.**
   - **Observable gauges** (queried per scrape, no background goroutine): `wrkflw_outbox_pending`,
     `wrkflw_outbox_dead`, `wrkflw_outbox_oldest_pending_age_seconds`, `wrkflw_timers_armed`, registered
     by consumer-wired collectors `runtime.NewOutboxStatsCollector` / `NewTimerStatsCollector` (each
     reads its port once per collection via a shared `Meter.RegisterCallback`). A new
     `observability.Telemetry.Int64ObservableGauge` helper (noop-fallback) backs single-gauge use.
   - **Counters** on the runner: `wrkflw_timer_fired_total` and `wrkflw_action_failures_total`
     (labels `action`, `retryable`).
   - Chosen over a polling goroutine because callback-only gauges have no lifecycle to manage and are
     always fresh at scrape time; the collectors are opt-in (no meter provider → noop).

3. **Health probe.** `persistence.NewRelayBacklogCheck(reader, WithMaxDead(n), WithMaxPending(n))` —
   structurally satisfies `rest.HealthCheck` without importing `transport/rest` in production (the
   `NewPingCheck` pattern). Thresholds default 0 = disabled. A scheduler/leadership probe is not shipped
   (leadership state is not cleanly exposed) — documented as a `HealthCheckFunc` recipe.

4. **Admin endpoints** behind the existing default-deny admin middleware, registered only when wired:
   - REST `GET /admin/relay-stats`, `GET /admin/timers`, `GET /admin/instances/{id}/lineage`; a
     `category` field on `GET /admin/dead-letters`.
   - gRPC `GetRelayStats`, `ListTimers`, `GetInstanceLineage`; `DeadLetter.category`.
   - DLQ categorization is the pure `runtime.ClassifyDeadLetter(lastError)` → timeout/connection/
     validation/unknown. New `service` ports `RelayStatsAdmin`, `TimerAdmin`, `LineageAdmin`.

5. **Instance lineage** — single-hop direct relations via new store reads on the call-link
   (`ParentOf`/`ChildrenOf`) and chain-link (`PredecessorOf`/`SuccessorsOf`) stores (Postgres + MySQL +
   Mem), composed by `runtime.NewLineageReader` into `runtime.InstanceLineage`.

6. **Dashboards / alerts / runbooks.** `docs/dashboards/wrkflw-overview.json` (Grafana),
   `docs/dashboards/wrkflw-alerts.yml` (Prometheus rules), `docs/runbooks/{high-dlq-depth,relay-backlog,
   action-failures}.md`, and `docs/observability.md` (the authoritative metric index + wiring recipes).

## Consequences

- **Observable-gauge cost:** each gauge collection runs one indexed aggregate query per scrape.
  Mitigated by the partial indexes and the opt-in collectors; documented in `docs/observability.md`.
- **`CallLinkRef` is faithful to `wrkflw_call_links`, narrowing the spec:** the table records only the
  parent definition and a child's *own* status is not in the `runtime.CallLink` read type. So
  `CallLinkRef` carries **no `Status`** field, and for a child relation `DefID`/`DefVersion` are empty
  (the child's definition is not recorded in call-links). An operator fetches a related instance's
  status/definition from its instance snapshot. (Caught and corrected during task review.)
- **Single-hop lineage:** recursive/full ancestry trees are deferred to a future enhancement.
- **Single-tenant:** metrics carry no tenant label.
- Admin endpoints inherit the default-deny gate; a consumer must opt in via the admin middleware
  (REST) or an auth interceptor (gRPC), consistent with the existing admin surface.
