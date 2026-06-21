# 19. Observability runtime boundary: OTel-API-direct, runtime-is-the-boundary

- Status: Accepted
- Date: 2026-06-21

## Context

REQUIREMENTS line 17 — *"This library must be able to expose process metrics, enable traces,
using slog golang logger."* — is unimplemented across the codebase except for a single OTel span
emitted by the watermill eventing adapter (`eventing.publish`).

Two hard constraints shape the design:

1. **Engine purity (HANDOVER "Core invariants" #1, ADR-0002).** `engine` and `model` must import
   only stdlib (+ the in-repo pure packages `model`/`authz`/`humantask`/`expreval`). No transport,
   storage, bus, or observability vendor may appear in the core. Naively, metrics/traces/logs are
   I/O side-effects that would pollute `engine.Step` and break this invariant.

2. **`Step` determinism (Core invariant #2, #3).** `Step` is a pure function:
   identical `(state, trigger)` must yield identical `(state, commands)`. Recording a span or
   incrementing a counter inside `Step` would introduce invisible I/O without breaking determinism
   per se, but it would violate invariant #1 (vendor import) and is the wrong abstraction layer —
   the engine does not know about deployments.

Three design forks required decisions:

**A. Where does the observability boundary sit?**
The candidates are the transport layer (REST/gRPC handler), the runtime (`Runner`), or the engine
core. The transport is the wrong layer for metrics that span the full lifecycle (e.g.
`instances_active` must count instances regardless of whether they were started via HTTP or gRPC).
The engine core is off-limits (invariant #1). The runtime's `deliverLoop` and `perform` already
see every trigger applied and every command executed — it is the natural chokepoint.

**B. OTel API directly, or an in-repo tracing/metrics port?**
The existing vendor isolation pattern (ADR-0008/ADR-0009/ADR-0010/ADR-0012) wraps vendored
libraries behind in-repo interfaces so they are swappable. However, OpenTelemetry's API packages
(`go.opentelemetry.io/otel`, `.../otel/metric`, `.../otel/trace`) are themselves the
vendor-neutral abstraction: the API is stable, provider-agnostic, and carries noop defaults.
Adding an in-repo re-export would be pure indirection with no swap benefit (you cannot realistically
swap OTel for another observability protocol at the module level). The watermill/casbin/gocron/
clockwork ports exist because those are implementation details that consumers might swap; OTel is
the interface consumers *configure*. Direct OTel API import is correct here.

**C. Manual W3C transport spans vs. OTel contrib packages.**
`go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` and the gRPC equivalent would
auto-instrument HTTP and gRPC handlers. Using them adds heavyweight transitive dependencies and
couples the transport layer to a specific OTel contrib version. Manual span creation (one `Start`
at handler/RPC entry, `End` in defer) is a dozen lines per transport and carries no transitive
baggage. Manual is chosen.

## Decision

**The runtime is the observability boundary.** Every metric and trace that `wrkflw` emits about
process execution is emitted by `runtime.Runner` (and its collaborators `runtime.TaskService`,
`transport/rest`, `transport/grpc`, `internal/eventing/watermill`, `internal/scheduling/gocron`,
`internal/persistence/postgres/relay`) — never from `engine/` or `model/`.

### (a) Engine purity guard

A test `TestCorePurityNoOTel` in `engine/purity_test.go` asserts, via `go list -f {{.Deps}}`
subprocess, that neither `engine` nor `model` transitively imports any
`go.opentelemetry.io` package. This guard must stay green permanently.

### (b) OTel API imported directly

`runtime/`, `transport/rest/`, `transport/grpc/`, `internal/observability/`,
`internal/eventing/watermill/`, and `internal/scheduling/gocron/` import the OTel API directly
(`go.opentelemetry.io/otel`, `.../otel/metric`, `.../otel/trace`). SDK packages
(`go.opentelemetry.io/otel/sdk/trace`, `.../sdk/metric`) appear **only in test files** — the
production code depends only on the API so a consumer can plug in any compliant SDK.

### (c) Manual W3C transport spans

`transport/rest` opens one span per HTTP request (`wrkflw.rest METHOD`) at the handler entry
point. `transport/grpc` opens one span per RPC (`wrkflw.grpc /<service>/<method>`) at the
interceptor level. Both propagate the W3C `traceparent`/`tracestate` headers manually. No
`otelhttp` / `otelgrpc` contrib packages are imported.

### (d) Per-component functional options with global defaults

Each component (`Runner`, `TaskService`, `transport/rest` handler, `transport/grpc` server,
`internal/eventing/watermill` publisher, `internal/scheduling/gocron` scheduler) accepts three
optional functional options:

- `WithLogger(*slog.Logger)` — defaults to `slog.Default()`.
- `WithTracerProvider(trace.TracerProvider)` — defaults to `otel.GetTracerProvider()` (the OTel
  global; noop if not set).
- `WithMeterProvider(metric.MeterProvider)` — defaults to `otel.GetMeterProvider()` (the OTel
  global; noop if not set).

These are implemented via the shared `internal/observability.Telemetry` helper and its
`internal/observability.New(scope, ...Option)` constructor. A consumer may configure once via
OTel globals (e.g. `otel.SetTracerProvider(myTP)`) and all components pick it up automatically,
or may override per-component for fine-grained control (e.g. silence the scheduler's logger with
`io.Discard`).

### (e) Span tree

```
[transport] wrkflw.rest METHOD          (one per HTTP request, manual W3C propagation)
[transport] wrkflw.grpc /<svc>/<method> (one per gRPC call, metadata propagation)
  └─ [runner]  wrkflw.runner.Run         (per r.Run call)
      └─ [runner]  wrkflw.step           (per engine.Step call within deliverLoop)
          └─ [runner]  wrkflw.action <name>  (per InvokeAction command)
  └─ [runner]  wrkflw.runner.Deliver     (per r.Deliver call, e.g. human-task / timer)
      └─ [runner]  wrkflw.step
          └─ [runner]  wrkflw.action <name>
[eventing]   wrkflw.eventing.publish     (per outbox event batch relay)
[relay]      wrkflw.relay.batch          (per relay DrainOnce batch)
```

### (f) Metric catalog

All metrics use the instrumentation scope `github.com/zakyalvan/krtlwrkflw/runtime`
(or the per-component scope). All attribute keys are lowercase snake_case.

| Metric | Type | Attributes | Description |
|---|---|---|---|
| `wrkflw_instances_started_total` | Counter | `def` | Process instances started. |
| `wrkflw_instances_completed_total` | Counter | `def`, `status` | Instances reaching a terminal state. |
| `wrkflw_instances_active` | UpDownCounter | — | Currently live (non-terminal) instances. |
| `wrkflw_step_duration_seconds` | Histogram | `trigger` | Duration of a single `engine.Step` call. |
| `wrkflw_action_duration_seconds` | Histogram | `action`, `outcome` | Duration of a service-action invocation. |
| `wrkflw_action_retries_total` | Counter | — | Service-action retries scheduled. |
| `wrkflw_incidents_raised_total` | Counter | `def` | Incidents raised. |
| `wrkflw_incidents_resolved_total` | Counter | `def` | Incidents resolved. |
| `wrkflw_human_tasks_total` | Counter | `event` | Human-task lifecycle transitions (`created`/`claimed`/`reassigned`/`completed`). |
| `wrkflw_eventing_published_total` | Counter | `status` | Outbox events published by the watermill adapter. |

### (g) Structured-log key conventions

All `slog` log calls use consistent, lowercase snake_case attribute keys:

| Key | Description |
|---|---|
| `instance_id` | Process instance identifier. |
| `def_id` | Process definition identifier. |
| `node_id` | Current engine node identifier. |
| `trigger` | Trigger type name (e.g. `StartInstance`, `TimerFired`). |
| `trace_id` | OTel trace ID (added by the shared `Telemetry.LogAttrs` helper). |
| `span_id` | OTel span ID (added by the shared `Telemetry.LogAttrs` helper). |

## Consequences

**Easier:**
- Engine core stays pure and is guarded permanently by `TestCorePurityNoOTel`.
- A consumer who does not configure any observability option incurs only noop overhead (the OTel
  noop tracer/meter and `slog.Default` are nearly free).
- A consumer configures observability once via OTel globals and all components pick it up, or
  overrides per-component for fine-grained control.
- The `internal/observability.Telemetry` helper is reusable across all components without
  duplicating the three-option wiring.
- The existing watermill eventing adapter's OTel span fits the same pattern and can be migrated
  onto the shared helper without a design change.

**Harder / deferred follow-ups (deliberate, not regressions):**

1. **Public `observability` root package** — a consumer-facing package exporting a ready-made
   trace-correlating `slog.Handler` (injects `trace_id`/`span_id` into log records) and a
   convenience `Setup(tp, mp, logger)` that configures OTel globals + injects into a runner.
   Currently the helper is `internal/observability` (unexported).
2. **Migrate eventing adapter onto the shared helper** — `internal/eventing/watermill` has its own
   With-option wiring; it should delegate to `internal/observability.New` for consistency.
3. **Async DB-backed `instances_active` gauge** — the current `wrkflw_instances_active`
   UpDownCounter is in-process only (resets to zero on restart). A true gauge would query
   the Postgres `wrkflw_instances` table for the live count. Deferred because it requires a
   periodic background query or an async observable gauge callback.
4. **`instances_active` mid-run abort caveat** — if a hard error aborts `deliverLoop` before the
   instance reaches a terminal state, `wrkflw_instances_active` is NOT decremented (the instance
   is errored but non-terminal from the counter's perspective). This is intentional: the instance
   is still "live" in the store (not terminal). Documented as a known semantic.
5. **Store-commit / `perform` error span coverage** — the `wrkflw.step` span ends before the
   `store.Commit` call; errors from `Commit` or `perform` are not recorded on the step span (they
   surface on the parent `wrkflw.runner.Run` or `wrkflw.runner.Deliver` span). A deeper span
   hierarchy covering commit and individual command execution is a follow-up.
6. **OTel contrib transport option** — `transport/rest` and `transport/grpc` could accept an
   `otelhttp`/`otelgrpc`-based option for consumers who prefer automatic propagation; manual is
   shipped as v1.
7. **Persistence `Store` (Load/Commit) spans and metrics** — the Postgres store operations are
   not instrumented; adding per-`Load`/`Commit` spans and a `wrkflw_store_duration_seconds`
   histogram is a follow-up for the Performance/caching track.
8. **REST route-template span naming** — the REST span name is currently `wrkflw.rest METHOD`
   (e.g. `wrkflw.rest GET`) because the route pattern (`r.Pattern`) is unavailable at middleware
   time in Go 1.22+ `http.ServeMux`. High-cardinality path segments (instance IDs) must not be
   in the span name. A route-template approach (e.g. `wrkflw.rest GET /instances/{id}`) requires
   per-handler span creation rather than a blanket middleware.
9. **Histogram exemplars** — exemplars linking histogram data points to trace IDs are not yet
   configured; an `exemplar.AlwaysOnFilter` option is a follow-up.
10. **REST/relay `WithMeterProvider` parity** — `transport/rest` and `internal/persistence/postgres/relay`
    accept `WithMeterProvider` for future use but emit no metrics yet; adding route-level request
    counters/latency histograms and relay throughput counters is a follow-up.
11. **Deferred span attributes (spec §5)** — the following span attributes named in spec §5 are
    deferred to a follow-up track: `wrkflw.node_id` and `wrkflw.attempt` on step/action spans;
    `http.status_code` and `http.route` on REST spans (requires a response-capturing
    `ResponseWriter` wrapper and route-template extraction); `wrkflw.instance_id` on gRPC spans.
