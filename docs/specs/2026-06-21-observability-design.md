# Observability (Metrics / Traces / Structured Logging) — Design Spec

Status: **Approved** (design forks decided 2026-06-21) · Date: 2026-06-21
Sub-project: **Observability** — third track of the deferred-backlog run.
Drives the project requirement: *"This library must be able to expose process metrics, enable traces,
using slog golang logger."*

This spec is the contract. When a plan and this spec disagree, **the spec wins** — except where
the spec lists example code: trust the current source over any listing (the engine has grown a
lot; ground every edit against the then-current code).

## 1. Scope & decisions

The design forks were decided up front:

| Fork | Decision |
|---|---|
| OTel dependency shape | **OTel API directly** in `runtime`/`service`/`transport`/`internal` — no in-repo tracing/metrics port. The OTel API *is* the vendor-neutral abstraction (unlike watermill/casbin/gocron/clockwork). The **engine core stays pure** — zero OTel imports in `engine/` + `model/`. |
| Instrumentation boundary | **The runtime instruments *around* the pure `Step` and `perform`.** The engine never sees a span/meter/logger; the runtime supplies all observability context (instance/def/node/trigger/command-count). |
| Transport tracing | **Manual spans, no new deps.** Extract/inject W3C tracecontext via `otel.GetTextMapPropagator()`; create request spans in our own REST/gRPC code. **No** `otelhttp`/`otelgrpc` contrib dependency. |
| Metric catalog | **Full catalog** (instance lifecycle counters + active gauge + step/action duration histograms + retry/incident/human-task counters). |
| Wiring | **Per-component functional options** `WithLogger` / `WithTracerProvider` / `WithMeterProvider`, mirroring the existing **eventing** adapter. Defaults: `slog.Default()` + OTel **global** providers + noop fallback. |
| Shared helper | A single internal **`internal/observability.Telemetry`** value centralizes the noop-fallback boilerplate, pre-builds instruments, and supplies log↔trace correlation attrs. |

Constraints fixed by existing invariants (HANDOVER "Core invariants"):

- **The pure core never imports OTel and never reads the clock.** `engine/` and `model/` keep
  their stdlib-only (+ `model`/`authz`/`humantask`/`expreval`) dependency set. All spans, metrics,
  and logs are emitted by the **runtime** and the outer layers.
- **`Step` stays deterministic and pure.** Observability is a pure side-effect wrapper *around*
  `Step`; it reads `StepResult` but never alters `(state, commands)`. No new `InstanceState` field
  is introduced for observability.
- **Backward compatible / opt-in.** Absent configured providers, every component uses
  `slog.Default()` + the OTel **noop** tracer/meter. No behaviour changes; all existing tests stay
  green. A consumer who configures nothing pays only the negligible noop overhead.

Research backing (recorded in ADR-0019): OpenTelemetry semantic conventions (span naming, the
`messaging`/`rpc` attribute families), the OTel Go "instrumentation library" idiom (per-library
`TracerProvider`/`MeterProvider` with global-provider defaults), Prometheus metric/label naming
(`_total` counters, `_seconds` histograms, low-cardinality labels), and slog structured-logging +
trace-correlation (`trace_id`/`span_id` on every log record).

## 2. Architecture overview

