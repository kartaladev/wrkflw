package runtime_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// collect gathers a ResourceMetrics snapshot from a ManualReader.
func collect(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	return rm
}

// counterValue sums all Int64Sum data points whose attributes contain all
// entries in filter (nil filter matches any). Returns the summed value, or 0
// if the metric is not found.
func counterValue(rm metricdata.ResourceMetrics, name string, filter map[string]string) int64 {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				return 0
			}
			var total int64
			for _, dp := range sum.DataPoints {
				if dpAttributesMatch(dp.Attributes, filter) {
					total += dp.Value
				}
			}
			return total
		}
	}
	return 0
}

// dpAttributesMatch reports whether all key/value pairs in filter are present
// in the attribute.Set attrs.
func dpAttributesMatch(attrs attribute.Set, filter map[string]string) bool {
	for k, want := range filter {
		v, ok := attrs.Value(attribute.Key(k))
		if !ok || v.AsString() != want {
			return false
		}
	}
	return true
}

// histogramCount returns the total number of observations recorded for a
// Float64Histogram instrument (sum of all data-point Counts).
func histogramCount(rm metricdata.ResourceMetrics, name string) uint64 {
	return histogramCountFiltered(rm, name, nil)
}

// histogramCountFiltered returns the total Count across data-points whose
// attributes contain all key/value pairs in filter (nil filter matches any).
func histogramCountFiltered(rm metricdata.ResourceMetrics, name string, filter map[string]string) uint64 {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				return 0
			}
			var total uint64
			for _, dp := range hist.DataPoints {
				if dpAttributesMatch(dp.Attributes, filter) {
					total += dp.Count
				}
			}
			return total
		}
	}
	return 0
}

// TestNewRunnerWithObservabilityOptions verifies a Runner can be constructed
// with each of the three observability options without panicking.
func TestNewRunnerWithObservabilityOptions(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		opt    runtime.Option
		assert func(t *testing.T, r *runtime.Runner)
	}

	cases := []testCase{
		{
			name: "with logger",
			opt:  runtime.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
			assert: func(t *testing.T, r *runtime.Runner) {
				require.NotNil(t, r)
			},
		},
		{
			name: "with tracer provider",
			opt:  runtime.WithTracerProvider(sdktrace.NewTracerProvider()),
			assert: func(t *testing.T, r *runtime.Runner) {
				require.NotNil(t, r)
			},
		},
		{
			name: "with meter provider",
			opt:  runtime.WithMeterProvider(sdkmetric.NewMeterProvider()),
			assert: func(t *testing.T, r *runtime.Runner) {
				require.NotNil(t, r)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := runtime.NewRunner(
				action.NewMapCatalog(nil),
				clock.System(),
				runtime.NewMemStore(),
				tc.opt,
			)
			tc.assert(t, r)
		})
	}
}

// TestStepSpanAndLifecycleMetrics verifies that running a linear process to
// completion via [runtime.Runner.Run] produces:
//   - at least one "wrkflw.step" span in the OTel trace,
//   - wrkflw_instances_started_total == 1,
//   - wrkflw_instances_completed_total{status="completed"} == 1,
//   - at least one observation in wrkflw_step_duration_seconds.
func TestStepSpanAndLifecycleMetrics(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"greet": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"greeted": true}, nil
		}),
	})

	r := runtime.NewRunner(
		cat,
		clock.System(),
		runtime.NewMemStore(),
		runtime.WithTracerProvider(tp),
		runtime.WithMeterProvider(mp),
	)

	// linearDef() is defined in example_test.go: start → greet (service) → end.
	_, err := r.Run(t.Context(), linearDef(), "i1", map[string]any{"name": "world"})
	require.NoError(t, err, "run must succeed for linear process")

	// Assert at least one wrkflw.step span was recorded.
	var sawStep bool
	for _, s := range sr.Ended() {
		if s.Name() == "wrkflw.step" {
			sawStep = true
		}
	}
	if !sawStep {
		t.Fatal("expected at least one 'wrkflw.step' span but none were recorded")
	}

	// Assert metric counters.
	rm := collect(t, reader)
	if v := counterValue(rm, "wrkflw_instances_started_total", nil); v != 1 {
		t.Fatalf("wrkflw_instances_started_total = %d, want 1", v)
	}
	if v := counterValue(rm, "wrkflw_instances_completed_total", map[string]string{"status": "completed"}); v != 1 {
		t.Fatalf("wrkflw_instances_completed_total{status=completed} = %d, want 1", v)
	}

	// Assert the step-duration histogram received at least one observation.
	if c := histogramCount(rm, "wrkflw_step_duration_seconds"); c == 0 {
		t.Fatal("wrkflw_step_duration_seconds has 0 observations, want ≥1")
	}
}

// TestActionSpanAndDurationMetric verifies that running a linear
// start→service-task→end process produces:
//   - spans "wrkflw.runner.Run", "wrkflw.step", and "wrkflw.action charge",
//   - one observation in wrkflw_action_duration_seconds{action=charge,outcome=ok}.
func TestActionSpanAndDurationMetric(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"charge": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"charged": true}, nil
		}),
	})

	chargeDef := &model.ProcessDefinition{
		ID: "payment", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "charge", Kind: model.KindServiceTask, Action: "charge"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "charge"},
			{ID: "f2", Source: "charge", Target: "end"},
		},
	}

	r := runtime.NewRunner(cat, clock.System(), runtime.NewMemStore(),
		runtime.WithTracerProvider(tp), runtime.WithMeterProvider(mp))
	if _, err := r.Run(t.Context(), chargeDef, "i1", map[string]any{}); err != nil {
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
	if c := histogramCountFiltered(rm, "wrkflw_action_duration_seconds", map[string]string{"action": "charge", "outcome": "ok"}); c != 1 {
		t.Fatalf("wrkflw_action_duration_seconds{action=charge,outcome=ok} count = %d, want 1", c)
	}
}
