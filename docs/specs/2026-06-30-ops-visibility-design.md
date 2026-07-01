# Design — Ops visibility (SLI metrics, health probes, admin endpoints, dashboards)

**Date:** 2026-06-30
**Status:** Draft for review
**ADR:** 0078 (ops-visibility surface)
**Backlog item:** P1-A of `docs/plans/2026-06-30-production-readiness-backlog.md`

## Problem

The 2026-06-30 audit found ops visibility the weakest cluster. The engine emits throughput metrics
(instances, steps, actions, relay-published) but is missing the **SLIs an operator actually pages on**,
has no **health probe** beyond a DB ping, no **admin drill-down** into the relay/DLQ/timers, and ships
**no dashboards, alerts, or runbooks**. An operator cannot answer "is the outbox backing up?", "how deep
is the DLQ?", "what timers are armed?", or "which actions are failing?" without raw SQL.

## Scope (maintainer-confirmed: full P1-A in one track)

1. **SLI metrics** — the missing instruments.
2. **Health probes** — relay-backlog readiness check.
3. **Admin endpoints** — relay-stats, timers, DLQ failure categorization (REST + gRPC).
4. **Dashboards / alerts / runbooks** — shipped reference artifacts.

5. **Instance lineage** — parent-child (call-activity) + predecessor-successor (chaining) relations
   (maintainer-confirmed: included in this track).

## Design

All values are read from the existing `wrkflw_outbox` / `wrkflw_timers` tables via cheap indexed
queries (the partial indexes `wrkflw_outbox_dead_idx`, `wrkflw_outbox_claim_idx`, and
`wrkflw_timers_fire_at_idx` already exist). Engine/ + model/ stay zero-diff.

### 1. Stats read-methods on the persistence layer

New value types (`runtime` package, so both backends and transports share them):

```go
// runtime/opsstats.go
type OutboxStats struct {
    Pending          int64
    Dead             int64
    OldestPendingAge time.Duration // 0 when no pending rows
}
type TimerStats struct {
    Armed      int64
    NextFireAt *time.Time // nil when none armed
}
```

New methods, added to the existing `Relay` and `TimerStore` impls in **both** postgres and mysql:

- `Relay.OutboxStats(ctx) (runtime.OutboxStats, error)`
  - Postgres: `SELECT count(*) FILTER (WHERE status='pending'), count(*) FILTER (WHERE status='dead'),
    EXTRACT(EPOCH FROM now() - min(created_at) FILTER (WHERE status='pending')) FROM wrkflw_outbox`
  - MySQL: `SELECT SUM(status='pending'), SUM(status='dead'),
    TIMESTAMPDIFF(SECOND, MIN(CASE WHEN status='pending' THEN created_at END), NOW()) FROM wrkflw_outbox`
- `TimerStore.Stats(ctx) (runtime.TimerStats, error)` — `SELECT count(*), min(fire_at) FROM wrkflw_timers`.

New ports (in `runtime`, so transports/observability depend on interfaces, not impls):

```go
type OutboxStatsReader interface { OutboxStats(ctx context.Context) (OutboxStats, error) }
type TimerStatsReader  interface { Stats(ctx context.Context) (TimerStats, error) }
```

Facade constructors (`persistence.go` / `mysql.go`) already return the concrete `*Relay` / `*TimerStore`,
which now satisfy these — no new facade wiring beyond doc comments.

### 2. SLI metrics

**Observable gauges** (DB-derived, queried at scrape time — maintainer-chosen). Add a helper to
`internal/observability` mirroring the noop-fallback pattern:

```go
func (t Telemetry) Int64ObservableGauge(name, desc string, cb metric.Int64Callback) metric.Int64ObservableGauge
```

Registered by a new, optional `runtime.NewOutboxStatsCollector(reader OutboxStatsReader, opts ...observability.Option)`
and `NewTimerStatsCollector(reader TimerStatsReader, ...)` — small structs that register the gauges on
construction and whose callbacks call the reader. Gauges:
- `wrkflw_outbox_pending` — pending outbox rows.
- `wrkflw_outbox_dead` — dead (DLQ) rows.
- `wrkflw_outbox_oldest_pending_age_seconds` — age of the oldest pending row (relay-lag proxy).
- `wrkflw_timers_armed` — currently armed timers.

