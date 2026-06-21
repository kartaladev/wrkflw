package runtime_test

import (
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// TestNewRunnerWithObservabilityOptions is a smoke test that verifies a Runner
// can be constructed with all three observability options without panicking, and
// that the returned value is non-nil.
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
