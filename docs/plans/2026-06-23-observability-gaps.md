# Observability gaps — Implementation Plan

> Executed via superpowers:subagent-driven-development. Strict TDD. Non-engine; defaults to noop (no behaviour change).

**Goal:** (A) a public `observability` root pkg with a trace-correlating slog.Handler; (B) Postgres Store Load/Commit spans + `wrkflw_store_duration_seconds` histogram. Engine/model untouched.

## Global Constraints
- Module `github.com/kartaladev/wrkflw`; no `pkg/` prefix.
- Strict TDD; RED before GREEN.
- Engine/model production diff ZERO. Changes: new `observability/` root pkg; `internal/persistence/postgres/store.go`; `persistence` façade.
- `workflow-` error prefix where applicable; black-box tests; table-test assert-closure; `t.Context()`.
- Defaults to noop (OTel globals) → existing store/relay tests unchanged.
- Mirror the relay telemetry pattern (`postgres/relay.go` `WithRelay*` + `observability.New` + span/Tracer.Start) and the SDK-recorder test pattern (`relay_observability_test.go`, `runtime/observability_test.go`).
- Gate: `go test -race -p 1 ./...` green; ≥85% on observability + internal/persistence/postgres; lint clean.
- Spec: docs/specs/2026-06-23-observability-gaps-design.md. ADR-0037.

## File Structure
- `observability/handler.go` (**create**) — `NewHandler` + `NewLogger` + the handler impl.
- `observability/handler_test.go` (**create**).
- `internal/persistence/postgres/store.go` (**modify**) — options + telemetry + Load/Commit spans + histogram.
- `internal/persistence/postgres/store_observability_test.go` (**create**).
- `persistence/persistence.go` (**modify**) — re-export the store options (if the façade builds the Store).

---

### Task 1: public observability root pkg (trace-correlating slog.Handler)

**Files:** create `observability/handler.go`, `observability/handler_test.go`.

**Context:** `internal/observability/observability.go` `LogAttrs(ctx)` does the trace-id/span-id extraction: `sc := trace.SpanContextFromContext(ctx); if sc.IsValid() { return []slog.Attr{slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String())} }`. The new PUBLIC package reimplements this in a `slog.Handler`.

**Produces:** `observability.NewHandler(base slog.Handler) slog.Handler`; `observability.NewLogger(base slog.Handler) *slog.Logger`.

**Steps (TDD):**
1. Write `observability/handler_test.go` (black-box `observability_test`): a record-capturing base handler (implement a tiny `slog.Handler` that stores records, or use `slog.NewJSONHandler` into a buffer and parse). Build `lg := slog.New(observability.NewHandler(base))`. Cases: (a) log within a started span (`tp := sdktrace.NewTracerProvider(...); ctx, span := tp.Tracer("t").Start(ctx, "s"); lg.InfoContext(ctx, "msg")`) → the emitted record has `trace_id`==span's TraceID and `span_id`==span's SpanID; (b) log with no span in ctx → no trace_id/span_id attrs; (c) `lg.With("k","v").InfoContext(ctx,...)` (WithAttrs path) still correlates + carries k=v; (d) a group via `WithGroup` delegates. Run RED (NewHandler undefined).
2. Implement `observability/handler.go`: `type handler struct{ base slog.Handler }`; `Handle(ctx, rec)`: if `sc := trace.SpanContextFromContext(ctx); sc.IsValid()` → `rec = rec.Clone(); rec.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))`; `return h.base.Handle(ctx, rec)`. `Enabled` delegates; `WithAttrs(as)` → `&handler{h.base.WithAttrs(as)}`; `WithGroup(n)` → `&handler{h.base.WithGroup(n)}`. `NewHandler`/`NewLogger` constructors with godoc. Imports: `context`, `log/slog`, `go.opentelemetry.io/otel/trace`. Run GREEN. Lint.
3. Commit `feat(observability): public trace-correlating slog.Handler`.

---

### Task 2: Postgres Store Load/Commit spans + histogram

**Files:** modify `internal/persistence/postgres/store.go`; create `internal/persistence/postgres/store_observability_test.go`; modify `persistence/persistence.go` if it constructs the Store with options.

**Context:** `postgres.Store{pool, historyCap, notify}`; `NewStore(pool, opts ...StoreOption)`. `Load(ctx,id)` and `Commit(ctx,expected,step)` are uninstrumented. Mirror `relay.go`: it holds `logOpt/tpOpt/mpOpt observability.Option` + `tel observability.Telemetry`, builds `tel = observability.New("github.com/kartaladev/wrkflw/persistence", filterNilOpts(...))` in `NewRelay`, and uses `r.tel.Tracer.Start(...)`. `observability.New(...).Float64Histogram(name, desc)` creates a histogram. Read relay.go's `WithRelayTracerProvider`/`filterNilOpts` + the relay_observability_test.go SDK-recorder setup.

**Steps (TDD):**
1. Write `store_observability_test.go` (testcontainers `database.RunTestDatabase` + `tracetest.NewSpanRecorder` + `sdkmetric.NewManualReader`, mirror relay_observability_test.go): build `pg.NewStore(pool, pg.WithStoreTracerProvider(tp), pg.WithStoreMeterProvider(mp))`; do `Create`→`Load`→`Commit` (use existing store test helpers/fixtures — check store_test.go for how an AppliedStep is built); assert spans `wrkflw.store.load` + `wrkflw.store.commit` present (SpanRecorder.Ended) and `wrkflw_store_duration_seconds` has ≥1 observation with an `op` attr (load/commit). Also a default `pg.NewStore(pool)` (no opts) Load/Commit → no panic. Run RED (options undefined).
2. Implement: add `logOpt/tpOpt/mpOpt observability.Option`, `tel observability.Telemetry`, `storeDuration metric.Float64Histogram` to Store; `StoreOption`s `WithStoreLogger`/`WithStoreTracerProvider`/`WithStoreMeterProvider` (mirror WithRelay*); in `NewStore` after applying opts, `s.tel = observability.New("github.com/kartaladev/wrkflw/persistence", filterNilOpts(s.logOpt,s.tpOpt,s.mpOpt)...)` (reuse/relocate `filterNilOpts` if it's package-private in relay.go — it's same package `postgres`, so reuse directly) and `s.storeDuration = s.tel.Float64Histogram("wrkflw_store_duration_seconds", "Duration of persistence Store operations in seconds")`. Wrap `Load`: `ctx, span := s.tel.Tracer.Start(ctx, "wrkflw.store.load"); defer span.End(); start := time.Now(); defer func(){ s.storeDuration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(attribute.String("op","load"))) }()`; on error `span.RecordError(err); span.SetStatus(otelcodes.Error, ...)`. Same for `Commit` (`wrkflw.store.commit`, op="commit"). Façade: re-export the 3 options on the persistence package if it builds the Store (check persistence.OpenPostgres/NewStore). Run GREEN. Lint.
3. Commit `feat(persistence/postgres): Store Load/Commit spans + duration histogram`.

---

### Task 3: docs + gate (controller)
ADR-0037 written. Controller verifies, updates HANDOVER + memory, full gate, whole-branch review, merge.

## Verification Checklist
- [ ] `go test -race -p 1 ./...` green; ≥85% observability + internal/persistence/postgres.
- [ ] `golangci-lint run ./...` clean; engine/model diff ZERO.
- [ ] Default (no options) store path unchanged (existing store tests green).
- [ ] Whole-branch review; merge + push; HANDOVER + memory.

## Spec coverage self-check
- §2 public handler → Task 1. §3 store spans+histogram+façade → Task 2. §4 tests → per-task. ✓