```
   transport (REST / gRPC)                     ── ROOT span; W3C propagation in/out
   │   extract tracecontext → ctx
   │   span "wrkflw.rest POST /instances" / "wrkflw.grpc StartInstance"
   ▼
   service.Service                             ── pass-through (carries ctx; no own span in v1)
   ▼
   runtime.Runner                              ── OBSERVABILITY BOUNDARY (owns logger/tracer/meter)
   │   span "wrkflw.runner.Run"  /  "wrkflw.runner.Deliver"
   │   ├─ metric: instances_started_total, instances_active++           (on first Run/Start)
   │   ├─ for each trigger applied:
   │   │     span "wrkflw.step" {instance,def,node,trigger,commands,status}
   │   │     metric: step_duration_seconds{trigger}                     (around the pure Step call)
   │   │        │
   │   │        └── engine.Step(...)            ── PURE. zero OTel. returns (state, commands)
   │   │
   │   ├─ perform(cmd):
   │   │     span "wrkflw.action <name>" {action,node,attempt}          (side-effecting commands)
   │   │     metric: action_duration_seconds{action,outcome}, action_retries_total{action}
   │   │     metric (human task): human_tasks_total{event}
   │   │
   │   └─ on terminal: instances_completed_total{def,status}, instances_active--
   │      on incident:  incidents_raised_total{def} ; on resolve: incidents_resolved_total
   │
   internal/scheduling/gocron, internal/persistence/postgres (relay), internal/eventing/watermill
       ── each: injected logger + tracer + meter (eventing already done; relay/scheduler added)

   slog: every log record is context-aware (…Context) and carries trace_id/span_id (correlation)
```

The **engine core is untouched**: `Step` keeps its exact signature and purity. The runtime computes
all attributes from its own knowledge of the trigger it is applying and the `StepResult` it gets
back — it does not need the engine to emit anything.

## 3. The shared `internal/observability` helper

A single internal package removes the noop-fallback duplication currently inlined in the eventing
adapter and gives every layer one consistent construction path.

```go
package observability

// Telemetry bundles the three signals. The zero value is unusable; build via New.
type Telemetry struct {
    Logger *slog.Logger
    Tracer trace.Tracer
    Meter  metric.Meter
}

type Option func(*config)

func WithLogger(l *slog.Logger) Option            // nil → keep default
func WithTracerProvider(tp trace.TracerProvider) Option // nil → otel global
func WithMeterProvider(mp metric.MeterProvider) Option  // nil → otel global

// New builds a Telemetry for a given instrumentation scope (e.g.
// "github.com/kartaladev/wrkflw/runtime"). Defaults: slog.Default(),
// otel.GetTracerProvider(), otel.GetMeterProvider().
func New(instrumentationName string, opts ...Option) Telemetry

// LogAttrs returns slog attrs carrying trace_id/span_id from ctx's span (empty if none),
// so logs correlate to traces. Callers spread it into …Context log calls.
func (t Telemetry) LogAttrs(ctx context.Context) []slog.Attr
```

Each consuming package (`runtime`, `transport/rest`, `transport/grpc`, the `internal/*` adapters)
defines a small instrument struct built once from a `Telemetry` (counters/histograms created with
the noop-on-error fallback the eventing adapter already uses). Instruments are created once at
construction, not per call.

> The already-merged **eventing** adapter keeps its working three-option wiring as-is; migrating it
> onto this shared helper is an optional, non-blocking cleanup (deferred follow-up §10) to avoid
> destabilizing a shipped package.

## 4. Public wiring (per-component options, mirrors eventing)

Every public constructor gains three options with the **same names and semantics** as
`eventing.WithLogger`/`WithTracerProvider`/`WithMeterProvider`:

| Constructor | New options |
|---|---|
| `runtime.NewRunner(cat, clk, store, ...Option)` | `runtime.WithLogger` / `WithTracerProvider` / `WithMeterProvider` |
| `rest.NewHandler(svc, ...Option)` | `rest.WithLogger` / `WithTracerProvider` / `WithMeterProvider` |
| gRPC registration `RegisterWorkflowServiceServer(s, svc, ...Option)` | gains a **variadic** `transport/grpc.WithLogger` / `WithTracerProvider` / `WithMeterProvider`; the options configure the server impl wrapper before registration |
| `scheduling.NewScheduler(clock, ...Option)` | `scheduling.WithLogger` / `WithTracerProvider` / `WithMeterProvider` |
| `persistence.NewRelay(...)` | `persistence.WithLogger` / `WithTracerProvider` / `WithMeterProvider` |

**Ergonomics:** because the defaults are the OTel *global* providers, a consumer can configure
**once** — `otel.SetTracerProvider(tp)`, `otel.SetMeterProvider(mp)`, `slog.SetDefault(l)` — and
every component is instrumented with no per-component option at all. Per-component options exist for
consumers who want distinct providers per subsystem.

