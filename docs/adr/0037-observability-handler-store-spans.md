# 37. Public trace-correlating slog handler + Store Load/Commit instrumentation

- Status: Accepted
- Date: 2026-06-23

## Context

ADR-0019 established the observability architecture (the runtime as the boundary;
`internal/observability.Telemetry`; per-component provider options). Two deferred gaps remain:

1. No **public** trace-correlating `slog.Handler`. `internal/observability.LogAttrs(ctx)` returns bare
   `trace_id`/`span_id` attrs that each call site appends manually; consumers have no exported handler
   that injects them automatically into every record.
2. The persistence **Store** (`Load`/`Commit`, the hot path) is uninstrumented — no spans, no duration
   metric — unlike the relay and the runner.

## Decision

Two additive, non-engine changes (defaults to noop; no behaviour change):

**A. Public `observability` root package.** A new exported package with a trace-correlating
`slog.Handler`: `NewHandler(base slog.Handler) slog.Handler` (and `NewLogger` convenience). Its
`Handle(ctx, rec)` appends `trace_id`/`span_id` from `trace.SpanContextFromContext(ctx)` when valid,
then delegates to `base`; `WithAttrs`/`WithGroup`/`Enabled` delegate (wrapping the result). A logger
built from it auto-correlates with the active span — the opt-in public replacement for manual
`LogAttrs` appends. It depends only on `log/slog` + `go.opentelemetry.io/otel/trace`.

**B. Store instrumentation.** The Postgres `Store` gains `WithStore{Logger,TracerProvider,MeterProvider}`
options (mirroring the relay's `WithRelay*`), building an `observability.Telemetry` +
`wrkflw_store_duration_seconds` histogram. `Load`/`Commit` are wrapped in `wrkflw.store.load` /
`wrkflw.store.commit` spans and record the histogram with an `op` attribute. The `persistence` façade
re-exports the options. Duration is measured with `time.Since` (the persistence layer, not the pure
engine, may read the wall clock).

A `Setup` convenience that grabs OTel globals is **deferred** — the consumer owns OTel SDK setup; the
library configures providers only through the explicit options.

## Consequences

**Positive**
- Consumers get trace-correlated logs across the whole library by mounting one handler on the logger
  they pass to the `WithLogger` options — no per-call-site changes.
- The persistence hot path is now observable (spans + a duration histogram), closing the largest
  remaining instrumentation gap; mirrors the established relay/runner patterns.
- Additive and opt-in: default wiring is noop, so existing behaviour and tests are unchanged.

**Negative / trade-offs**
- The public `observability` package minimally duplicates the trace-id extraction logic already in
  `internal/observability.LogAttrs` (kept dependency-light rather than coupling the public package to
  `internal/`).
- Existing internal call sites keep their manual `LogAttrs` appends (not refactored onto the new
  handler) — both work; a future cleanup could migrate them.
- `Setup`-grabs-globals deferred — consumers must wire their OTel SDK and pass providers explicitly.
