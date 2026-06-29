# Ops-Visibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development — one subagent
> per task, strict TDD (visible RED→GREEN), review between tasks. Steps use `- [ ]`.

**Goal:** Ship the P1-A ops-visibility surface: SLI metrics, a relay-backlog health probe, REST+gRPC
admin endpoints (relay-stats, timers, DLQ categorization, instance lineage), and dashboards/runbooks.

**Architecture:** Cheap indexed reads over the existing `wrkflw_outbox` / `wrkflw_timers` /
`wrkflw_call_links` / `wrkflw_chain_links` tables, surfaced as OTel observable gauges + counters, a
`rest.HealthCheck`, and additive `service` ports mounted on both transports. Engine/ + model/ zero-diff.

**Tech Stack:** Go 1.25, pgx/database-sql, OTel metric SDK (`go.opentelemetry.io/otel/sdk/metric`),
testcontainers, protoc (+ protoc-gen-go / protoc-gen-go-grpc, both on `$(go env GOPATH)/bin`).

## Global Constraints

- Module path `github.com/zakyalvan/krtlwrkflw`. TDD strict: failing test first, visible RED via
  `go test ./<pkg>/...`. Black-box tests (`<pkg>_test`); table form (assert-closure) for ≥2 cases.
- DB tests use `database.RunTestDatabase(t)` (postgres) / `database.RunTestMySQL(t)` (mysql, auto-migrates);
  postgres needs an explicit `Migrate`. Real services via testcontainers, never mocked.
- Value/port types live in `runtime`; postgres impls in `internal/persistence/postgres`, mysql in
  `internal/persistence/mysql`; transports in `transport/{rest,grpc}`; admin ports in `service`.
- engine/ + model/ production code zero-diff. Error messages carry the `workflow-` prefix; `errors.Is`.
- New SQL uses `$n` (pgx) / `?` (database/sql) placeholders only — no string interpolation (gosec clean).
- ≥85% on touched packages; full `go test -race ./...` green; `golangci-lint run ./...` clean.
- ADR: **0078**.

---

## Phase 1 — Stats reads (foundation)

### Task 1: Stats value types + ports + Postgres reads

**Files:** Create `runtime/opsstats.go`; modify `internal/persistence/postgres/relay.go`,
`internal/persistence/postgres/timerstore.go`; Test: `internal/persistence/postgres/opsstats_test.go`.

**Produces:**
```go
// runtime/opsstats.go
type OutboxStats struct { Pending, Dead int64; OldestPendingAge time.Duration }
type TimerStats  struct { Armed int64; NextFireAt *time.Time }
type OutboxStatsReader interface { OutboxStats(ctx context.Context) (OutboxStats, error) }
type TimerStatsReader  interface { Stats(ctx context.Context) (TimerStats, error) }
```
Methods (postgres):
- `func (r *Relay) OutboxStats(ctx context.Context) (runtime.OutboxStats, error)` —
  `SELECT count(*) FILTER (WHERE status='pending'), count(*) FILTER (WHERE status='dead'),
   COALESCE(EXTRACT(EPOCH FROM now()-min(created_at) FILTER (WHERE status='pending')),0) FROM wrkflw_outbox`.
  Scan the epoch seconds into a float64, convert to `time.Duration(sec*float64(time.Second))`.
- `func (s *TimerStore) Stats(ctx context.Context) (runtime.TimerStats, error)` —
  `SELECT count(*), min(fire_at) FROM wrkflw_timers` (scan `min` into `*time.Time`).

- [ ] RED: `opsstats_test.go` (testcontainers): seed 2 pending + 1 dead + 1 published outbox row and 2
      timers; assert `OutboxStats{Pending:2,Dead:1, OldestPendingAge>0}` and `TimerStats{Armed:2, NextFireAt!=nil}`;
      empty-table case → zero values, `NextFireAt==nil`, `OldestPendingAge==0`. Run → RED.
