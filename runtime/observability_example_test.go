package runtime_test

import (
	"context"
	"log/slog"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// ExampleProcessDriver_observability shows how a library consumer wires an SDK
// TracerProvider, MeterProvider, and slog logger into a [runtime.ProcessDriver].
//
// The pattern:
//   - Build SDK providers (real or noop) and a *slog.Logger.
//   - Pass them via [runtime.WithTracerProvider], [runtime.WithMeterProvider],
//     and [runtime.WithLogger].
//   - The runner emits spans and metrics automatically around each engine step
//     and service-action invocation; the consumer's backend receives them.
//
// When any With* option is omitted the runner defaults to the OTel global
// provider (or noop if no global is set) and slog.Default() — so observability
// is purely additive: processes that do not need it incur only noop overhead.
func ExampleProcessDriver_observability() {
	// Build a minimal SDK TracerProvider (discards spans in this example).
	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(context.Background()) }()

	// Build a ManualReader-backed MeterProvider so the consumer can collect
	// snapshots; any real exporter (OTLP, Prometheus) plugs in here instead.
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer func() { _ = mp.Shutdown(context.Background()) }()

	// Simple start → service-task → end definition.
	def := &model.ProcessDefinition{
		ID:      "demo",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("greet", activity.WithActionName("greet")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "greet"},
			{ID: "f2", Source: "greet", Target: "end"},
		},
	}

	cat := action.NewMapCatalog(map[string]action.Action{
		"greet": action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
			return map[string]any{"greeting": "hello"}, nil
		}),
	})

	mem, err := kernel.NewMemInstanceStore()
	if err != nil {
		panic(err)
	}
	r, err := runtime.NewProcessDriver(
		runtime.WithActionCatalog(cat),
		runtime.WithInstanceStore(mem),
		runtime.WithTracerProvider(tp),
		runtime.WithMeterProvider(mp),
		runtime.WithLogger(slog.Default()),
	)
	if err != nil {
		panic(err)
	}

	defer func() { _ = r.Shutdown(context.Background()) }()

	_, _ = r.Run(context.Background(), def, "demo-1", map[string]any{})
	// Output:
}
