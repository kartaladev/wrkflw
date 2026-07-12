package runtime_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
)

// TestDriveRecordsGeneratedInstanceIDOnSpan verifies that when Drive mints an
// instance id (empty instanceID argument), the generated id is recorded on the
// trace span — not the empty string it was called with.
func TestDriveRecordsGeneratedInstanceIDOnSpan(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	cat := action.NewCatalog(map[string]action.Action{
		"greet": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) { return nil, nil }),
	})
	r := runtimetest.MustProcessDriver(t, cat, runtimetest.MustMemStore(t), runtime.WithTracerProvider(tp))

	out, err := r.Drive(t.Context(), linearDef(), "", nil) // empty id → minted by idgen
	require.NoError(t, err)
	require.NotEmpty(t, out.InstanceID)

	var found bool
	for _, sp := range sr.Ended() {
		if sp.Name() != "wrkflw.runner.Run" {
			continue
		}
		found = true
		var got string
		for _, a := range sp.Attributes() {
			if string(a.Key) == "wrkflw.instance_id" {
				got = a.Value.AsString()
			}
		}
		assert.Equal(t, out.InstanceID, got, "span must record the generated instance id")
	}
	require.True(t, found, "expected a wrkflw.runner.Run span")
}
