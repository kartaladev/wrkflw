package runtime_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

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
