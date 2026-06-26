# Spec — Observability nits (CallNotifier span, relay/REST meters, route-template spans)

Date: 2026-06-27
Status: Accepted (autonomous backlog program, track L1)
Relates to: ADR-0037 (observability root pkg + Store spans/metrics)

## Problem

Three observability gaps from the HANDOVER backlog (the "async DB-backed instances_active gauge"
item is dropped — `wrkflw_instances_active` already exists in `runtime/observability.go`):

1. **CallNotifier has no telemetry at all.** `runtime/call_notifier.go` `DrainOnce` has no span,
   tracer, or meter — unlike the structurally-parallel `Relay` which emits a `wrkflw.relay.batch`
   span. A drained batch of call-link notifications is invisible to tracing.
2. **Declared-but-unused meters.** `transport/rest` accepts `WithMeterProvider` and
   `internal/persistence/postgres` Relay accepts `WithRelayMeterProvider`, but neither creates or
   records any instrument — the option is misleadingly accepted and ignored.
3. **Coarse REST span naming.** REST server spans are named `"wrkflw.rest <METHOD>"` with the raw
   path in an attribute, so `/instances/{id}` requests are not grouped by route template.

## Goals (additive only; no behaviour change to request handling)

- A `wrkflw.callnotifier.batch` span around CallNotifier `DrainOnce`, plus a meaningful counter so
  the accompanying meter is not itself dead.
- Real relay metrics recorded per drain.
- Real REST request metrics recorded per request.
- REST span names + metric route labels keyed by the matched route TEMPLATE (low cardinality).

All instrument names follow the existing `wrkflw_<area>_<name>_<unit>` convention. Telemetry is wired
through the existing `observability.Telemetry` + staged-option pattern (mirror `Relay`).

## Design

### 1. CallNotifier telemetry (`runtime/call_notifier.go`)
- Add a `tel observability.Telemetry` field, staged `logOpt/tpOpt/mpOpt`, and options
  `WithCallNotifierLogger`, `WithCallNotifierTracerProvider`, `WithCallNotifierMeterProvider`
  (mirror the Relay options), assembling `tel` in the constructor scoped to the runtime pkg path.
- Wrap `DrainOnce` body in `ctx, span := n.tel.Tracer.Start(ctx, "wrkflw.callnotifier.batch")`,
  `defer span.End()`. Record a counter `wrkflw_callnotifier_links_notified_total` (Int64Counter)
  incremented by the number of links notified in the batch — so the meter is exercised, not dead.

### 2. Relay metrics (`internal/persistence/postgres/relay.go`)
- The Telemetry/Meter is already wired (`WithRelayMeterProvider`). Create instruments in `NewRelay`:
  - `wrkflw_relay_events_published_total` (Int64Counter) — incremented by published count per drain.
  - `wrkflw_relay_batch_duration_seconds` (Float64Histogram) — per-`DrainOnce` wall time.
- Record them inside `DrainOnce` (the same scope as the existing `wrkflw.relay.batch` span).
- Update the `WithRelayMeterProvider` doc comment (drop "creates no metric instruments").

### 3 + 4. REST metrics + route-template spans (`transport/rest`)
- In `NewRelay`-equivalent REST construction, create instruments from the existing meter:
  - `wrkflw_rest_requests_total` (Int64Counter; attrs: `http.method`, `http.route`, `http.status_code`).
  - `wrkflw_rest_request_duration_seconds` (Float64Histogram; same route/method attrs).
- In the trace middleware: capture a status-recording `http.ResponseWriter` wrapper; after
  `next.ServeHTTP`, read the matched route template from `r.Pattern` (Go 1.22+ `net/http` ServeMux
  populates it post-routing). Use the template (fallback to `"unmatched"` when empty) to:
  - rename the span to `"wrkflw.rest <METHOD> <route-template>"` via `span.SetName(...)`,
  - set the `http.route` span attribute,
  - record both metrics with the route-template + status attributes.
- Raw path stays only as the existing `http.target` attribute (high-cardinality, not a metric label).

## Testing (in-memory OTel, no external infra)

- Tracing: `sdktrace.NewTracerProvider` with an in-memory span exporter (follow the existing relay
  tracer test pattern); assert the new span names appear and (REST) carry the route-template `http.route`.
- Metrics: `sdkmetric.NewMeterProvider` with a `manual`/in-memory reader; collect and assert each new
  instrument records the expected value (e.g. published_total == N, requests_total has the route attr).
- Route-template: a request to `/instances/{id}/snapshot` yields span name + metric label with the
  template, not the concrete id; an unmatched path yields `"unmatched"`.

Gate: `go test -race ./runtime/... ./transport/rest/... ./internal/persistence/postgres/...` green
(testcontainers for postgres), touched pkgs ≥85%, `golangci-lint run ./...` clean, gofmt clean,
engine/model untouched.

## Non-goals / risk

- No new OTel SDK setup helper (consumer owns SDK wiring — ADR-0037 deferral stands).
- No exemplars / OTel-contrib middleware / per-aggregate relay ordering (separate backlog items).
- Risk is low (additive instruments + span rename). The one subtlety: `r.Pattern` is only populated
  after the mux matches, so the span rename/metric record MUST happen after `next.ServeHTTP` returns.
