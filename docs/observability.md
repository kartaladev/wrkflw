# Observability

`wrkflw` exports OpenTelemetry (OTel) metrics for every operational concern: instance
throughput, step and action latency, relay health, outbox queue depth, timer state, scheduler
job outcomes, and HTTP/persistence performance.  This document is the authoritative index:
metric inventory, collector wiring, health-probe recipe, admin-endpoint reference, and
pointers to dashboards and runbooks.

Not every metric is emitted by every deployment.  The counters and histograms below are
registered by the component that owns them and appear as soon as that component runs; the
callback-only instruments are **opt-in** and appear only once the consumer constructs the
matching collector.  Each table says which.

---

## Metric inventory

All metric names use the `wrkflw_` prefix.  Histogram suffixes follow Prometheus/OTel
conventions (`_bucket`, `_sum`, `_count`).

### Counters

| Metric | Labels | Meaning |
|---|---|---|
| `wrkflw_instances_started_total` | `def` | Process instances created. `def` is the process-definition id. |
| `wrkflw_instances_completed_total` | `def`, `status` | Process instances that reached **any** terminal status — success is not implied, despite the name. `status` is `engine.Status.String()`, one of `completed`, `failed`, `terminated`; filter `status="completed"` to count successes. |
| `wrkflw_action_retries_total` | — | Total action retry attempts across all instances. |
| `wrkflw_incidents_raised_total` | `def` | Incidents raised by the engine (non-retryable failures, unhandled errors). |
| `wrkflw_incidents_resolved_total` | `def` | Incidents resolved by operators. |
| `wrkflw_human_tasks_total` | `event` | Human-task lifecycle transitions. `event` is one of `created`, `claimed`, `reassigned`, `completed`, `candidates_refreshed` — so this is **not** a count of tasks. |
| `wrkflw_relay_events_published_total` | — | Outbox events successfully delivered to the broker. |
| `wrkflw_callnotifier_links_notified_total` | — | Parent-process call-links notified on child completion. |
| `wrkflw_chain_started_total` | — | Process chains initiated (successor instances spawned). |
| `wrkflw_rest_requests_total` | `http.method`, `http.route`, `http.status_code` | Inbound HTTP requests handled by a mounted transport route group. `http.route` is the STATIC route template, never the resolved path. |
| `wrkflw_timer_fired_total` | — | Timer callbacks that fired and delivered a `TimerFired` trigger. |
| `wrkflw_timer_arms_refused_total` | — | Timer arms refused because the scheduler could not run them (ADR-0176). A non-zero rate means work that should have been scheduled was not. |
| `wrkflw_action_failures_total` | `action`, `retryable` | Action execution failures. `action` is the catalog name; `retryable` is `"true"` or `"false"`. |
| `wrkflw_scheduler_job_runs_total` | `status`, `job_id` | gocron job runs by outcome (ADR-0134). ⚠ `job_id` is the caller-supplied timer/job id — deliberately high-cardinality, because per-timer attribution is the point. |
| `wrkflw_eventing_published_total` | — | Events published to the broker by the watermill eventing adapter. Emitted only when that adapter is wired. |
| `wrkflw_human_task_audit_drops_total` | — | Human-task audit columns dropped by a degraded list query. |
| `wrkflw_authz_policy_reload_failures_total` | — | Cross-node casbin policy reloads that failed, leaving enforcement on a stale policy. Emitted only by the DB-backed casbin authorizer. |

### UpDownCounters

| Metric | Labels | Meaning |
|---|---|---|
| `wrkflw_instances_active` | — | Current number of in-flight process instances. Incremented on start, decremented on terminal event. |

### Histograms

| Metric | Labels | Meaning |
|---|---|---|
| `wrkflw_step_duration_seconds` | `trigger` | Wall-clock time to execute a single `engine.Step` call. `trigger` is the trigger type name with the `engine.` prefix stripped (e.g. `StartInstance`). |
| `wrkflw_action_duration_seconds` | `action`, `outcome` | Wall-clock time for a single `Action` invocation. `outcome` is `"ok"` or `"error"`, so failures are included. |
| `wrkflw_relay_batch_duration_seconds` | — | Wall-clock time for one relay poll-and-publish batch. |
| `wrkflw_store_duration_seconds` | `op` | Wall-clock time for persistence-layer (store) operations. `op` is `load` or `commit`. |
| `wrkflw_rest_request_duration_seconds` | `http.method`, `http.route`, `http.status_code` | End-to-end HTTP request latency, labelled identically to `wrkflw_rest_requests_total`. |
| `wrkflw_scheduler_job_duration_seconds` | `status`, `job_id` | Duration of a gocron job run (ADR-0134). Same high-cardinality `job_id` note as `wrkflw_scheduler_job_runs_total`. |