The callback honours ctx and runs one indexed aggregate; a query error observes nothing for that scrape
and is logged (no panic). These collectors are consumer-wired (like the pruner cron) — the library does
not assume a meter provider is configured.

**Counters** (event-driven, on the runner) — two new `metric.Int64Counter` fields on `runnerObs`,
initialised in `newRunnerObs`:
- `wrkflw_timer_fired_total` — incremented in the timer fire callback (`runner.go` ~line 1096).
- `wrkflw_action_failures_total{action,retryable}` — incremented in the `InvokeAction` failure branch
  (`runner.go` ~line 804), labelled with `cmd.Name` and `action.IsRetryable(err)`; also the
  unknown-action and fire-and-forget failure sub-cases.

### 3. Health probe

`persistence.NewRelayBacklogCheck(reader runtime.OutboxStatsReader, opts ...RelayBacklogOption) rest.HealthCheck`
(name `"relay-backlog"`). `Check(ctx)` reads `OutboxStats`; returns an error (→ `/readyz` 503, raw error
never leaked per the handler contract) when `Dead > maxDead` or `Pending > maxPending`. Thresholds via
`WithMaxDead(n)` / `WithMaxPending(n)`; both default to 0 = disabled (a consumer opts into thresholds).
Lives in the `persistence` facade (it already owns `NewPingCheck`). A scheduler/leadership probe is **not**
added (leadership state is not cleanly exposed today); documented as a `rest.HealthCheckFunc` recipe.

### 4. Admin endpoints

New `service` ports (mirroring `DeadLetterAdmin`), satisfied directly by the relay / timer store:

```go
type RelayStatsAdmin interface { OutboxStats(ctx) (runtime.OutboxStats, error) }
type TimerAdmin       interface { Stats(ctx) (runtime.TimerStats, error); ListArmed(ctx) ([]runtime.ArmedTimer, error) }
```

**REST** (under the existing default-deny `adminMiddleware`, registered only when the port is wired):
- `GET /admin/relay-stats` → `{pending, dead, oldestPendingAgeSeconds}`.
- `GET /admin/timers` → `{count, nextFireAt, items:[{instanceId, defId, defVersion, timerId, fireAt, kind}]}`
  (kind rendered as a string).
- DLQ categorization: extend the existing `deadLetterView` with a derived `category` field
  (`classifyDeadLetter(lastError) string` — e.g. `timeout`, `connection`, `validation`, `unknown` by
  matching known substrings/sentinels); pure function, unit-tested.

**gRPC** (mirror RPCs; `UnimplementedWorkflowServiceServer` keeps it compiling before impl):
- `GetRelayStats(GetRelayStatsRequest) returns (RelayStats)`.
- `ListTimers(ListTimersRequest) returns (ListTimersResponse)`.
- `category` added to the existing `DeadLetter` proto message.
New proto messages + `*opts.go` wiring (`WithRelayStatsAdmin`, `WithTimerAdmin`) on both transports;
errors via the existing `WriteHTTPError` / `mapToGRPCStatus`.

### 5. Dashboards / alerts / runbooks

- `docs/dashboards/wrkflw-overview.json` — Grafana dashboard: instance throughput + active, step/action
  latency (histograms), action failures, relay published rate, **outbox pending/dead gauges**, oldest-
  pending age, timers armed.
- `docs/dashboards/wrkflw-alerts.yml` — Prometheus alerting rules: DLQ depth > 0 (warning) / sustained
  (critical), outbox oldest-pending-age high, action-failure rate spike, instance-active flatline.
- `docs/runbooks/{high-dlq-depth,relay-backlog,action-failures}.md` — symptom → checks → remediation
  (redrive via the DLQ admin, resolve incidents, etc.), in the `docs/retention.md` voice.
- `docs/observability.md` — index: every metric name + type + labels, the collector wiring snippet, the
  health-probe recipe, and a pointer to the dashboards/runbooks.

### 6. Instance lineage

