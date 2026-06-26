# 0070. Observability nits: CallNotifier span, relay/REST meters, route-template spans

Status: Accepted — 2026-06-27
Follow-up to: ADR-0037 (observability root package, Store spans/metrics).

## Context

Three observability gaps remained after ADR-0037: the CallNotifier emitted no telemetry (unlike
the parallel Relay batch span); `transport/rest` and the postgres Relay accepted a MeterProvider
option but recorded no instruments (declared-but-unused); and REST server spans were named by HTTP
method only, with the raw path in an attribute, so requests were not grouped by route template.
(The backlog's "async DB-backed instances_active gauge" item is moot — `wrkflw_instances_active`
already exists in `runtime/observability.go`.)

## Decision

Additive instrumentation only, via the existing `observability.Telemetry` + staged-option pattern:

- **CallNotifier**: add Telemetry (logger/tracer/meter options mirroring Relay); wrap `DrainOnce`
  in a `wrkflw.callnotifier.batch` span and record `wrkflw_callnotifier_links_notified_total`.
- **Relay**: record `wrkflw_relay_events_published_total` and `wrkflw_relay_batch_duration_seconds`
  per drain (the meter was already wired but unused).
- **REST**: record `wrkflw_rest_requests_total` and `wrkflw_rest_request_duration_seconds`; rename
  server spans to `wrkflw.rest <METHOD> <route-template>` and set `http.route`, keyed by the matched
  ServeMux pattern (`http.Request.Pattern`, read after routing). Route template — not concrete path —
  is the span name and the only path-derived metric label, keeping cardinality bounded.

## Consequences

- New metric series (`wrkflw_callnotifier_*`, `wrkflw_relay_*`, `wrkflw_rest_*`) appear for consumers
  who wire a real MeterProvider; the previously-ignored `WithMeterProvider`/`WithRelayMeterProvider`
  options now do something. No behaviour change to request/relay handling.
- REST span names become route-template-grouped (better aggregation); the raw path remains available
  as the `http.target` attribute.
- The span rename + metric record happen AFTER `next.ServeHTTP` because `r.Pattern` is only populated
  once the mux matches — documented to prevent a future refactor from moving it before routing.
- engine/ and model/ remain untouched (observability stays outside the pure stepper, per ADR-0037).
- Still deferred (separate backlog items): OTel SDK setup helper, exemplars, OTel-contrib HTTP
  middleware, per-aggregate relay ordering.
