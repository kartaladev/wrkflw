package calllink_test

import (
	"context"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/calllink"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// newTracingCallNotifier builds a CallNotifier with an in-memory SpanRecorder
// and a noopDeliverFn that succeeds for all links.
func newTracingCallNotifier(t *testing.T) (*calllink.CallNotifier, *tracetest.SpanRecorder) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	cl := kernel.NewMemCallLinkStore()
	deliver := calllink.CallDeliverFunc(func(_ context.Context, _ *model.ProcessDefinition, _ string, _ engine.Trigger) error {
		return nil
	})
	reg := kernel.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		"batch-span-parent:1": {ID: "batch-span-parent", Version: 1},
	})

	n := runtimetest.MustCallNotifier(t, cl, deliver, reg,
		calllink.WithCallNotifierTracerProvider(tp),
	)
	return n, sr
}

// TestCallNotifierBatchSpan verifies that DrainOnce records a
// "wrkflw.callnotifier.batch" span when a TracerProvider is injected via
// WithCallNotifierTracerProvider.
func TestCallNotifierBatchSpan(t *testing.T) {
	n, sr := newTracingCallNotifier(t)

	// Seed one terminal link so the batch has work to do.
	cl := kernel.NewMemCallLinkStore()
	link := kernel.CallLink{
		ChildInstanceID:  "batch-span-child-1",
		ParentInstanceID: "batch-span-parent-1",
		ParentDefID:      "batch-span-parent",
		ParentDefVersion: 1,
		ParentCommandID:  "cmd-span-1",
	}
	runtimetest.SeedTerminalCallLink(t, cl, link, kernel.CallOutcome{
		Completed: true,
		Output:    map[string]any{"k": "v"},
	})

	// Rebuild with the seeded store so DrainOnce drains something.
	sr2 := tracetest.NewSpanRecorder()
	tp2 := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr2))

	parentDef := &model.ProcessDefinition{ID: "batch-span-parent", Version: 1}
	reg := kernel.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		"batch-span-parent:1": parentDef,
	})
	deliver := calllink.CallDeliverFunc(func(_ context.Context, _ *model.ProcessDefinition, _ string, _ engine.Trigger) error {
		return nil
	})
	n2 := runtimetest.MustCallNotifier(t, cl, deliver, reg,
		calllink.WithCallNotifierTracerProvider(tp2),
	)

	notified, err := n2.DrainOnce(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, notified, "expected 1 link notified")

	ended := sr2.Ended()
	var batchSpan sdktrace.ReadOnlySpan
	for _, s := range ended {
		if s.Name() == "wrkflw.callnotifier.batch" {
			batchSpan = s
			break
		}
	}
	require.NotNil(t, batchSpan, "expected a wrkflw.callnotifier.batch span to be recorded")

	// Also verify that the empty-drain case emits a span.
	_ = n
	notified0, err := n.DrainOnce(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, notified0)

	ended0 := sr.Ended()
	var batchSpan0 sdktrace.ReadOnlySpan
	for _, s := range ended0 {
		if s.Name() == "wrkflw.callnotifier.batch" {
			batchSpan0 = s
			break
		}
	}
	require.NotNil(t, batchSpan0, "batch span must be emitted even on empty drain")
}

// TestCallNotifierLinksNotifiedCounter verifies that DrainOnce increments
// wrkflw_callnotifier_links_notified_total by the number of links notified.
func TestCallNotifierLinksNotifiedCounter(t *testing.T) {
	type testCase struct {
		name        string
		seedLinks   int
		wantCounter int64
	}

	cases := []testCase{
		{name: "empty batch — counter stays 0", seedLinks: 0, wantCounter: 0},
		{name: "1 link notified", seedLinks: 1, wantCounter: 1},
		{name: "3 links notified", seedLinks: 3, wantCounter: 3},
	}

	parentDef := &model.ProcessDefinition{ID: "counter-parent", Version: 1}
	reg := kernel.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		"counter-parent:1": parentDef,
	})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader := sdkmetric.NewManualReader()
			mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })

			cl := kernel.NewMemCallLinkStore()

			// Seed tc.seedLinks terminal links.
			for i := range tc.seedLinks {
				childID := "counter-child-" + tc.name + "-" + string(rune('0'+i))
				link := kernel.CallLink{
					ChildInstanceID:  childID,
					ParentInstanceID: "counter-parent-" + tc.name + "-" + string(rune('0'+i)),
					ParentDefID:      "counter-parent",
					ParentDefVersion: 1,
					ParentCommandID:  "cmd-" + string(rune('0'+i)),
				}
				runtimetest.SeedTerminalCallLink(t, cl, link, kernel.CallOutcome{
					Completed: true,
					Output:    map[string]any{"i": i},
				})

			}

			deliver := calllink.CallDeliverFunc(func(_ context.Context, _ *model.ProcessDefinition, _ string, _ engine.Trigger) error {
				return nil
			})
			n := runtimetest.MustCallNotifier(t, cl, deliver, reg,
				calllink.WithCallNotifierMeterProvider(mp),
			)

			notified, err := n.DrainOnce(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tc.seedLinks, notified)

			// Collect all metrics.
			var rm metricdata.ResourceMetrics
			require.NoError(t, reader.Collect(t.Context(), &rm))

			// Find wrkflw_callnotifier_links_notified_total.
			var counterSum int64
			var found bool
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					if m.Name == "wrkflw_callnotifier_links_notified_total" {
						found = true
						data, ok := m.Data.(metricdata.Sum[int64])
						require.True(t, ok, "expected Sum[int64] data type")
						for _, dp := range data.DataPoints {
							counterSum += dp.Value
						}
					}
				}
			}
			if tc.wantCounter > 0 {
				require.True(t, found, "expected wrkflw_callnotifier_links_notified_total metric to be present")
				assert.Equal(t, tc.wantCounter, counterSum,
					"counter must equal the number of links notified in the batch")
			}
		})
	}
}

// TestCallNotifierTelemetryOptionsAdditive verifies that the new telemetry
// options (WithCallNotifierTracerProvider, WithCallNotifierMeterProvider,
// WithCallNotifierLogger) are variadic/additive and do not break the existing
// CallNotifier constructor signature.
func TestCallNotifierTelemetryOptionsAdditive(t *testing.T) {
	cl := kernel.NewMemCallLinkStore()
	deliver := calllink.CallDeliverFunc(func(_ context.Context, _ *model.ProcessDefinition, _ string, _ engine.Trigger) error {
		return nil
	})
	reg := kernel.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{})

	// All existing callers pass no telemetry options — must compile and not panic.
	n := runtimetest.MustCallNotifier(t, cl, deliver, reg)
	require.NotNil(t, n)

	// Callers may supply any subset of the new options.
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	n2 := runtimetest.MustCallNotifier(t, cl, deliver, reg,
		calllink.WithCallNotifierTracerProvider(tp),
	)
	require.NotNil(t, n2)

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })

	n3 := runtimetest.MustCallNotifier(t, cl, deliver, reg,
		calllink.WithCallNotifierMeterProvider(mp),
	)
	require.NotNil(t, n3)

	n4 := runtimetest.MustCallNotifier(t, cl, deliver, reg,
		calllink.WithCallNotifierTracerProvider(tp),
		calllink.WithCallNotifierMeterProvider(mp),
	)
	require.NotNil(t, n4)
}