- [ ] GREEN: add the two methods + `runtime/opsstats.go`. Run → GREEN.
- [ ] Compile-time assertions: `var _ runtime.OutboxStatsReader = (*Relay)(nil)`,
      `var _ runtime.TimerStatsReader = (*TimerStore)(nil)`.
- [ ] Commit `feat(persistence): OutboxStats/TimerStats reads (postgres)`.

### Task 2: MySQL stats reads

**Files:** modify `internal/persistence/mysql/relay.go`, `internal/persistence/mysql/timerstore.go`;
Test: `internal/persistence/mysql/opsstats_test.go`.

Same method signatures returning `runtime.OutboxStats`/`runtime.TimerStats`. MySQL SQL:
- `SELECT SUM(status='pending'), SUM(status='dead'),
   COALESCE(TIMESTAMPDIFF(SECOND, MIN(CASE WHEN status='pending' THEN created_at END), NOW()),0) FROM wrkflw_outbox`
  (SUM of a boolean returns the count; scan into int64 — note SUM returns NULL on empty table, use COALESCE).
- `SELECT count(*), MIN(fire_at) FROM wrkflw_timers`.

- [ ] RED (mirror Task 1 with `RunTestMySQL`) → GREEN → compile-time assertions → commit
      `feat(persistence): OutboxStats/TimerStats reads (mysql)`.

---

## Phase 2 — Metrics

### Task 3: Observable-gauge helper + stats collectors

**Files:** modify `internal/observability/observability.go`; create `runtime/stats_collector.go`;
Test: `internal/observability/observability_test.go`, `runtime/stats_collector_test.go`.

**Produces:**
- `func (t Telemetry) Int64ObservableGauge(name, desc string, cb metric.Int64Callback) metric.Int64ObservableGauge`
  (noop-fallback like the other helpers; on meter error return
  `metricnoop.NewMeterProvider().Meter(t.name).Int64ObservableGauge(name)` ignoring err).
- `func NewOutboxStatsCollector(r OutboxStatsReader, opts ...observability.Option) *OutboxStatsCollector` —
  registers `wrkflw_outbox_pending`, `wrkflw_outbox_dead`, `wrkflw_outbox_oldest_pending_age_seconds`.
  One callback reads `r.OutboxStats(ctx)` once and `o.Observe(...)` each gauge (register the three gauges,
  then one `RegisterCallback`/shared `Int64Callback` — or three `WithInt64Callback` gauges each calling the
  reader; prefer ONE read per scrape via `Meter.RegisterCallback(cb, g1,g2,g3)`). On reader error: log via
  the telemetry logger, observe nothing.
- `func NewTimerStatsCollector(r TimerStatsReader, opts ...observability.Option) *TimerStatsCollector` —
  registers `wrkflw_timers_armed`.

- [ ] RED: helper test — a `sdkmetric.NewManualReader()` + `MeterProvider`, build Telemetry
      `WithMeterProvider`, register a gauge whose callback observes 42, `rdr.Collect` → assert datapoint 42.
      Collector test — fake reader returning `OutboxStats{Pending:3,Dead:1,OldestPendingAge:2s}`; `Collect`
      → assert the three gauges; error-returning reader → no datapoints, no panic. Run → RED.
- [ ] GREEN → ensure goroutine-free (callbacks only) → commit
      `feat(observability): observable-gauge helper + outbox/timer stats collectors`.

### Task 4: Runner counters (timer-fired, action-failures)

**Files:** modify `runtime/observability.go` (struct + `newRunnerObs`), `runtime/runner.go`;
Test: `runtime/runner_metrics_test.go`.

Add to `runnerObs`: `timerFired metric.Int64Counter`, `actionFailures metric.Int64Counter`; init in
`newRunnerObs` (`wrkflw_timer_fired_total`, `wrkflw_action_failures_total`). Increment:
- `timerFired.Add(ctx,1)` in the timer fire callback (runner.go ~L1096, the `engine.NewTimerFired` path).
- `actionFailures.Add(actx,1, WithAttributes(attribute.String("action",cmd.Name),
   attribute.Bool("retryable", action.IsRetryable(err))))` in the `InvokeAction` `err != nil` branch
  (runner.go ~L804), plus the unknown-action (retryable=false) and fire-and-forget sub-cases.

