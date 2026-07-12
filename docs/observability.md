# Observability

`wrkflw` exports OpenTelemetry (OTel) metrics for every operational concern: instance
throughput, step and action latency, relay health, outbox queue depth, timer state, and
REST/persistence performance.  This document is the authoritative index: metric inventory,
collector wiring, health-probe recipe, admin-endpoint reference, and pointers to dashboards
and runbooks.

---

## Metric inventory

All metric names use the `wrkflw_` prefix.  Histogram suffixes follow Prometheus/OTel
conventions (`_bucket`, `_sum`, `_count`).

### Counters

| Metric | Labels | Meaning |
|---|---|---|
| `wrkflw_instances_started_total` | — | Process instances created. |
| `wrkflw_instances_completed_total` | — | Process instances that reached a terminal end-event successfully. |
| `wrkflw_action_retries_total` | — | Total action retry attempts across all instances. |
| `wrkflw_incidents_raised_total` | — | Incidents raised by the engine (non-retryable failures, unhandled errors). |
| `wrkflw_incidents_resolved_total` | — | Incidents resolved by operators. |
| `wrkflw_human_tasks_total` | — | Human-task nodes entered. |
| `wrkflw_relay_events_published_total` | — | Outbox events successfully delivered to the broker. |
| `wrkflw_callnotifier_links_notified_total` | — | Parent-process call-links notified on child completion. |
| `wrkflw_chain_started_total` | — | Process chains initiated (successor instances spawned). |
| `wrkflw_rest_requests_total` | — | Inbound REST requests handled by the engine's HTTP adapter. |
| `wrkflw_timer_fired_total` | — | **New this release.** Timer events fired by the scheduling layer. |
| `wrkflw_action_failures_total` | `action`, `retryable` | **New this release.** Action execution failures. `action` is the catalog name; `retryable` is `"true"` or `"false"`. |

### UpDownCounters

| Metric | Labels | Meaning |
|---|---|---|
| `wrkflw_instances_active` | — | Current number of in-flight process instances. Incremented on start, decremented on terminal event. |

### Histograms

| Metric | Labels | Meaning |
|---|---|---|
| `wrkflw_step_duration_seconds` | — | Wall-clock time to execute a single process-graph node step (token advance through one node). |
| `wrkflw_action_duration_seconds` | — | Wall-clock time for a single `Action` invocation, successful or not. |
| `wrkflw_relay_batch_duration_seconds` | — | Wall-clock time for one relay poll-and-publish batch. |
| `wrkflw_store_duration_seconds` | — | Wall-clock time for persistence-layer (store) operations. |
| `wrkflw_rest_request_duration_seconds` | — | End-to-end REST request latency. |

### Observable gauges (new this release)

