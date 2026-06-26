# Plan — L1: Observability nits

Spec: `docs/specs/2026-06-27-observability-nits-design.md`. ADR: `docs/adr/0070-observability-nits.md`.
Branch: `feat/observability-nits`. Module: `github.com/zakyalvan/krtlwrkflw`.

## Global Constraints (binding — copy to reviewers verbatim)

- Additive ONLY: no change to request/relay/notifier control flow or outputs. Telemetry wired via the
  existing `observability.Telemetry` + staged-option pattern (mirror `Relay` in
  `internal/persistence/postgres/relay.go`). Use the `Telemetry.Int64Counter` / `Float64Histogram`
  helpers (already noop-safe).
- Instrument names EXACTLY (convention `wrkflw_<area>_<name>_<unit>`):
  `wrkflw_callnotifier_links_notified_total`, `wrkflw_relay_events_published_total`,
  `wrkflw_relay_batch_duration_seconds`, `wrkflw_rest_requests_total`,
  `wrkflw_rest_request_duration_seconds`.
- Span names EXACTLY: `wrkflw.callnotifier.batch`; REST `wrkflw.rest <METHOD> <route-template>`.
- REST route template comes from `r.Pattern` (populated only AFTER the mux matches) → the span rename
  and metric record MUST be after `next.ServeHTTP`. Empty pattern → label `"unmatched"`. Route
  template is the ONLY path-derived metric label / span-name component (cardinality).
- engine/ and model/ untouched. TDD with in-memory OTel (sdktrace in-memory exporter, sdkmetric manual
  reader) — follow the existing relay tracer test for the pattern. table-test style where cases share a SUT.
- Gate: `go test -race ./runtime/... ./transport/rest/... ./internal/persistence/postgres/...` green;
  touched pkgs ≥85%; `golangci-lint run ./...` clean; gofmt clean.

## Task 1 — CallNotifier span + counter (`runtime/call_notifier.go`)
- Red: in-memory-tracer test asserting `DrainOnce` produces a `wrkflw.callnotifier.batch` span; meter
  test asserting `wrkflw_callnotifier_links_notified_total` increments by the number of links notified.
- Green: add `tel observability.Telemetry` + staged `logOpt/tpOpt/mpOpt` + options
  `WithCallNotifierLogger`/`WithCallNotifierTracerProvider`/`WithCallNotifierMeterProvider`; assemble
  `tel` in the constructor (scope `github.com/zakyalvan/krtlwrkflw/runtime`); wrap `DrainOnce` in the
  span and record the counter. Confirm existing CallNotifier callers/constructor signature stay
  compatible (options are additive/variadic).

## Task 2 — Relay metrics (`internal/persistence/postgres/relay.go`)
- Red: metric test (in-memory reader) asserting after a `DrainOnce` that drains N events,
  `wrkflw_relay_events_published_total` == N and `wrkflw_relay_batch_duration_seconds` recorded ≥1 obs.
- Green: create the two instruments in `NewRelay` from `r.tel`; record in `DrainOnce` alongside the
  existing `wrkflw.relay.batch` span; update the `WithRelayMeterProvider` doc (drop "creates no
  metric instruments"). (testcontainers Postgres — gate via the existing relay test harness.)

## Task 3 — REST metrics + route-template spans (`transport/rest`)
- Red: in-memory-tracer+meter test: a request to a templated route (e.g. `/instances/{id}/snapshot`)
  yields span name `wrkflw.rest GET /instances/{id}/snapshot` with `http.route` set to the template,
  and `wrkflw_rest_requests_total` / `_request_duration_seconds` recorded with the template + method +
  status attrs; an unmatched path → `"unmatched"`.
- Green: create the two REST instruments from the existing meter; in the trace middleware wrap the
  ResponseWriter to capture status; after `next.ServeHTTP`, read `r.Pattern`, rename the span, set
  `http.route`, and record both metrics. Keep `http.target` (raw path) attribute as-is.

## Verification checklist
- [ ] T1 span + counter red→green; CallNotifier options additive.
- [ ] T2 relay counter + histogram red→green; doc updated.
- [ ] T3 REST metrics + route-template span red→green; unmatched-path case covered.
- [ ] No control-flow/behaviour change (handlers/relay/notifier outputs identical).
- [ ] `go test -race` green on the three packages; ≥85%; lint 0; gofmt clean; engine/model untouched.
- [ ] ADR-0070 + spec committed; HANDOVER updated; whole-branch opus review clean.