- [ ] RED: drive a Runner (MemStore) with a `WithMeterProvider(manualReader MP)`: a failing action →
      `wrkflw_action_failures_total{action,retryable=...}` == 1; a timer-fired path → `wrkflw_timer_fired_total`==1.
      Run → RED → GREEN → commit `feat(runtime): timer-fired and action-failure counters`.

---

## Phase 3 — Health probe

### Task 5: Relay-backlog health check

**Files:** create `persistence/relay_health.go`; Test: `persistence/relay_health_test.go`.

**Produces:**
```go
type RelayBacklogOption func(*relayBacklogConfig)
func WithMaxDead(n int64) RelayBacklogOption
func WithMaxPending(n int64) RelayBacklogOption
func NewRelayBacklogCheck(r runtime.OutboxStatsReader, opts ...RelayBacklogOption) rest.HealthCheck
```
`Name()` → `"relay-backlog"`. `Check(ctx)`: read `OutboxStats`; if `maxDead>0 && Dead>maxDead` or
`maxPending>0 && Pending>maxPending`, return a `workflow-`-prefixed error (handler maps to 503, raw error
not leaked). Both thresholds default 0 = disabled (then Check always nil unless the read errors).

- [ ] RED (table test, fake reader): over/under each threshold; disabled never fails; reader error → error;
      cancelled ctx honoured. Run → RED → GREEN → commit `feat(persistence): relay-backlog health check`.

---

## Phase 4 — Lineage

### Task 6: Lineage value types + Postgres store reads

**Files:** modify `runtime/opsstats.go` (lineage types), `internal/persistence/postgres/call_links.go`,
`internal/persistence/postgres/chain_links.go`; Test: `internal/persistence/postgres/lineage_test.go`.

**Produces (runtime):**
```go
type CallLinkRef  struct { InstanceID, DefID string; DefVersion int; Status string; Depth int }
type ChainLinkRef struct { InstanceID, DefinitionRef, Outcome string }
type InstanceLineage struct {
    InstanceID string
    CallParent *CallLinkRef; CallChildren []CallLinkRef
    ChainPredecessor *ChainLinkRef; ChainSuccessors []ChainLinkRef
}
```
New store methods (postgres), returning the existing `runtime.CallLink`/`runtime.ChainLink` (see
`runtime/calllink.go`, `runtime/chainlink.go`):
- `CallLinkStore.ParentOf(ctx, childID) (*runtime.CallLink, error)` — `WHERE child_instance_id=$1` (PK);
  no row → `(nil, nil)`.
- `CallLinkStore.ChildrenOf(ctx, parentID) ([]runtime.CallLink, error)` — `WHERE parent_instance_id=$1
   ORDER BY created_at, child_instance_id`.
- `ChainLinkStore.PredecessorOf(ctx, successorID) (*runtime.ChainLink, error)` —
  `WHERE successor_instance_id=$1` (indexed); no row → `(nil, nil)`.
- `ChainLinkStore.SuccessorsOf(ctx, predecessorID) ([]runtime.ChainLink, error)` —
  `WHERE predecessor_instance_id=$1 ORDER BY outcome`.

(Define the read-port interfaces in `runtime` so the assembler depends on interfaces:
`CallLineageReader{ParentOf,ChildrenOf}`, `ChainLineageReader{PredecessorOf,SuccessorsOf}`.)

- [ ] RED (testcontainers): seed a parent→child call-link and a predecessor→successor chain-link; assert
      each of the four reads; absent relation → `(nil,nil)` / empty slice. Run → RED → GREEN →
      compile-time assertions → commit `feat(persistence): lineage reads (postgres)`.

### Task 7: Lineage reads — MySQL + Mem + assembler

