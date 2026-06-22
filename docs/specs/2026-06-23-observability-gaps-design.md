# Design: public observability handler + Store Load/Commit instrumentation

**Date:** 2026-06-23
**Status:** Approved (user-chosen high-value subset)
**Track:** Backlog (Observability). Follow-up to ADR-0019.
**ADR:** 0037.

## 1. Problem & scope

ADR-0019 made the runtime the observability boundary (`internal/observability.Telemetry` +
per-component `WithLogger`/`WithTracerProvider`/`WithMeterProvider`). Two gaps remain from its
deferred list:

- **A. No public trace-correlating slog handler.** `internal/observability.LogAttrs(ctx)` returns
  bare `trace_id`/`span_id` attrs that each call site appends manually; there is no public,
  consumer-facing `slog.Handler` that injects them automatically into every record. (Internal, not
  exported.)
- **B. The persistence Store (Load/Commit) is uninstrumented.** The Postgres `Store` holds no
  `Telemetry`; its `Load`/`Commit` (the hot persistence path) emit no spans and no duration metric,
  unlike the relay (`wrkflw.relay.batch` span) and the runner (`wrkflw_step_duration_seconds`).

**In scope:**
- **A.** A new **public `observability` root package** exporting a trace-correlating `slog.Handler`
  (`NewHandler`) + a `NewLogger` convenience. Non-engine, additive.
- **B.** `WithStore{Logger,TracerProvider,MeterProvider}` options on the Postgres `Store` (+ façade);
  `Load`/`Commit` wrapped in `wrkflw.store.load` / `wrkflw.store.commit` spans and a
  `wrkflw_store_duration_seconds{op}` histogram.

**Out of scope (deferred, documented):** a `Setup` that grabs OTel globals — the consumer owns OTel
SDK setup; we won't `otel.SetTracerProvider` on their behalf (only document the wiring). Migrating
every existing call site off manual `LogAttrs` onto the new handler (the handler is provided for
consumers; internal call sites keep working). MemStore instrumentation (test/reference only).

## 2. Part A — public `observability` root package

A new `observability/` root package (the product's public surface; `internal/observability` stays the
internal impl). It exports a trace-correlating `slog.Handler`:

```go
// observability/handler.go
package observability

// NewHandler wraps base so that every record carries trace_id and span_id from the
// span in the record's context (when a valid span is present). Mount it on a
// *slog.Logger and pass that logger to the engine's WithLogger options to get
// trace-correlated logs across the whole library with no per-call-site changes.
func NewHandler(base slog.Handler) slog.Handler

// NewLogger is a convenience: slog.New(NewHandler(base)).
func NewLogger(base slog.Handler) *slog.Logger
```

`handler`'s `Handle(ctx, rec)` reads `trace.SpanContextFromContext(ctx)`; if valid it appends
`trace_id`/`span_id` attrs to `rec` before delegating to `base.Handle`. `WithAttrs`/`WithGroup`/
`Enabled` delegate to `base` (wrapping the returned handler so correlation survives). slog passes the
logging-call ctx to `Handle`, so a logger built from `NewHandler` auto-correlates with the active span
— this is the public, opt-in replacement for manual `LogAttrs` appends.

Implementation reuses the trace-id/span-id extraction already in `internal/observability.LogAttrs`
(duplicated minimally or the internal helper exported-by-value — keep the root package dependency-free
of `internal/observability` if simplest; it only needs `log/slog` + `go.opentelemetry.io/otel/trace`).

## 3. Part B — Store Load/Commit instrumentation

Mirror the relay's telemetry wiring (`postgres/relay.go`):

- `postgres.Store` gains `logOpt/tpOpt/mpOpt observability.Option` + `tel observability.Telemetry` +
  a `storeDuration metric.Float64Histogram`. `NewStore(pool, opts...)` builds `tel =
  observability.New("github.com/zakyalvan/krtlwrkflw/persistence", …)` and the histogram
  (`wrkflw_store_duration_seconds`, "Duration of persistence Store operations in seconds").
- New `StoreOption`s: `WithStoreLogger(*slog.Logger)`, `WithStoreTracerProvider(trace.TracerProvider)`,
  `WithStoreMeterProvider(metric.MeterProvider)` (mirroring `WithRelay*`).
- `Load`: `ctx, span := s.tel.Tracer.Start(ctx, "wrkflw.store.load"); defer span.End()`; record
  `storeDuration` with `attribute.String("op","load")` and the elapsed seconds (via a captured
  start time; the Store has no clock today — use a monotonic `time.Now()` *for duration only*, which
  is acceptable in the persistence layer, OR add a clock option — choose: a plain `time.Since(start)`
  for the metric is fine here, persistence is not the pure engine). On error set the span error.
- `Commit`: same with `"wrkflw.store.commit"` / `op="commit"`. Span records `wrkflw.committed`
  outcome (a bool attr on the version-CAS conflict path is optional).
- Façade: `persistence.OpenPostgres` (or `NewStore` equivalent) re-exports the three store options
  (`persistence.WithStoreTracerProvider`, etc.) so a consumer enables it through the façade.
- Default (no options) → noop tracer/meter (OTel globals), so existing behaviour/tests are unchanged.

## 4. Testing strategy

- **observability (root pkg, `observability_test`):** a `NewHandler` over a record-capturing base
  handler: a log call made within a started span carries `trace_id`/`span_id` matching the span; a
  log call with no active span carries neither; `WithAttrs`/`WithGroup` still delegate.
- **internal/persistence/postgres (testcontainers + SDK recorder, mirror
  `relay_observability_test.go`):** with `WithStoreTracerProvider(sr)` + `WithStoreMeterProvider(reader)`,
  a `Create`→`Load`→`Commit` cycle produces `wrkflw.store.load` + `wrkflw.store.commit` spans and ≥1
  `wrkflw_store_duration_seconds` observation per op (`op` attr present); default (no options) → no panic.

**Gate:** `go test -race -p 1 ./...` green; ≥85% on `observability`, `internal/persistence/postgres`;
`golangci-lint` clean; engine/model diff ZERO; no new forbidden vendor imports; existing store/relay
tests unchanged.

## 5. ADR

| ADR | Decision |
|---|---|
| **0037** | (A) Add a public `observability` root package exporting a trace-correlating `slog.Handler` (`NewHandler`/`NewLogger`) that auto-injects `trace_id`/`span_id` from the record's span context — the opt-in public replacement for manual `LogAttrs` appends. (B) Instrument the Postgres `Store`: `WithStore{Logger,TracerProvider,MeterProvider}` options (mirroring the relay) wrap `Load`/`Commit` in `wrkflw.store.load`/`wrkflw.store.commit` spans + a `wrkflw_store_duration_seconds{op}` histogram. A `Setup`-that-grabs-OTel-globals is deferred (consumer owns SDK setup). Non-engine; engine/model untouched; defaults to noop (no behaviour change). |
