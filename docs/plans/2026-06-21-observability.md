# Observability (Metrics / Traces / Structured Logging) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the engine expose process metrics, emit OpenTelemetry traces, and log via `slog` across every layer **outside** the pure engine core, fulfilling the project requirement *"This library must be able to expose process metrics, enable traces, using slog golang logger."*

**Architecture:** The **runtime is the observability boundary**. `engine/` and `model/` stay pure (zero OTel imports, enforced by a new guard test). The runtime owns a `logger`/`tracer`/`meter` and instruments *around* the pure `engine.Step` call and the side-effecting `perform`. Transports own request spans with manual W3C propagation (no contrib deps). All wiring uses per-component functional options (`WithLogger`/`WithTracerProvider`/`WithMeterProvider`) mirroring the merged **eventing** adapter, defaulting to `slog.Default()` + the OTel global providers + noop fallback.

**Tech Stack:** Go 1.25, `go.opentelemetry.io/otel` v1.43.0 (API in production, SDK in tests), `log/slog`, OTel SDK test utilities (`tracetest.SpanRecorder`, `metric.NewManualReader`).

## Global Constraints

- **Module path:** `github.com/kartaladev/wrkflw`.
- **Engine purity (Invariant #1):** `engine/` and `model/` import only stdlib (+ `model`/`authz`/`humantask`/`expreval`). **No `go.opentelemetry.io/...` import in `engine/` or `model/`** — enforced by Task 2.
- **`Step` stays pure & deterministic (Invariants #3, #4):** observability is a read-only wrapper around `Step`; never mutate `(state, commands)`; **no new `InstanceState` field** for observability.
- **Wiring parity:** option names/semantics match `eventing.WithLogger`/`WithTracerProvider`/`WithMeterProvider` exactly — nil arg is ignored; default logger `slog.Default()`; default providers `otel.GetTracerProvider()`/`otel.GetMeterProvider()`; metric init never fails construction (noop fallback).
- **Metric labels are low-cardinality** — never the instance id as a label.
- **Tests:** black-box (`package <pkg>_test`); table-driven with an `assert` closure per case (project `table-test` skill, NOT `want`/`wantErr`); `t.Context()`; pair each `foo.go` with `foo_test.go`; reserve `*_example_test.go` for genuine e2e.
- **Lint:** `golangci-lint` is v2 (`.golangci.yml version: "2"`).
- **Verify on completion:** `go test -race ./...` green; coverage ≥ 85% on every touched package; `golangci-lint run ./...` clean. Run the Postgres package with limited container parallelism (`go test -p 1 ./internal/persistence/postgres/...`).
- **Commits:** Conventional Commits scoped to the area; end with `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.

---

### Task 1: Shared `internal/observability.Telemetry` helper

**Files:**
- Create: `internal/observability/observability.go`
- Test: `internal/observability/observability_test.go`

**Interfaces:**
- Consumes: nothing (leaf package).
- Produces:
  - `type Telemetry struct { Logger *slog.Logger; Tracer trace.Tracer; Meter metric.Meter }`
  - `type Option func(*config)`
  - `func WithLogger(l *slog.Logger) Option` / `func WithTracerProvider(tp trace.TracerProvider) Option` / `func WithMeterProvider(mp metric.MeterProvider) Option` (nil ignored)
  - `func New(instrumentationName string, opts ...Option) Telemetry` (defaults: `slog.Default()`, otel globals)
  - `func (t Telemetry) LogAttrs(ctx context.Context) []slog.Attr` — `trace_id`/`span_id` from the active span (nil if none)
  - never-fail instrument constructors: `func (t Telemetry) Int64Counter(name, desc string) metric.Int64Counter`, `func (t Telemetry) Int64UpDownCounter(name, desc string) metric.Int64UpDownCounter`, `func (t Telemetry) Float64Histogram(name, desc string) metric.Float64Histogram`

- [ ] **Step 1: Write the failing test**

```go
package observability_test

import (
	"log/slog"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"github.com/kartaladev/wrkflw/internal/observability"
)

func TestNewDefaults(t *testing.T) {
	tel := observability.New("test/scope")
	if tel.Logger == nil || tel.Tracer == nil || tel.Meter == nil {
		t.Fatalf("New must populate all three signals: %+v", tel)
	}
}

func TestLogAttrs(t *testing.T) {
	cases := []struct {
		name   string
		active bool
		assert func(t *testing.T, attrs []slog.Attr)
	}{
		{"no span", false, func(t *testing.T, attrs []slog.Attr) {
			if len(attrs) != 0 {
				t.Fatalf("want no attrs without a span, got %v", attrs)
			}
		}},
		{"active span", true, func(t *testing.T, attrs []slog.Attr) {
			keys := map[string]bool{}
			for _, a := range attrs {
				keys[a.Key] = true
			}
			if !keys["trace_id"] || !keys["span_id"] {
				t.Fatalf("want trace_id+span_id, got %v", attrs)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tel := observability.New("test/scope",
				observability.WithTracerProvider(sdktrace.NewTracerProvider()))
			ctx := t.Context()
			if tc.active {
				var sp interface{ End() }
				ctx, sp = func() (c any, s interface{ End() }) {
					cc, ss := tel.Tracer.Start(t.Context(), "op")
					return cc, ss
				}()
				_ = ctx
				defer sp.End()
				ctx2, sp2 := tel.Tracer.Start(t.Context(), "op2")
				defer sp2.End()
				tc.assert(t, tel.LogAttrs(ctx2))
				return
			}
			tc.assert(t, tel.LogAttrs(ctx))
		})
	}
}

func TestInstrumentsNeverFail(t *testing.T) {
	tel := observability.New("test/scope")
	if tel.Int64Counter("c", "d") == nil ||
		tel.Int64UpDownCounter("g", "d") == nil ||
		tel.Float64Histogram("h", "d") == nil {
		t.Fatal("instrument constructors must never return nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/observability/...`
Expected: FAIL — `package github.com/kartaladev/wrkflw/internal/observability is not in std` / undefined `observability.New`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package observability bundles slog, OTel tracing, and OTel metrics behind one
// small helper so every outer layer (runtime, transports, internal adapters)
// constructs its signals the same way: defaults to slog.Default() and the OTel
// global providers, with a noop fallback so instrument creation never fails.
//
// The engine core (engine/, model/) MUST NOT import this package — observability
// lives strictly outside the pure stepper.
package observability

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
)

// Telemetry carries a logger plus a scoped tracer and meter.
type Telemetry struct {
	Logger *slog.Logger
	Tracer trace.Tracer
	Meter  metric.Meter

	name string
}

type config struct {
	logger *slog.Logger
	tp     trace.TracerProvider
	mp     metric.MeterProvider
}

// Option configures New.
type Option func(*config)

// WithLogger sets the structured logger (default slog.Default()). nil is ignored.
func WithLogger(l *slog.Logger) Option {
	return func(c *config) {
		if l != nil {
			c.logger = l
		}
	}
}

// WithTracerProvider sets the tracer provider (default: otel global). nil ignored.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(c *config) {
		if tp != nil {
			c.tp = tp
		}
	}
}

// WithMeterProvider sets the meter provider (default: otel global). nil ignored.
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(c *config) {
		if mp != nil {
			c.mp = mp
		}
	}
}

// New builds a Telemetry scoped to instrumentationName (e.g. the importing
// package path). Unset providers fall back to the OTel globals.
func New(instrumentationName string, opts ...Option) Telemetry {
	cfg := config{logger: slog.Default()}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.tp == nil {
		cfg.tp = otel.GetTracerProvider()
	}
	if cfg.mp == nil {
		cfg.mp = otel.GetMeterProvider()
	}
	return Telemetry{
		Logger: cfg.logger,
		Tracer: cfg.tp.Tracer(instrumentationName),
		Meter:  cfg.mp.Meter(instrumentationName),
		name:   instrumentationName,
	}
}

// LogAttrs returns trace_id/span_id attrs for the span active in ctx so logs
// correlate to traces. Returns nil when no valid span is active.
func (t Telemetry) LogAttrs(ctx context.Context) []slog.Attr {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return nil
	}
	return []slog.Attr{
		slog.String("trace_id", sc.TraceID().String()),
		slog.String("span_id", sc.SpanID().String()),
	}
}

// Int64Counter creates a counter, falling back to a noop instrument on error so
// construction never fails.
func (t Telemetry) Int64Counter(name, desc string) metric.Int64Counter {
	c, err := t.Meter.Int64Counter(name, metric.WithDescription(desc))
	if err != nil {
		c, _ = metricnoop.NewMeterProvider().Meter(t.name).Int64Counter(name)
	}
	return c
}

// Int64UpDownCounter creates an up/down counter with a noop fallback.
func (t Telemetry) Int64UpDownCounter(name, desc string) metric.Int64UpDownCounter {
	c, err := t.Meter.Int64UpDownCounter(name, metric.WithDescription(desc))
	if err != nil {
		c, _ = metricnoop.NewMeterProvider().Meter(t.name).Int64UpDownCounter(name)
	}
	return c
}

// Float64Histogram creates a histogram with a noop fallback.
func (t Telemetry) Float64Histogram(name, desc string) metric.Float64Histogram {
	h, err := t.Meter.Float64Histogram(name, metric.WithDescription(desc))
	if err != nil {
		h, _ = metricnoop.NewMeterProvider().Meter(t.name).Float64Histogram(name)
	}
	return h
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/observability/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/observability/
git commit -m "feat(observability): shared Telemetry helper (slog+tracer+meter, noop fallback)"
```

---

### Task 2: Core purity import-guard test

**Files:**
- Create: `engine/purity_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: a regression net. No production symbols.

This guard parses every non-test `.go` file under `engine/` and `model/` and fails if any imports a `go.opentelemetry.io/...` path. It passes immediately (the core is pure today); its value is catching a future violation.

- [ ] **Step 1: Write the test**

```go
package engine_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCorePurityNoOTel asserts the pure core never imports OpenTelemetry.
// Observability lives strictly in the runtime and outer layers (spec §1).
func TestCorePurityNoOTel(t *testing.T) {
	for _, dir := range []string{".", "../model"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, imp := range f.Imports {
				if strings.Contains(imp.Path.Value, "go.opentelemetry.io") {
					t.Errorf("%s imports %s: the pure core must not import OpenTelemetry", path, imp.Path.Value)
				}
			}
		}
	}
}
```

- [ ] **Step 2: Run test to verify it passes (guard is green today)**

Run: `go test -run TestCorePurityNoOTel ./engine/...`
Expected: PASS. (To prove it bites, temporarily add `import _ "go.opentelemetry.io/otel"` to a core file, re-run → FAIL, then revert. Optional sanity check; do not commit the temporary import.)

- [ ] **Step 3: Commit**

```bash
git add engine/purity_test.go
git commit -m "test(engine): guard core against OpenTelemetry imports"
```

---

### Task 3: Runner observability plumbing (options + instrument set)

**Files:**
- Modify: `runtime/runner.go` (struct fields + options + `NewRunner` build)
- Create: `runtime/observability.go` (runner instrument struct + builder)
- Test: `runtime/observability_test.go`

**Interfaces:**
- Consumes: `observability.Telemetry`, `observability.New`, `observability.With*` (Task 1).
- Produces:
  - `func WithLogger(l *slog.Logger) Option`, `func WithTracerProvider(tp trace.TracerProvider) Option`, `func WithMeterProvider(mp metric.MeterProvider) Option` (runtime package)
  - unexported `r.obs *runnerObs` holding `tel observability.Telemetry` + the nine instruments (used by Tasks 4–7)

- [ ] **Step 1: Write the failing test**

```go
package runtime_test

import (
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/clock"
	"github.com/kartaladev/wrkflw/runtime"
)

// Constructing a Runner with observability options must not panic and must
// accept all three signal options (smoke test of the plumbing).
func TestNewRunnerWithObservabilityOptions(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	r := runtime.NewRunner(
		action.NewMapCatalog(nil),
		clock.System(),
		runtime.NewMemStore(),
		runtime.WithTracerProvider(tp),
	)
	if r == nil {
		t.Fatal("NewRunner returned nil")
	}
}
```

> Verify `action.NewMapCatalog` / `runtime.NewMemStore` constructor names against the current source before running; substitute the real constructors if they differ.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestNewRunnerWithObservabilityOptions ./runtime/...`
Expected: FAIL — undefined `runtime.WithTracerProvider`.

- [ ] **Step 3: Write minimal implementation**

Add to `runtime/runner.go` imports: `"go.opentelemetry.io/otel/trace"`, `"go.opentelemetry.io/otel/metric"`, and `"github.com/kartaladev/wrkflw/internal/observability"`. Add a field to `Runner`:

```go
	// obs carries the logger/tracer/meter and the pre-built process instruments.
	// Always non-nil after NewRunner (defaults to noop providers + slog.Default()).
	obs *runnerObs
```

Add the three options:

```go
// WithLogger sets the structured logger (default slog.Default()). nil ignored.
func WithLogger(l *slog.Logger) Option { return func(r *Runner) { r.logOpt = observability.WithLogger(l) } }

// WithTracerProvider sets the tracer provider (default: otel global). nil ignored.
func WithTracerProvider(tp trace.TracerProvider) Option { return func(r *Runner) { r.tpOpt = observability.WithTracerProvider(tp) } }

// WithMeterProvider sets the meter provider (default: otel global). nil ignored.
func WithMeterProvider(mp metric.MeterProvider) Option { return func(r *Runner) { r.mpOpt = observability.WithMeterProvider(mp) } }
```

Add the three staged option fields to `Runner` (`logOpt, tpOpt, mpOpt observability.Option`), and after the option loop in `NewRunner` build `r.obs`:

```go
	r.obs = newRunnerObs(r.logOpt, r.tpOpt, r.mpOpt)
```

Create `runtime/observability.go`:

```go
package runtime

import (
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/kartaladev/wrkflw/internal/observability"
)

const runnerInstrumentationName = "github.com/kartaladev/wrkflw/runtime"

// runnerObs bundles the runner's telemetry and pre-built process instruments.
type runnerObs struct {
	tel observability.Telemetry

	instStarted       metric.Int64Counter
	instCompleted     metric.Int64Counter
	instActive        metric.Int64UpDownCounter
	stepDuration      metric.Float64Histogram
	actionDuration    metric.Float64Histogram
	actionRetries     metric.Int64Counter
	incidentsRaised   metric.Int64Counter
	incidentsResolved metric.Int64Counter
	humanTasks        metric.Int64Counter
}

func newRunnerObs(opts ...observability.Option) *runnerObs {
	// Drop nil options (unset signal options) so observability.New sees only real ones.
	real := opts[:0]
	for _, o := range opts {
		if o != nil {
			real = append(real, o)
		}
	}
	tel := observability.New(runnerInstrumentationName, real...)
	return &runnerObs{
		tel:               tel,
		instStarted:       tel.Int64Counter("wrkflw_instances_started_total", "Process instances started."),
		instCompleted:     tel.Int64Counter("wrkflw_instances_completed_total", "Process instances that reached a terminal state."),
		instActive:        tel.Int64UpDownCounter("wrkflw_instances_active", "Currently live (non-terminal) process instances."),
		stepDuration:      tel.Float64Histogram("wrkflw_step_duration_seconds", "Duration of a single engine.Step call."),
		actionDuration:    tel.Float64Histogram("wrkflw_action_duration_seconds", "Duration of a service-action invocation."),
		actionRetries:     tel.Int64Counter("wrkflw_action_retries_total", "Service-action retries scheduled."),
		incidentsRaised:   tel.Int64Counter("wrkflw_incidents_raised_total", "Incidents raised."),
		incidentsResolved: tel.Int64Counter("wrkflw_incidents_resolved_total", "Incidents resolved."),
		humanTasks:        tel.Int64Counter("wrkflw_human_tasks_total", "Human-task lifecycle transitions."),
	}
}

func (o *runnerObs) tracer() trace.Tracer { return o.tel.Tracer }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestNewRunnerWithObservabilityOptions ./runtime/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/runner.go runtime/observability.go runtime/observability_test.go
git commit -m "feat(runtime): observability options and runner instrument set"
```

---

### Task 4: Step span + step_duration + instance-lifecycle & incident metrics

**Files:**
- Modify: `runtime/runner.go` (`deliverLoop`)
- Test: `runtime/observability_test.go` (extend)

**Interfaces:**
- Consumes: `r.obs` (Task 3), `engine.Step`, `engine.InstanceState{Status, Incidents}`, `engine.StatusCompleted/StatusFailed/StatusTerminated`.
- Produces: spans `wrkflw.step`; metrics `wrkflw_step_duration_seconds{trigger}`, `wrkflw_instances_started_total{def}`, `wrkflw_instances_completed_total{def,status}`, `wrkflw_instances_active`, `wrkflw_incidents_raised_total{def}`.

**Detection rules** (computed in `deliverLoop`, no engine change):
- *started*: the `create == true` iteration (first step of a fresh instance).
- *terminal*: `st.Status` transitions into `StatusCompleted`/`StatusFailed`/`StatusTerminated` (was `StatusRunning`/`StatusCompensating` before this step).
- *incident raised*: `len(st.Incidents)` increased vs. the pre-step count.
- `status` label = `completed`/`failed`/`terminated` (map from the terminal `Status`).
- `def` label = `def.ID` (low cardinality).

- [ ] **Step 1: Write the failing test**

```go
func TestStepSpanAndLifecycleMetrics(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	r := runtime.NewRunner(cat, clk, runtime.NewMemStore(),
		runtime.WithTracerProvider(tp), runtime.WithMeterProvider(mp))

	// def is a minimal start→service-task→end (or start→end) linear definition.
	_, err := r.Run(t.Context(), def, "i1", map[string]any{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// at least one wrkflw.step span recorded
	var sawStep bool
	for _, s := range sr.Ended() {
		if s.Name() == "wrkflw.step" {
			sawStep = true
		}
	}
	if !sawStep {
		t.Fatal("expected a wrkflw.step span")
	}

	// instances_started_total == 1 and instances_completed_total{status=completed} == 1
	rm := collect(t, reader)
	if v := counterValue(rm, "wrkflw_instances_started_total", nil); v != 1 {
		t.Fatalf("started_total=%v want 1", v)
	}
	if v := counterValue(rm, "wrkflw_instances_completed_total", map[string]string{"status": "completed"}); v != 1 {
		t.Fatalf("completed_total{completed}=%v want 1", v)
	}
}
```

> `collect`, `counterValue`, and `histogramCount` are small test helpers (read `sdkmetric.ResourceMetrics` via `reader.Collect`); add them to the test file. `sdkmetric` aliases `go.opentelemetry.io/otel/sdk/metric`. Use the existing process-definition builders/fixtures from the current `runtime` tests for `def`, `cat`, `clk`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestStepSpanAndLifecycleMetrics ./runtime/...`
Expected: FAIL — no `wrkflw.step` span / counters are zero.

- [ ] **Step 3: Write minimal implementation**

In `deliverLoop`, wrap the `engine.Step` call and record metrics. Sketch (graft against the current loop body):

```go
		prevStatus := st.Status
		prevIncidents := len(st.Incidents)

		stepCtx, span := r.obs.tracer().Start(ctx, "wrkflw.step", trace.WithAttributes(
			attribute.String("wrkflw.instance_id", st.InstanceID),
			attribute.String("wrkflw.def_id", def.ID),
			attribute.String("wrkflw.trigger", triggerName(t)),
		))
		start := r.clk.Now()
		res, err := engine.Step(def, st, t, engine.StepOptions{DefaultRetryPolicy: r.defaultRetryPolicy})
		r.obs.stepDuration.Record(stepCtx, r.clk.Now().Sub(start).Seconds(),
			metric.WithAttributes(attribute.String("trigger", triggerName(t))))
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			span.End()
			return st, fmt.Errorf("runtime: step: %w", err)
		}
		st = res.State
		span.SetAttributes(
			attribute.Int("wrkflw.command_count", len(res.Commands)),
			attribute.String("wrkflw.status", statusName(st.Status)),
		)
		span.End()

		if create {
			r.obs.instStarted.Add(ctx, 1, metric.WithAttributes(attribute.String("def", def.ID)))
			r.obs.instActive.Add(ctx, 1)
		}
		if isTerminal(st.Status) && !isTerminal(prevStatus) {
			r.obs.instCompleted.Add(ctx, 1, metric.WithAttributes(
				attribute.String("def", def.ID), attribute.String("status", statusName(st.Status))))
			r.obs.instActive.Add(ctx, -1)
		}
		if len(st.Incidents) > prevIncidents {
			r.obs.incidentsRaised.Add(ctx, 1, metric.WithAttributes(attribute.String("def", def.ID)))
		}
```

Then continue the existing `outboxEventsFor`/`Create`/`Commit`/`perform` body using `res`/`st` as before. Thread `stepCtx` (the span context) into the subsequent `r.perform(stepCtx, …)` so action spans (Task 5) nest under the step. Add helpers in `runtime/observability.go`:

```go
func isTerminal(s engine.Status) bool {
	return s == engine.StatusCompleted || s == engine.StatusFailed || s == engine.StatusTerminated
}

func statusName(s engine.Status) string {
	switch s {
	case engine.StatusCompleted:
		return "completed"
	case engine.StatusFailed:
		return "failed"
	case engine.StatusTerminated:
		return "terminated"
	case engine.StatusCompensating:
		return "compensating"
	default:
		return "running"
	}
}

// triggerName returns a stable, low-cardinality label for a trigger type.
func triggerName(t engine.Trigger) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", t), "engine.")
}
```

Add imports to `runner.go`: `"go.opentelemetry.io/otel/attribute"`, `"go.opentelemetry.io/otel/codes"`, `"go.opentelemetry.io/otel/metric"`, `"go.opentelemetry.io/otel/trace"`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestStepSpanAndLifecycleMetrics ./runtime/...` then `go test ./runtime/...`
Expected: PASS (and no regressions).

- [ ] **Step 5: Commit**

```bash
git add runtime/runner.go runtime/observability.go runtime/observability_test.go
git commit -m "feat(runtime): step span plus lifecycle and incident metrics in deliverLoop"
```

---

### Task 5: Run/Deliver spans + action span + action metrics

**Files:**
- Modify: `runtime/runner.go` (`Run`, `Deliver`, `ResolveIncident`, `perform` InvokeAction + ScheduleTimer)
- Test: `runtime/observability_test.go` (extend)

**Interfaces:**
- Consumes: `r.obs`, `engine.InvokeAction{Name,CommandID,Input}`, `engine.ScheduleTimer{Kind}`, `engine.TimerRetry`, `engine.NewResolveIncident`.
- Produces: spans `wrkflw.runner.Run`, `wrkflw.runner.Deliver`, `wrkflw.action <name>`; metrics `wrkflw_action_duration_seconds{action,outcome}`, `wrkflw_action_retries_total{action}`, `wrkflw_incidents_resolved_total{def}`.

- [ ] **Step 1: Write the failing test**

```go
func TestActionSpanAndDurationMetric(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	// cat resolves an action named "charge" that succeeds.
	r := runtime.NewRunner(cat, clk, runtime.NewMemStore(),
		runtime.WithTracerProvider(tp), runtime.WithMeterProvider(mp))
	if _, err := r.Run(t.Context(), defWithServiceTask, "i1", map[string]any{}); err != nil {
		t.Fatalf("run: %v", err)
	}

	names := map[string]bool{}
	for _, s := range sr.Ended() {
		names[s.Name()] = true
	}
	for _, want := range []string{"wrkflw.runner.Run", "wrkflw.step", "wrkflw.action charge"} {
		if !names[want] {
			t.Fatalf("missing span %q; got %v", want, names)
		}
	}
	rm := collect(t, reader)
	if histogramCount(rm, "wrkflw_action_duration_seconds", map[string]string{"action": "charge", "outcome": "ok"}) != 1 {
		t.Fatal("want one ok action_duration sample for charge")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestActionSpanAndDurationMetric ./runtime/...`
Expected: FAIL — missing `wrkflw.runner.Run` / `wrkflw.action charge`.

- [ ] **Step 3: Write minimal implementation**

`Run` and `Deliver` open a span around `deliverLoop`:

```go
func (r *Runner) Run(ctx context.Context, def *model.ProcessDefinition, instanceID string, vars map[string]any) (engine.InstanceState, error) {
	ctx, span := r.obs.tracer().Start(ctx, "wrkflw.runner.Run", trace.WithAttributes(
		attribute.String("wrkflw.instance_id", instanceID),
		attribute.String("wrkflw.def_id", def.ID),
		attribute.Int("wrkflw.def_version", def.Version),
	))
	defer span.End()
	st := engine.InstanceState{InstanceID: instanceID}
	out, err := r.deliverLoop(ctx, def, st, 0, true, engine.NewStartInstance(r.clk.Now(), vars))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetAttributes(attribute.String("wrkflw.status", statusName(out.Status)))
	}
	return out, err
}
```

> `def.Version`’s field name: confirm against `model.ProcessDefinition` (it may be `Version`); use the real field.

Wrap `Deliver` similarly with span `"wrkflw.runner.Deliver"` (attrs: instance id + `wrkflw.trigger`=`triggerName(trg)`), opened **before** `store.Load` so the load is inside the span.

In `perform`’s `engine.InvokeAction` case, wrap the action call:

```go
	case engine.InvokeAction:
		if r.cat == nil {
			return engine.NewActionFailed(r.clk.Now(), cmd.CommandID, "no action catalog: "+cmd.Name, false), nil
		}
		a, ok := r.cat.Resolve(cmd.Name)
		if !ok {
			return engine.NewActionFailed(r.clk.Now(), cmd.CommandID, "unknown action: "+cmd.Name, false), nil
		}
		actx, span := r.obs.tracer().Start(ctx, "wrkflw.action "+cmd.Name, trace.WithAttributes(
			attribute.String("wrkflw.action", cmd.Name),
		))
		start := r.clk.Now()
		out, err := a.Do(actx, cmd.Input)
		outcome := "ok"
		if err != nil {
			outcome = "error"
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		r.obs.actionDuration.Record(actx, r.clk.Now().Sub(start).Seconds(),
			metric.WithAttributes(attribute.String("action", cmd.Name), attribute.String("outcome", outcome)))
		span.End()
		if err != nil {
			return engine.NewActionFailedJittered(r.clk.Now(), cmd.CommandID, err.Error(), true, r.jitter.Fraction()), nil
		}
		return engine.NewActionCompleted(r.clk.Now(), cmd.CommandID, out), nil
```

In `perform`’s `engine.ScheduleTimer` case, count a retry when `cmd.Kind == engine.TimerRetry`:

```go
		if cmd.Kind == engine.TimerRetry {
			r.obs.actionRetries.Add(ctx, 1, metric.WithAttributes(attribute.String("timer_id", "")))
		}
```

> Prefer an `action`-labelled retry counter; the action name is not on `ScheduleTimer`. Use a low-cardinality label available at this site (e.g. drop the label entirely, or label by `def.ID`). Decide in the test: assert `wrkflw_action_retries_total` total == 1 without a label. Keep labels low-cardinality.

In `ResolveIncident`, after a successful `Deliver`, increment `incidentsResolved`:

```go
func (r *Runner) ResolveIncident(ctx context.Context, def *model.ProcessDefinition, instanceID, incidentID string, addAttempts int) (engine.InstanceState, error) {
	st, err := r.Deliver(ctx, def, instanceID, engine.NewResolveIncident(r.clk.Now(), incidentID, addAttempts))
	if err == nil {
		r.obs.incidentsResolved.Add(ctx, 1, metric.WithAttributes(attribute.String("def", def.ID)))
	}
	return st, err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestActionSpanAndDurationMetric ./runtime/...` then `go test ./runtime/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/runner.go runtime/observability.go runtime/observability_test.go
git commit -m "feat(runtime): Run/Deliver/action spans and action metrics"
```

---

### Task 6: Human-task lifecycle counter

**Files:**
- Modify: `runtime/runner.go` (`perform` AwaitHuman → `created`)
- Modify: `runtime/taskservice.go` (`Claim`/`Reassign`/`Complete` → `claimed`/`reassigned`/`completed`)
- Test: `runtime/observability_test.go` (extend)

**Interfaces:**
- Consumes: `r.obs.humanTasks` (Task 3). `TaskService` must reach the same counter.
- Produces: metric `wrkflw_human_tasks_total{event}` with `event ∈ {created, claimed, reassigned, completed}`.

`TaskService` is constructed separately (`NewTaskService(store, az, clk)`), so give it its own `*runnerObs`-equivalent counter via a new option, OR pass the counter in. Simplest: add an unexported `humanTasks metric.Int64Counter` field to `TaskService` plus a `WithTaskServiceObservability(...)`-style option that mirrors the runner options; default to a noop via `observability.New`. Keep the public surface consistent: add `func WithLogger/WithTracerProvider/WithMeterProvider` variadic options to `NewTaskService` **or** a `taskservice`-local `Option`. Confirm the current `NewTaskService` signature and extend it with `...Option`.

- [ ] **Step 1: Write the failing test**

```go
func TestHumanTaskCounter(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	r := runtime.NewRunner(cat, clk, runtime.NewMemStore(),
		runtime.WithHumanTasks(resolver, tasks, authz.AllowAll{}),
		runtime.WithMeterProvider(mp))

	// defWithUserTask parks at a user task on first Run → one "created".
	if _, err := r.Run(t.Context(), defWithUserTask, "i1", map[string]any{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	rm := collect(t, reader)
	if counterValue(rm, "wrkflw_human_tasks_total", map[string]string{"event": "created"}) != 1 {
		t.Fatal("want one human_tasks_total{created}")
	}
}
```

> Use the current `WithHumanTasks` resolver/tasks fixtures from existing human-task tests. If `authz.AllowAll{}` is not the real symbol, use the real allow-all authorizer.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestHumanTaskCounter ./runtime/...`
Expected: FAIL — counter is zero.

- [ ] **Step 3: Write minimal implementation**

In `perform`’s `engine.AwaitHuman` case, after a successful `r.tasks.Upsert`, add:

```go
		r.obs.humanTasks.Add(ctx, 1, metric.WithAttributes(attribute.String("event", "created")))
```

In `taskservice.go`, build a counter from the new options in `NewTaskService` and increment it in each method after the successful state transition: `claimed` in `Claim`, `reassigned` in `Reassign`, `completed` in `Complete`. Example for `Claim` (graft against the real method body):

```go
	s.humanTasks.Add(ctx, 1, metric.WithAttributes(attribute.String("event", "claimed")))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestHumanTaskCounter ./runtime/...` then `go test ./runtime/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/runner.go runtime/taskservice.go runtime/observability.go runtime/observability_test.go
git commit -m "feat(runtime): human-task lifecycle counter"
```

---

### Task 7: Inject loggers — runner timer-fire path + gocron scheduler

**Files:**
- Modify: `runtime/runner.go` (timer-fire callback: replace two package-global `slog.Error` calls with `r.obs.tel.Logger` + `LogAttrs`)
- Modify: `scheduling/scheduling.go` (façade: add `WithLogger` option, thread to internal)
- Modify: `internal/scheduling/gocron/scheduler.go` (accept injected logger; replace two global `slog.Error` calls)
- Test: `runtime/observability_test.go` and/or `internal/scheduling/gocron/scheduler_test.go` (capturing slog handler)

**Interfaces:**
- Consumes: `r.obs.tel.Logger`, `r.obs.tel.LogAttrs(ctx)`; scheduler gains an injected `*slog.Logger` (default `slog.Default()`).
- Produces: `scheduling.WithLogger(*slog.Logger) Option`; no package-global `slog.*` calls remain in `runtime/runner.go` or `internal/scheduling/gocron/scheduler.go`.

- [ ] **Step 1: Write the failing test (capturing handler)**

```go
// captureHandler records slog records for assertions.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func TestSchedulerUsesInjectedLogger(t *testing.T) {
	h := &captureHandler{}
	logger := slog.New(h)
	// Build the gocron scheduler via the façade with WithLogger(logger); force an
	// error path (e.g. Cancel of an unknown timer logs nothing; Schedule a job whose
	// callback errors). Assert no record was emitted through slog.Default and that
	// when the error path runs, the injected handler captured it.
	_ = logger
	// (Concrete trigger of the error path depends on the gocron impl; assert the
	// injected handler receives the scheduler's error log, not the global default.)
}
```

> This test’s concrete error-trigger depends on the scheduler internals — read `internal/scheduling/gocron/scheduler.go` and drive the real path that currently calls `slog.Error`. The binding assertion: the **injected** handler captures the record (proving global `slog` is no longer used).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestSchedulerUsesInjectedLogger ./internal/scheduling/gocron/...`
Expected: FAIL — scheduler still logs through the global default; injected handler empty.

- [ ] **Step 3: Write minimal implementation**

- In `internal/scheduling/gocron/scheduler.go`: add an unexported `logger *slog.Logger` field (default `slog.Default()` when unset) and an internal `WithLogger` option; replace the two `slog.Error(...)` call sites with `s.logger.Error(...)`.
- In `scheduling/scheduling.go`: add `func WithLogger(l *slog.Logger) Option` that threads to the internal option.
- In `runtime/runner.go` timer-fire callback: replace `slog.Error(...)` with:

```go
				r.obs.tel.Logger.LogAttrs(fireCtx, slog.LevelError, "runtime: timer fire: Deliver failed",
					append(r.obs.tel.LogAttrs(fireCtx),
						slog.String("timer_id", timerID),
						slog.String("instance_id", instanceID),
						slog.Any("error", err))...)
```

and the same for the "permanently dropped" message. (`*slog.Logger.LogAttrs` takes `...slog.Attr`, so spread the correlation attrs in.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/scheduling/gocron/... ./scheduling/... ./runtime/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/runner.go scheduling/ internal/scheduling/gocron/
git commit -m "feat(runtime,scheduling): inject loggers, drop package-global slog calls"
```

---

### Task 8: REST transport — request span + injected logger

**Files:**
- Modify: `transport/rest/handler.go` (options + per-request span middleware + injected logger in `writeJSON`/handlers)
- Modify or Create: `transport/rest/options.go` (add `WithLogger`/`WithTracerProvider`/`WithMeterProvider`)
- Test: `transport/rest/observability_test.go`

**Interfaces:**
- Consumes: `observability.Telemetry`, `otel.GetTextMapPropagator()`, `propagation.HeaderCarrier`.
- Produces: `rest.WithLogger`/`WithTracerProvider`/`WithMeterProvider`; a span `wrkflw.rest <METHOD> <route>` per request with W3C context extracted from request headers; the handler logs through the injected logger (no package-global `slog.Error`).

- [ ] **Step 1: Write the failing test**

```go
func TestRESTRequestSpan(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	h := rest.NewHandler(svc, rest.WithTracerProvider(tp))

	req := httptest.NewRequest(http.MethodPost, "/instances", strings.NewReader(`{"def_ref":"d:1"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var sawSpan bool
	for _, s := range sr.Ended() {
		if strings.HasPrefix(s.Name(), "wrkflw.rest") {
			sawSpan = true
		}
	}
	if !sawSpan {
		t.Fatalf("expected a wrkflw.rest span; got %d spans", len(sr.Ended()))
	}
}
```

> Reuse the existing REST test’s `svc` fixture (a `service.Service` over a Runner+MemStore). Confirm the start-instance request body shape (`def_ref`) against current handler tests.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestRESTRequestSpan ./transport/rest/...`
Expected: FAIL — undefined `rest.WithTracerProvider` / no span.

- [ ] **Step 3: Write minimal implementation**

Add `tel observability.Telemetry` to the handler `config` (built from new options in `NewHandler`). Wrap the returned mux in a tracing middleware:

```go
func (h *handler) traceMiddleware(next http.Handler) http.Handler {
	prop := otel.GetTextMapPropagator()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		route := r.Pattern // Go 1.22+ ServeMux sets the matched pattern
		name := "wrkflw.rest " + r.Method + " " + r.URL.Path
		ctx, span := h.cfg.tel.Tracer.Start(ctx, name, trace.WithAttributes(
			attribute.String("http.method", r.Method),
			attribute.String("http.route", route),
		))
		defer span.End()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
```

Return `h.traceMiddleware(mux)` from `NewHandler`. Replace the package-global `slog.Error("rest: encode response", …)` in `writeJSON` with the injected logger (thread `cfg.tel.Logger`; make `writeJSON` a method or pass the logger). Add `transport/rest/options.go` with the three options mirroring eventing.

> `propagation` = `go.opentelemetry.io/otel/propagation`. If `r.Pattern` is unavailable in the mounted-under-prefix case, fall back to `r.URL.Path` for the route attribute. Keep the span name low-cardinality by preferring the matched pattern when present.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestRESTRequestSpan ./transport/rest/...` then `go test ./transport/rest/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add transport/rest/
git commit -m "feat(transport/rest): per-request span with W3C propagation and injected logger"
```

---

### Task 9: gRPC transport — per-RPC span

**Files:**
- Modify: `transport/grpc/server.go` (`RegisterWorkflowServiceServer` gains variadic options; per-RPC span)
- Create: `transport/grpc/observability.go` + `transport/grpc/options.go`
- Test: `transport/grpc/observability_test.go`

**Interfaces:**
- Consumes: `observability.Telemetry`, `otel.GetTextMapPropagator()`, gRPC `metadata.MD` carrier.
- Produces: `RegisterWorkflowServiceServer(reg, svc, ...Option)` (variadic); per-RPC spans `wrkflw.grpc <Method>` with context extracted from incoming metadata; span error status on failure.

Implementation note: rather than instrument each RPC method by hand, store the `Telemetry` on `server` and add a small unexported helper `withSpan(ctx, method, fn)` each RPC wraps, or implement a `grpc.UnaryServerInterceptor` the consumer can also reuse. Simplest with the existing per-method bodies: a helper the methods call at entry.

- [ ] **Step 1: Write the failing test**

```go
func TestGRPCRequestSpan(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	// Reuse the existing bufconn harness; register with the new option.
	srv := grpc.NewServer()
	grpctransport.RegisterWorkflowServiceServer(srv, svc, grpctransport.WithTracerProvider(tp))
	// ... start bufconn server, dial, call StartInstance ...

	var sawSpan bool
	for _, s := range sr.Ended() {
		if strings.HasPrefix(s.Name(), "wrkflw.grpc") {
			sawSpan = true
		}
	}
	if !sawSpan {
		t.Fatal("expected a wrkflw.grpc span")
	}
}
```

> Reuse the bufconn setup from `transport/grpc/server_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestGRPCRequestSpan ./transport/grpc/...`
Expected: FAIL — undefined `grpctransport.WithTracerProvider` / no span.

- [ ] **Step 3: Write minimal implementation**

Add a `tel observability.Telemetry` field to `server`; build it from variadic options in `RegisterWorkflowServiceServer`. Add `transport/grpc/options.go` (three options) and `transport/grpc/observability.go` with:

```go
func (s *server) startSpan(ctx context.Context, method string) (context.Context, trace.Span) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		ctx = otel.GetTextMapPropagator().Extract(ctx, mdCarrier(md))
	}
	return s.tel.Tracer.Start(ctx, "wrkflw.grpc "+method, trace.WithAttributes(
		attribute.String("rpc.system", "grpc"),
		attribute.String("rpc.method", method),
	))
}
```

`mdCarrier` adapts `metadata.MD` to `propagation.TextMapCarrier` (Get/Set/Keys). At the top of each RPC method:

```go
	ctx, span := s.startSpan(ctx, "StartInstance")
	defer span.End()
```

and on the error return, `span.RecordError(err); span.SetStatus(codes.Error, err.Error())` before `return nil, mapToGRPCStatus(err)`. (`codes` here is `go.opentelemetry.io/otel/codes`; the existing gRPC `codes`/`status` imports are `google.golang.org/grpc/...` — alias to avoid a clash, e.g. `otelcodes`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestGRPCRequestSpan ./transport/grpc/...` then `go test ./transport/grpc/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add transport/grpc/
git commit -m "feat(transport/grpc): per-RPC span with metadata propagation"
```

---

### Task 10: Relay observability — batch span + structured logs

**Files:**
- Modify: `persistence/persistence.go` (façade `NewRelay`: add `WithLogger`/`WithTracerProvider`/`WithMeterProvider`)
- Modify: `internal/persistence/postgres/relay.go` (accept telemetry; span around a publish batch; structured logs through injected logger)
- Test: `internal/persistence/postgres/relay_observability_test.go` (testcontainers Postgres)

**Interfaces:**
- Consumes: `observability.Telemetry`, the existing relay loop.
- Produces: `persistence.WithLogger`/`WithTracerProvider`/`WithMeterProvider`; a span `wrkflw.relay.batch` per drained batch; relay errors logged through the injected logger.

- [ ] **Step 1: Write the failing test**

```go
func TestRelayBatchSpan(t *testing.T) {
	pool := database.RunTestDatabase(t) // existing helper
	// migrate, seed one pending outbox row, build a publisher stub that succeeds
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	relay := persistence.NewRelay(pool, pub, persistence.WithTracerProvider(tp))
	if err := relay.DrainOnce(t.Context()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	var saw bool
	for _, s := range sr.Ended() {
		if s.Name() == "wrkflw.relay.batch" {
			saw = true
		}
	}
	if !saw {
		t.Fatal("expected a wrkflw.relay.batch span")
	}
}
```

> Confirm `persistence.NewRelay`’s real constructor signature and the `DrainOnce` method name against current source; reuse the existing relay test scaffolding for the publisher stub and seeding. Run this package with `-p 1`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -p 1 -run TestRelayBatchSpan ./internal/persistence/postgres/...`
Expected: FAIL — undefined `persistence.WithTracerProvider` / no span.

- [ ] **Step 3: Write minimal implementation**

Thread an `observability.Telemetry` into the relay (built from the new façade options, default noop). Wrap each batch drain:

```go
	ctx, span := r.tel.Tracer.Start(ctx, "wrkflw.relay.batch")
	defer span.End()
	// ... claim + publish rows ...
	span.SetAttributes(attribute.Int("wrkflw.batch_size", n))
```

Replace any relay error logging with `r.tel.Logger.ErrorContext(ctx, …, r.tel.LogAttrs(ctx)…)`. (The DLQ counters belong to the resilience track — do not add metrics here; the publish counter already exists in the eventing adapter.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -p 1 ./internal/persistence/postgres/...` then `go test -p 1 ./persistence/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add persistence/ internal/persistence/postgres/relay.go internal/persistence/postgres/relay_observability_test.go
git commit -m "feat(persistence): relay batch span and structured logging"
```

---

### Task 11: Testable example + ADR-0019 + HANDOVER update

**Files:**
- Create: `runtime/observability_example_test.go` (testable example wiring an SDK provider into a Runner)
- Create: `docs/adr/0019-observability-runtime-boundary.md`
- Modify: `docs/plans/HANDOVER.md` (add the Observability sub-project section + flip the resume point to the next track)

**Interfaces:**
- Consumes: everything above.
- Produces: a runnable `Example…` documenting the wiring; the ADR (Nygard template); the handover record.

- [ ] **Step 1: Write the testable example**

```go
func ExampleRunner_observability() {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	tp := sdktrace.NewTracerProvider()

	r := runtime.NewRunner(cat, clock.System(), runtime.NewMemStore(),
		runtime.WithTracerProvider(tp),
		runtime.WithMeterProvider(mp),
		runtime.WithLogger(slog.Default()),
	)
	_, _ = r.Run(context.Background(), def, "demo", map[string]any{})
	// Output:
}
```

> Make the example compile and produce stable (or empty) output. Use a trivial linear `def`/`cat`. The example is the library-facing documentation of the wiring (CLAUDE.md rule: testable examples for the embedded-engine API).

- [ ] **Step 2: Run the example**

Run: `go test -run ExampleRunner_observability ./runtime/...`
Expected: PASS.

- [ ] **Step 3: Write ADR-0019 (Nygard template)**

Create `docs/adr/0019-observability-runtime-boundary.md` with Status/Date, Context (the project requirement; the purity constraint; why OTel-direct over an in-repo port; why manual transport spans over contrib), Decision (the runtime is the boundary; the span tree; the full metric catalog; slog conventions; per-component options + globals default), Consequences (engine stays pure & guarded; consumer configures via OTel globals or per-component; deferred follow-ups from spec §10).

- [ ] **Step 4: Update HANDOVER.md**

Add an "Observability sub-project — ✅ COMPLETE" section (mirroring the resilience section: what shipped per layer, ADR-0019, the gate numbers) and change the resume point: Observability done; next track = **Performance/caching** (owned-instance lease cache, history cap, LISTEN/NOTIFY).

- [ ] **Step 5: Commit**

```bash
git add runtime/observability_example_test.go docs/adr/0019-observability-runtime-boundary.md docs/plans/HANDOVER.md
git commit -m "docs(observability): ADR-0019, runnable wiring example, HANDOVER update"
```

---

## Final verification (run before requesting the whole-branch review)

- [ ] `go test -race ./...` green (run `./internal/persistence/postgres/...` with `-p 1`).
- [ ] Coverage ≥ 85% on every touched package:
  `go test -race -coverprofile=cover.out ./internal/observability/... ./runtime/... ./transport/rest/... ./transport/grpc/... ./scheduling/... ./internal/scheduling/gocron/... ./persistence/... ./internal/persistence/postgres/... && go tool cover -func=cover.out | tail -1`
- [ ] `golangci-lint run ./...` clean.
- [ ] Purity guard green: `go test -run TestCorePurityNoOTel ./engine/...` — and grep confirms no `go.opentelemetry.io` import under `engine/` or `model/`.
- [ ] No package-global `slog.*` calls remain in `runtime/runner.go`, `internal/scheduling/gocron/scheduler.go`, or `transport/rest/handler.go` (all go through injected loggers).
```