**Files:** modify `internal/persistence/mysql/call_links.go`, `.../mysql/chain_links.go`,
`runtime/mem_calllink.go`, `runtime/chainlink.go` (Mem impl), create `runtime/lineage.go`; Tests
alongside each.

- MySQL: same four methods (`?` placeholders), `RunTestMySQL` test.
- Mem impls: same four methods over the in-memory maps.
- Assembler: `func NewLineageReader(calls CallLineageReader, chains ChainLineageReader) *LineageReader`
  with `Lineage(ctx, instanceID) (runtime.InstanceLineage, error)` composing the four reads into the DTO
  (map `CallLink`→`CallLinkRef`, `ChainLink`→`ChainLinkRef`). `service.LineageAdmin` port =
  `{ Lineage(ctx, string) (runtime.InstanceLineage, error) }`, satisfied by `*LineageReader`.

- [ ] RED→GREEN per impl (mysql testcontainers; Mem unit; assembler unit with fakes) → commit
      `feat(runtime): lineage reads (mysql+mem) and assembler`.

---

## Phase 5 — DLQ categorization

### Task 8: classifyDeadLetter

**Files:** create `runtime/dlq_category.go`; Test: `runtime/dlq_category_test.go`.

`func ClassifyDeadLetter(lastError string) string` — pure: returns `timeout` / `connection` /
`validation` / `unknown` by matching case-insensitive substrings (`deadline`/`timeout` → timeout;
`connection`/`dial`/`refused`/`EOF` → connection; `validation`/`invalid` → validation; else `unknown`).
Empty → `unknown`.

- [ ] RED (table test over representative strings) → GREEN → commit `feat(runtime): DLQ failure categorization`.

---

## Phase 6 — Transports

### Task 9: REST admin endpoints

**Files:** modify `transport/rest/admin.go`, `transport/rest/handler.go`, `transport/rest/options.go`;
Test: `transport/rest/admin_opsstats_test.go`, `..._lineage_test.go`.

- Options: `WithRelayStatsAdmin(service.RelayStatsAdmin)`, `WithTimerAdmin(service.TimerAdmin)`,
  `WithLineageAdmin(service.LineageAdmin)` (ports: `RelayStatsAdmin{OutboxStats}`,
  `TimerAdmin{Stats; ListArmed}`, `LineageAdmin{Lineage}` in `service`). Register routes only when wired,
  under `cfg.adminMiddleware`.
- `GET /admin/relay-stats` → `{pending,dead,oldestPendingAgeSeconds}`.
- `GET /admin/timers` → `{count,nextFireAt,items:[{instanceId,defId,defVersion,timerId,fireAt,kind}]}`
  (`kind` via a `engine.TimerKind` String()/switch).
- `GET /admin/instances/{id}/lineage` → `InstanceLineage` JSON.
- Add `category` to `deadLetterView` (call `runtime.ClassifyDeadLetter(LastError)`).

- [ ] RED (httptest): each endpoint default-deny (403) without middleware, 200 + shape with a permissive
      middleware + fake port; DLQ view carries `category`. Run → RED → GREEN → commit
      `feat(transport/rest): relay-stats, timers, lineage, DLQ categorization`.

### Task 10: gRPC admin RPCs

**Files:** modify `transport/grpc/proto/workflow.proto`, regenerate `transport/grpc/workflowpb/*`,
modify `transport/grpc/server.go`, `transport/grpc/options.go`; Test: `transport/grpc/*_test.go` (bufconn).

- Proto: add `rpc GetRelayStats(GetRelayStatsRequest) returns (RelayStats)`,
  `rpc ListTimers(ListTimersRequest) returns (ListTimersResponse)`,
  `rpc GetInstanceLineage(GetInstanceLineageRequest) returns (InstanceLineage)`; add `string category=…`
  to the existing `DeadLetter` message; new messages (RelayStats{int64 pending,dead,oldest_pending_age_seconds};
  Timer{...}; ListTimersResponse{repeated Timer items; int64 count; ... next_fire_at};
  InstanceLineage{... CallLinkRef/ChainLinkRef nested}).