These are **callback-only** gauges: they are queried on each scrape by registered
collectors and do not maintain a background goroutine.  They must be wired by the consumer;
see [Collector wiring](#collector-wiring) below.

| Metric | Labels | Meaning |
|---|---|---|
| `wrkflw_outbox_pending` | — | Outbox rows with `status = 'pending'` not yet delivered. A growing value means the relay is falling behind. |
| `wrkflw_outbox_dead` | — | Outbox rows with `status = 'dead'` (exhausted all delivery attempts). Any value > 0 needs operator action. |
| `wrkflw_outbox_oldest_pending_age_seconds` | — | Age in seconds of the oldest pending outbox row. Measures relay lag. |
| `wrkflw_timers_armed` | — | Number of timer records currently armed (scheduled but not yet fired). |

---

## Collector wiring

The observable gauges are not registered automatically.  The consumer must instantiate the
collectors and pass them a reader that implements the relevant interface.

### Outbox stats

```go
import (
    "github.com/kartaladev/wrkflw/runtime"
    "github.com/kartaladev/wrkflw/observability"
)

// relay is a *persistence.Relay (or any runtime.OutboxStatsReader)
collector, err := runtime.NewOutboxStatsCollector(
    relay,
    observability.WithMeterProvider(mp), // your otel.MeterProvider
)
if err != nil {
    // handle
}
_ = collector // keep alive; no goroutine is started
```

`runtime.NewOutboxStatsCollector` registers three observable gauge instruments
(`wrkflw_outbox_pending`, `wrkflw_outbox_dead`, `wrkflw_outbox_oldest_pending_age_seconds`)
that call back into the provided `runtime.OutboxStatsReader` on each metric collection.

### Timer stats

```go
// timerStore is a *persistence.TimerStore (or any runtime.TimerStatsReader)
collector, err := runtime.NewTimerStatsCollector(
    timerStore,
    observability.WithMeterProvider(mp),
)
if err != nil {
    // handle
}
_ = collector
```

`runtime.NewTimerStatsCollector` registers `wrkflw_timers_armed`.

Both constructors accept the same `observability.Option` variadic — pass
`observability.WithMeterProvider(mp)` to target a specific provider, or omit it to use
the global provider.

---

## Health-probe recipe

The relay-backlog health check exposes outbox queue depth as a `readyz` probe.  Mount it
alongside any other checks in your `rest.NewHealthHandler`:

```go
import (
    "github.com/kartaladev/wrkflw/persistence"
    "github.com/kartaladev/wrkflw/rest"
)

// relay implements persistence.OutboxStatsReader
relayCheck := persistence.NewRelayBacklogCheck(
    relay,
    persistence.WithMaxDead(0),    // 0 = disabled; set > 0 to fail readyz when dead > n
    persistence.WithMaxPending(0), // 0 = disabled; set > 0 to fail readyz when pending > n
)

handler := rest.NewHealthHandler(
    rest.WithHealthCheck("relay-backlog", relayCheck),
    // ... other checks
)

// Mount on your mux:
// mux.Handle("/readyz", handler)
```

`persistence.NewRelayBacklogCheck` returns a `rest.HealthCheck`.  `WithMaxDead(n)` causes
the check to fail (`503`) when `wrkflw_outbox_dead > n`; `WithMaxPending(n)` does the
same for pending rows.  Thresholds default to `0` (disabled).  Set them conservatively:
a `readyz` failure removes the pod from the load-balancer, so use this only if your
deployment strategy can tolerate it.

---

## Admin endpoints

Admin endpoints are **default-absent by composition** (ADR-0095): they exist only when you
mount `AdminRoutes` (from any of the `transport/http/{stdlib,gin,fiber}` adapters) on a router
group your own auth middleware already protects. They carry no built-in authentication — mount
them at a path restricted to internal/privileged callers.

| Method | Path | Enabled by (`AdminRoutes` field) |
|---|---|---|
| `GET` | `/admin/instances` | `Svc` (always) |
| `POST` | `/admin/instances/{id}/cancel` | `Svc` (always) |
| `POST` | `/admin/instances/{id}/incidents/{incidentID}/resolve` | `Svc` (always) |
| `GET` | `/admin/relay-stats` | `RelayStats` |
| `GET` | `/admin/timers` | `Timers` |
| `GET` | `/admin/instances/{id}/lineage` | `Lineage` |
| `GET` | `/admin/dead-letters` | `DeadLetters` |
| `POST` | `/admin/dead-letters/redrive` | `DeadLetters` — body `{"ids":["<id>",...]}`; omit `ids` to redrive all. |
| `GET`/`POST`/`DELETE` | `/admin/policies`, `/admin/role-bindings` | `Policies` |

Each optional field is nil-guarded: its routes register only when the field is set.

---

## Dashboards

`docs/dashboards/wrkflw-overview.json` — Grafana dashboard (schemaVersion 39, Prometheus
datasource templated as `${DS_PROMETHEUS}`).  Import via **Dashboards → Import → Upload
JSON**.

Panels:

- Instance throughput: `wrkflw_instances_started_total` / `wrkflw_instances_completed_total` rate + `wrkflw_instances_active` stat.
- Step latency: `histogram_quantile` p50/p95/p99 over `wrkflw_step_duration_seconds_bucket`.
- Action latency: same quantiles over `wrkflw_action_duration_seconds_bucket`.
- Action failures by action name (`wrkflw_action_failures_total` rate, split by `action`, `retryable`).
- Action retries (`wrkflw_action_retries_total` rate).
- Relay published rate (`wrkflw_relay_events_published_total`) + relay batch latency.
- Outbox stats: `wrkflw_outbox_pending`, `wrkflw_outbox_dead`, `wrkflw_outbox_oldest_pending_age_seconds` (stat + time series).
- Timers: `wrkflw_timers_armed` stat + `wrkflw_timer_fired_total` rate.
- REST rate + store latency.

---

## Prometheus alert rules

`docs/dashboards/wrkflw-alerts.yml` — Prometheus rule-group file.  Load via your
Prometheus configuration (`rule_files:`) or a PrometheusRule CRD in Kubernetes.

Alert summary:

| Alert | Severity | Condition |
|---|---|---|
| `WrkflwOutboxDeadLetters` | warning | `wrkflw_outbox_dead > 0` |
| `WrkflwOutboxDeadLettersSustained` | critical | `wrkflw_outbox_dead > 0` for 10 m |
| `WrkflwOutboxOldestPendingHigh` | warning | oldest pending age > 300 s for 5 m |
| `WrkflwOutboxOldestPendingCritical` | critical | oldest pending age > 1800 s for 5 m |
| `WrkflwActionFailureRateHigh` | warning | failure rate > 0.5/s for 5 m |
| `WrkflwActionFailureRateCritical` | critical | failure rate > 2/s for 5 m |
| `WrkflwActionNonRetryableFailures` | warning | non-retryable failure rate > 0 for 2 m |
| `WrkflwNoInstanceCompletions` | warning | no completions in 15 m with active instances |
| `WrkflwActiveInstancesFlatline` | warning | active gauge unchanged for 30 m while non-zero |

---

## Runbooks

| Runbook | Alert(s) |
|---|---|
| `docs/runbooks/high-dlq-depth.md` | `WrkflwOutboxDeadLetters`, `WrkflwOutboxDeadLettersSustained` |
| `docs/runbooks/relay-backlog.md` | `WrkflwOutboxOldestPendingHigh`, `WrkflwOutboxOldestPendingCritical`, `WrkflwNoInstanceCompletions`, `WrkflwActiveInstancesFlatline` |
| `docs/runbooks/action-failures.md` | `WrkflwActionFailureRateHigh`, `WrkflwActionFailureRateCritical`, `WrkflwActionNonRetryableFailures` |

See also `docs/retention.md` for outbox table pruning, which directly affects
`wrkflw_outbox_pending` trends and relay scan performance.