### Observable gauges and counters

These are **callback-only** instruments: they are queried on each scrape by registered
collectors and do not maintain a background goroutine.  They must be wired by the consumer;
see [Collector wiring](#collector-wiring) below.  Nothing registers them implicitly, so a
dashboard panel over one of these is empty until the matching collector is constructed.

| Metric | Kind | Registered by | Meaning |
|---|---|---|---|
| `wrkflw_outbox_pending` | gauge | `monitor.NewOutboxStatsCollector` | Outbox rows with `status = 'pending'` not yet delivered. A growing value means the relay is falling behind. |
| `wrkflw_outbox_dead` | gauge | `monitor.NewOutboxStatsCollector` | Outbox rows with `status = 'dead'` (exhausted all delivery attempts). Any value > 0 needs operator action. |
| `wrkflw_outbox_oldest_pending_age_seconds` | gauge | `monitor.NewOutboxStatsCollector` | Age in seconds of the oldest pending outbox row. Zero when there are no pending rows. |
| `wrkflw_timers_armed` | gauge | `monitor.NewTimerStatsCollector` | Number of timer rows currently armed (scheduled but not yet fired). |
| `wrkflw_timers_next_fire_age_seconds` | gauge | `monitor.NewTimerStatsCollector` | How far past due the earliest armed timer is, in whole seconds. **Zero is the healthy value** — it is clamped to zero when no timer is armed, when the stored `next_run` is the zero time (ADR-0181), and whenever the earliest timer is still in the future. This is the signal `wrkflw_timers_armed` cannot give: a scheduler keeping up and one 45 minutes behind report the same armed count. |
| `wrkflw_db_pool_in_use` | gauge | `persistence.NewPoolStatsCollector` / `NewPostgresPoolStatsCollector` | Database connections currently checked out of the pool. |
| `wrkflw_db_pool_idle` | gauge | same | Database connections currently idle in the pool. |
| `wrkflw_db_pool_max_open` | gauge | same | Configured maximum number of open connections — the saturation denominator. |
| `wrkflw_db_pool_waits_total` | counter | same | Cumulative acquisitions that had to wait for a free connection. A rising value means the ceiling is already being hit. |

Pool saturation is `wrkflw_db_pool_in_use / wrkflw_db_pool_max_open`.  On Postgres the
`waits_total` series is pgx's `EmptyAcquireCount`; on MySQL/SQLite it is `database/sql`'s
`WaitCount`.  Both count the same thing, so a dashboard is backend-independent.

---

## Collector wiring

The callback-only instruments are not registered automatically.  The consumer instantiates
each collector and passes it a reader that implements the relevant interface.  Every
snippet below is compiled, from **outside** the module, as part of this document's upkeep —
see `docs/plans/sweep-evidence/fix-review-docs.md` for the build log.

The collectors live in `github.com/kartaladev/wrkflw/runtime/monitor` and are configured
with **that package's own** `monitor.Option` type — `WithMeterProvider`, `WithLogger`,
`WithTracerProvider`, `WithClock`.  Omit an option to fall back to the OTel globals and the
real clock; a nil option is silently ignored.  Neither constructor returns an error:
instrument-registration failures are logged on the configured logger and never fatal.

### Outbox stats

```go
import (
    "go.opentelemetry.io/otel/metric"

    "github.com/kartaladev/wrkflw/persistence"
    "github.com/kartaladev/wrkflw/runtime/monitor"
)

// relay is whatever persistence.NewRelay / NewMySQLRelay / NewSQLiteRelay returned;
// persistence.Relay satisfies kernel.OutboxStatsReader through its OutboxStats method.
func mountOutboxStats(relay persistence.Relay, mp metric.MeterProvider) *monitor.OutboxStatsCollector {
    // No error return: instrument-registration failures are logged, never fatal.
    return monitor.NewOutboxStatsCollector(
        relay,
        monitor.WithMeterProvider(mp), // omit to use the OTel global provider
    )
}
```

`monitor.NewOutboxStatsCollector` registers `wrkflw_outbox_pending`, `wrkflw_outbox_dead`
and `wrkflw_outbox_oldest_pending_age_seconds`, sharing **one** callback so the reader is
queried once per collection cycle.  Keep the returned value alive for as long as you want
the gauges reported; it starts no goroutine.

### Timer stats

`persistence.NewTimerStore` (and its MySQL/SQLite siblings) declares `kernel.TimerStore`,
whose method set does **not** include `Stats`.  The shipped implementations do satisfy
`kernel.TimerStatsReader`, so reach it with a type assertion:

```go
import (
    "fmt"

    "go.opentelemetry.io/otel/metric"

    "github.com/kartaladev/wrkflw/runtime/kernel"
    "github.com/kartaladev/wrkflw/runtime/monitor"
)

func mountTimerStats(store kernel.TimerStore, mp metric.MeterProvider) (*monitor.TimerStatsCollector, error) {
    reader, ok := store.(kernel.TimerStatsReader)
    if !ok {
        return nil, fmt.Errorf("timer store %T does not report stats", store)
    }
    return monitor.NewTimerStatsCollector(reader, monitor.WithMeterProvider(mp)), nil
}
```

`monitor.NewTimerStatsCollector` registers **both** `wrkflw_timers_armed` and
`wrkflw_timers_next_fire_age_seconds`.  The overdue age is derived from the collector's
clock, so a test that asserts on it must inject one with
`monitor.WithClock(clockwork.NewFakeClockAt(instant))` — use an **explicit** instant, since
`clockwork.NewFakeClock()` seeds itself from the wall clock and an age assertion written
against it can pass even against a collector that never consults the injected clock.

### Database pool stats

Pool saturation is the one operational signal the engine cannot read for you: the consumer
owns the handle (`OpenPostgres` / `OpenMySQL` / `OpenSQLite` all take an already-open one).
These collectors only read statistics — they never open, close or reconfigure a connection,
and a nil handle yields a collector that observes zeroes rather than panicking a scrape.

```go
import (
    "database/sql"

    "github.com/jackc/pgx/v5/pgxpool"
    "go.opentelemetry.io/otel/metric"

    "github.com/kartaladev/wrkflw/persistence"
)

// Postgres (pgx): the pool stays owned by the caller.
func mountPgPoolStats(pool *pgxpool.Pool, mp metric.MeterProvider) *persistence.PoolStatsCollector {
    return persistence.NewPostgresPoolStatsCollector(pool, persistence.WithPoolStatsMeterProvider(mp))
}

// MySQL or SQLite (database/sql).
func mountSQLPoolStats(db *sql.DB, mp metric.MeterProvider) *persistence.PoolStatsCollector {
    return persistence.NewPoolStatsCollector(db, persistence.WithPoolStatsMeterProvider(mp))
}
```

These take `persistence.PoolStatsOption` (`WithPoolStatsMeterProvider`,
`WithPoolStatsLogger`) — a separate type from `monitor.Option`, because they are registered
by a different package.

---

## Health-probe recipe

The relay-backlog check exposes outbox queue depth as a `readyz` probe.  There is no
`rest` package: health routes are mounted through the transport adapter you already use —
`stdlib.MountHealth`, `gin.MountHealth` or `fiber.MountHealth` — which registers
`GET /healthz` and `GET /readyz`.

```go
import (
    "net/http"

    "github.com/kartaladev/wrkflw/persistence"
    "github.com/kartaladev/wrkflw/transport/http/stdlib"
)

func mountHealth(mux *http.ServeMux, relay persistence.Relay) {
    relayCheck := persistence.NewRelayBacklogCheck(
        relay,
        persistence.WithMaxDead(0),    // 0 = disabled; set > 0 to fail readyz when dead > n
        persistence.WithMaxPending(0), // 0 = disabled; set > 0 to fail readyz when pending > n
    )

    // Registers GET /healthz and GET /readyz; relayCheck reports under "relay-backlog".
    stdlib.MountHealth(mux, relayCheck)
}
```

`persistence.NewRelayBacklogCheck` returns a `persistence.RelayBacklogCheck`, which
satisfies `httpcore.HealthCheck` structurally (`Name` + `Check`) — the persistence package
deliberately does not import the transport package.  `WithMaxDead(n)` causes the check to
fail when the dead-row count exceeds `n`; `WithMaxPending(n)` does the same for pending
rows.  Thresholds default to `0` (disabled), so the default configuration only fails when
the read itself fails.

`/readyz` returns `200` with `{"status":"ok","checks":{…}}` when every check passes and
`503` with `{"status":"unavailable",…}` when any fails.  Failing checks are reported as
`"unavailable"` without the underlying error, which may carry host/DSN fragments — the
check implementation owns logging the detail.  Set thresholds conservatively: a `readyz`
failure removes the pod from the load-balancer, so use this only if your deployment
strategy can tolerate it.

`persistence.NewPingCheck` / `NewMySQLPingCheck` / `NewSQLitePingCheck` are the companion
liveness-style probes for the database handle itself, and are registered the same way.

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
| `POST` | `/admin/instances/{id}/compensation/resolve-stall` | `Svc` (always) — ADR-0175's operator escape from a stalled compensation walk. |
| `GET` | `/admin/relay-stats` | `RelayStats` |
| `GET` | `/admin/timers` | `Timers` |
| `GET` | `/admin/instances/{id}/lineage` | `Lineage` |
| `GET` | `/admin/dead-letters` | `DeadLetters` |
| `POST` | `/admin/dead-letters/redrive` | `DeadLetters` — body `{"ids":["<id>",...]}`; omit `ids` to redrive all. |
| `GET`/`POST`/`DELETE` | `/admin/policies`, `/admin/role-bindings` | `Policies` |

Each optional field is nil-guarded: its routes register only when the field is set.  The
paths above are written in stdlib's `{id}` template syntax; the gin and fiber adapters
register the same routes with `:id` / `:incidentID`.

**`resolve-stall` requires a body** — unlike resolve-incident, nothing defaults:

```json
{"command_id": "<compensating.active_command_id>", "disposition": "retry|skip|abandon", "incident_id": "<optional>"}
```

`command_id` and `disposition` are both mandatory. `disposition` fails closed on an empty or
unrecognised value rather than defaulting, because the zero `engine.CompensationDisposition`
is `CompensationRetry` — a re-execution primitive that would re-invoke a named action with
the record's captured input. `command_id` is read from the instance document's
`compensating.active_command_id`; `incident_id` is optional and targets the walk in flight
when empty.

---

## Dashboards

`docs/dashboards/wrkflw-overview.json` — Grafana dashboard (schemaVersion 39, Prometheus
datasource templated as `${DS_PROMETHEUS}`).  Import via **Dashboards → Import → Upload
JSON**.

Panels, by row:

- **Instances** — instance throughput (`wrkflw_instances_started_total` /
  `wrkflw_instances_completed_total` rate), `wrkflw_instances_active` stat, and incidents
  raised vs resolved.
- **Latency** — `histogram_quantile` p50/p95/p99 over `wrkflw_step_duration_seconds_bucket`
  and `wrkflw_action_duration_seconds_bucket`.
- **Action failures & retries** — `wrkflw_action_failures_total` rate split by `action` and
  `retryable`, plus `wrkflw_action_retries_total` rate.
- **Outbox relay** — `wrkflw_relay_events_published_total` rate and relay batch-duration
  quantiles.
- **Outbox queue health** — `wrkflw_outbox_pending`, `wrkflw_outbox_dead`,
  `wrkflw_outbox_oldest_pending_age_seconds` (stats + a shared trend series).
- **Timers** — `wrkflw_timers_armed` stat and trend, plus `wrkflw_timer_fired_total` rate.
- **REST & persistence** — `wrkflw_rest_requests_total` rate and
  `wrkflw_store_duration_seconds` quantiles.

⚠ The shipped dashboard predates several instruments listed above and has **no panel** for
`wrkflw_timers_next_fire_age_seconds`, `wrkflw_timer_arms_refused_total`, or the
`wrkflw_db_pool_*` series. Those are the newer health signals; add panels for them, or query
them ad hoc, until the dashboard catches up.

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