- Regenerate (protoc available; buf is not):
  ```bash
  cd transport/grpc && export PATH="$PATH:$(go env GOPATH)/bin"
  protoc --proto_path=proto --go_out=workflowpb --go_opt=paths=source_relative \
    --go-grpc_out=workflowpb --go-grpc_opt=paths=source_relative proto/workflow.proto
  ```
- Server methods mirror `ListDeadLetters` (nil-port → `codes.Unimplemented`; `startSpan`/`recordSpanErr`;
  `mapToGRPCStatus`). Options `WithRelayStatsAdmin`/`WithTimerAdmin`/`WithLineageAdmin`; fields on `server`.
  Populate `DeadLetter.category` via `runtime.ClassifyDeadLetter`.

- [ ] RED (bufconn): each RPC Unimplemented when unwired; correct response when wired; `DeadLetter.category`
      populated. Run → RED → GREEN → commit `feat(transport/grpc): relay-stats, timers, lineage RPCs`.

---

## Phase 7 — Docs

### Task 11: Dashboards, alerts, runbooks, observability index

**Files:** create `docs/dashboards/wrkflw-overview.json`, `docs/dashboards/wrkflw-alerts.yml`,
`docs/runbooks/{high-dlq-depth,relay-backlog,action-failures}.md`, `docs/observability.md`.

- Grafana dashboard JSON: panels for instances started/completed/active, step/action latency (histogram
  quantiles), `wrkflw_action_failures_total` rate, relay published rate, `wrkflw_outbox_pending`/`_dead`
  gauges, `wrkflw_outbox_oldest_pending_age_seconds`, `wrkflw_timers_armed`.
- Prometheus rules: `wrkflw_outbox_dead > 0` (warning) and sustained 10m (critical); oldest-pending-age >
  threshold; action-failure rate spike; active-instances flatline.
- Runbooks (in `docs/retention.md` voice): symptom → checks (which metric/endpoint) → remediation
  (redrive DLQ via `/admin/dead-letters/redrive`, resolve incidents, scale).
- `docs/observability.md`: table of every `wrkflw_*` metric (name, type, labels) + the collector wiring
  snippet + the health-probe recipe + dashboard/runbook pointers.

- [ ] Commit `docs(observability): Grafana dashboard, Prometheus alerts, runbooks, metric index`.

### Task 12: ADR + CHANGELOG + backlog

**Files:** create `docs/adr/0078-ops-visibility-surface.md` (Nygard); modify `CHANGELOG.md`,
`docs/plans/2026-06-30-production-readiness-backlog.md`.

- ADR-0078: observable-gauge mechanism, the new metrics/ports/endpoints, single-hop lineage choice,
  single-tenant note. CHANGELOG `Added` entries. Mark P1-A done in the backlog.
- [ ] Commit `docs(adr): 0078 ops-visibility surface`.

## Verification checklist

- [ ] `go test -race ./...` green (touched pkgs ≥85%).
- [ ] `golangci-lint run ./...` 0 issues (new SQL placeholder-only; gosec clean).
- [ ] engine/ + model/ `git diff` empty.
- [ ] Observable gauges add no goroutine (callback-only); collectors opt-in.
- [ ] Both backends (postgres+mysql) implement every new read method; Mem impls for lineage.
- [ ] opus whole-branch review before merge.

## Self-review

- Spec coverage: metrics→T1-4; health→T5; lineage→T6-7; DLQ category→T8; REST→T9; gRPC→T10; docs→T11-12. Complete.
- Type consistency: `OutboxStats`/`TimerStats`/`InstanceLineage`/`CallLinkRef`/`ChainLinkRef` and the
  reader ports are defined in `runtime` (T1/T6) and consumed unchanged downstream.
- Risk: proto regen (T10) needs protoc on PATH — confirmed available; command embedded.