## 5. Trace catalog (spans — all emitted outside the pure core)

| Span | Emitted by | Key attributes |
|---|---|---|
| `wrkflw.rest <METHOD> <route>` | `transport/rest` | `http.method`, `http.route`, `http.status_code`, `wrkflw.instance_id` |
| `wrkflw.grpc <Method>` | `transport/grpc` | `rpc.system=grpc`, `rpc.method`, `wrkflw.instance_id`, status code |
| `wrkflw.runner.Run` | `runtime` | `wrkflw.def_id`, `wrkflw.def_version`, `wrkflw.instance_id`, terminal `wrkflw.status` |
| `wrkflw.runner.Deliver` | `runtime` | `wrkflw.instance_id`, `wrkflw.trigger` |
| `wrkflw.step` | `runtime` (around `Step`) | `wrkflw.instance_id`, `wrkflw.def_id`, `wrkflw.node_id`, `wrkflw.trigger`, `wrkflw.command_count`, `wrkflw.status` |
| `wrkflw.action <name>` | `runtime` (around side-effecting `perform`) | `wrkflw.action`, `wrkflw.node_id`, `wrkflw.attempt`; records error on failure |

**Propagation:** transports `Extract` inbound W3C tracecontext into the request `ctx` before any
span; the runtime threads that `ctx` through `Run`/`Deliver`/`Step`/`perform`. **Call-activity child
instances nest** because the parent's span context rides the `ctx` into the child `Run`. Errors call
`span.RecordError(err)` + `span.SetStatus(codes.Error, …)` (the eventing adapter's exact idiom).

## 6. Metric catalog (full)

All instruments live on the per-package meter (instrumentation scope =
`github.com/kartaladev/wrkflw/<pkg>`). Labels are deliberately **low-cardinality** (never the
instance id).

| Instrument | Type | Labels | Emitted when |
|---|---|---|---|
| `wrkflw_instances_started_total` | Int64Counter | `def` | a new instance starts (`Run` of a fresh instance) |
| `wrkflw_instances_completed_total` | Int64Counter | `def`, `status` (`completed`/`failed`/`cancelled`) | instance reaches a terminal state |
| `wrkflw_instances_active` | Int64UpDownCounter | — | +1 on start, −1 on terminal (live-instance gauge) |
| `wrkflw_step_duration_seconds` | Float64Histogram | `trigger` | around each pure `Step` call |
| `wrkflw_action_duration_seconds` | Float64Histogram | `action`, `outcome` (`ok`/`error`) | around each service-action `perform` |
| `wrkflw_action_retries_total` | Int64Counter | `action` | a retry is scheduled (`TimerRetry`) for an action |
| `wrkflw_incidents_raised_total` | Int64Counter | `def` | an `Incident` is raised |
| `wrkflw_incidents_resolved_total` | Int64Counter | `def` | `ResolveIncident` clears one |
| `wrkflw_human_tasks_total` | Int64Counter | `event` (`created`/`claimed`/`reassigned`/`completed`) | the corresponding human-task transition |

Counters are derived from observed `StepResult` commands/state transitions and from the
runtime's `perform` path — never from inside the engine. `instances_active` uses an UpDownCounter
(synchronous gauge) so a scrape sees current live instances without an async callback over the DB.

## 7. slog conventions

- **Inject a `*slog.Logger`** into the Runner (new field + `WithLogger`), the gocron scheduler, and
  the REST handler — replacing today's package-global `slog.Error(...)` calls.
- **Context-aware always:** use `InfoContext`/`ErrorContext`/`DebugContext` so a handler can pull
  request-scoped values, and so `Telemetry.LogAttrs(ctx)` can attach `trace_id`/`span_id`.
- **Standard attribute keys** (kebab→snake to match OTel attrs): `instance_id`, `def_id`,
  `def_version`, `node_id`, `token_id`, `trigger`, `attempt`, `status`, plus `trace_id`/`span_id`.
- **Levels:** `Info` for lifecycle (instance started/completed, incident raised/resolved); `Debug`
  for per-step/per-trigger detail; `Error` for action failures, CAS-exhaustion, relay/publish
  errors. No `Warn` spam on expected control flow (e.g. a normal park).

## 8. Layer-by-layer work

1. **`internal/observability`** — `Telemetry`, `New`, the three options, `LogAttrs`. (New package.)
2. **`runtime`** — logger/tracer/meter fields + three options; build a runner instrument set; wrap
   `Run`/`Deliver`/`Step`/`perform` with spans + metrics; emit lifecycle counters from terminal
   detection; replace the two global `slog.Error` call-sites (timer-fire CAS path) with the injected
   context logger + correlation attrs.
3. **`service`** — thread `ctx` only (no own span in v1; it is a thin pass-through). No new deps.
4. **`transport/rest`** — `WithLogger`/`WithTracerProvider`/`WithMeterProvider`; per-request span via
   the propagator + manual `tracer.Start`; inject the logger (replace the global `slog.Error`).
5. **`transport/grpc`** — per-RPC span via the propagator + manual `tracer.Start`; status mapping
   already exists; add span error status.
6. **`internal/scheduling/gocron`** — inject logger (replace the two global `slog.Error` calls);
   optional `timer scheduled/fired` debug logs + a small span around the fire callback.
7. **`internal/persistence/postgres` relay** — `WithLogger`/`WithTracerProvider`/`WithMeterProvider`;
   span around a publish batch; `relay` already counts published — add nothing required beyond a span
   and structured logs (the DLQ counters are resilience-owned; not duplicated here).
8. **Docs/example** — a testable example wiring an SDK `TracerProvider` + `MeterProvider` + a slog
   handler into a `Runner`, asserting a span tree + a counter increment (mirrors
   `eventing/eventing_test.go`). ADR-0019. HANDOVER update.

## 9. Testing

- **Black-box** `_test` packages, table-driven with the project `assert`-closure form, `t.Context()`.
- **Traces:** `sdktrace.NewTracerProvider(WithSpanProcessor(tracetest.NewSpanRecorder()))`; assert
  span names, parent/child nesting (call-activity), and attributes. (Pattern already in
  `eventing/eventing_test.go`.)
- **Metrics:** `metric.NewManualReader()` + `mp.Collect`; assert instrument presence, value, and
  labels.
- **Logs:** a capturing `slog.Handler` (records `slog.Record`s); assert level, message, and the
  presence of standard keys incl. `trace_id` when a span is active.
- **Purity guard:** add a new import-guard test (none exists today; prior tracks verified purity by
  hand) that parses every non-test `.go` file in `engine/` and `model/` and asserts **none** import a
  `go.opentelemetry.io/...` package. This becomes the regression net for the core's purity.
- Coverage ≥ 85% on every touched package; `go test -race ./...` green; `golangci-lint run ./...`
  clean. (Run the Postgres package with limited container parallelism — `-p 1` — per the resilience
  track's note.)

## 10. Deferred follow-ups (explicitly out of scope for this track)

1. **Public `observability` root package** — a ready-made trace-correlating `slog.Handler` and a
   one-call `Telemetry` the consumer builds once and passes via a single `WithObservability(tel)`
   option on every constructor. v1 uses per-component options + OTel globals instead.
2. **Migrate the eventing adapter onto `internal/observability`** — DRY cleanup; deferred to avoid
   churning a shipped package.
3. **Async gauge over the DB** — `instances_active` is a process-local UpDownCounter; a cross-process
   accurate gauge (async callback querying `wrkflw_instances WHERE status='running'`) is a follow-up.
4. **OTel contrib transports** — if a consumer wants standard HTTP/RPC server metrics and
   auto-propagation, swapping our manual spans for `otelhttp`/`otelgrpc` is a later option.
5. **Persistence `Store` span/metrics** — per-query tracing + store-latency histograms on the
   Postgres `Store` (Load/Commit) are deferred; this track covers the relay + the runtime boundary.
6. **Exemplars / span-metric correlation** — attaching trace exemplars to histograms is a follow-up.