Single-hop direct relations (non-recursive; the response gives ids the operator can follow). Both
tables are already indexed for the reverse lookup (`wrkflw_chain_links_successor_idx`; call-links by PK
child + a `parent_instance_id` scan).

New value types (`runtime`):

```go
type CallLinkRef  struct { InstanceID, DefID string; DefVersion int; Status string; Depth int }
type ChainLinkRef struct { InstanceID, DefinitionRef, Outcome string }
type InstanceLineage struct {
    InstanceID       string
    CallParent       *CallLinkRef   // the instance that called this (nil if top-level)
    CallChildren     []CallLinkRef  // instances this one called (call activity)
    ChainPredecessor *ChainLinkRef  // the instance that chained into this (nil if none)
    ChainSuccessors  []ChainLinkRef // instances chained from this on terminal outcome
}
```

New read methods on the existing stores (both backends + the Mem impls):
- `CallLinkStore.ParentOf(ctx, childID) (*runtime.CallLink, error)` — `WHERE child_instance_id=$1` (PK).
- `CallLinkStore.ChildrenOf(ctx, parentID) ([]runtime.CallLink, error)` — `WHERE parent_instance_id=$1`.
- `ChainLinkStore.PredecessorOf(ctx, successorID) (*runtime.ChainLink, error)` — `WHERE successor_instance_id=$1` (indexed).
- `ChainLinkStore.SuccessorsOf(ctx, predecessorID) ([]runtime.ChainLink, error)` — `WHERE predecessor_instance_id=$1` (PK prefix).
  A missing single row returns `(nil, nil)` — absence is not an error.

Assembler: `runtime.NewLineageReader(calls CallLinkStore, chains ChainLinkStore)` with
`Lineage(ctx, instanceID) (InstanceLineage, error)` composing the four reads. Service port
`service.LineageAdmin` satisfied by it.

**REST:** `GET /admin/instances/{id}/lineage` → `InstanceLineage` JSON (behind `adminMiddleware`,
registered only when the port is wired). **gRPC:** `GetInstanceLineage(GetInstanceLineageRequest{instance_id})
returns (InstanceLineage)` with the mirrored proto messages.

## Testing

- **Stats methods:** testcontainers (postgres + mysql via `RunTestDatabase`/`RunTestMySQL`) — seed
  pending/dead/published rows + armed timers, assert exact counts + oldest-age sign; empty-table case.
- **Collectors:** a fake reader + a manual-reader OTel `metric.Reader` (sdk/metric) — force a collection,
  assert the gauge observes the reader's value; error-from-reader observes nothing (no panic).
- **Counters:** drive a runner (MemStore) — a failing action increments `action_failures_total` with the
  right labels; a fired timer increments `wrkflw_timer_fired_total` (assert via a manual reader).
- **Health probe:** table test — under/over each threshold; disabled (0) never fails; ctx honoured.
- **DLQ categorization:** table test over representative `lastError` strings → expected category.
- **Lineage:** store reads (testcontainers, both backends + Mem) — seed call-links + chain-links, assert
  ParentOf/ChildrenOf/PredecessorOf/SuccessorsOf; absent relation → `(nil, nil)`. Assembler composes them.
- **Admin endpoints:** REST httptest + gRPC bufconn, default-deny enforced, JSON/proto shapes, error mapping.
- Full `go test -race ./...` green; touched packages ≥ 85%; engine/model zero-diff; `golangci-lint` clean
  (incl. the gosec/bodyclose/errorlint added in ADR-0077 — new SQL uses `$n`/`?` placeholders only).

## Out of scope / deferred

- **Recursive/full ancestry trees** — lineage is single-hop (direct relations only); multi-level
  traversal is a future enhancement.
- Per-tenant metric labels (single-tenant; ADR notes it).
- A scheduler/leadership health probe (documented recipe, not a shipped type).
- Pushing metrics (the library exposes OTel instruments; the consumer owns the exporter/scrape endpoint).

## Risk

- Observable-gauge callbacks run a DB query per scrape. Mitigated: indexed partial-aggregate, and the
  collectors are opt-in (no meter provider → noop). Document the per-scrape cost.
- New `service` ports and transport options are additive; existing endpoints unchanged.
