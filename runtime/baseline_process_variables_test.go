package runtime_test

// baseline_process_variables_test.go — characterization test for the
// process-variables row "A lenient typed action sees the engine's stamp, not a
// caller's variant" (openspec/specs/process-variables). The strict twin is the
// "the engine's reserved stamp survives an attacker's case-variant" case of
// TestTypedActionDecodeFailureIsNonRetryable; this pins the lenient mode, which
// is the one a service task uses by default.

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
)

func TestBaselineLenientTypedActionSeesTheEngineStamp(t *testing.T) {
	t.Parallel()

	const instanceID = "typed-1"

	clk := clockwork.NewFakeClockAt(time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC))
	store := runtimetest.MustMemStore(t)
	// action.Typed with no options: lenient input decoding.
	cat := action.NewCatalog(map[string]action.Action{"t": action.Typed(typedIdemValueEcho)})
	driver := runtimetest.MustProcessDriver(t, cat, store,
		runtime.WithClock(clk),
		runtime.WithScheduler(&runtimetest.RecordingScheduler{Clock: clk}),
	)

	st, err := driver.Drive(t.Context(), typedTaskDef(), instanceID,
		map[string]any{"_idempotencykey": "SPOOFED-BY-ATTACKER"})
	require.NoError(t, err)

	// The echo action writes the key it RECEIVED to "idem", so this compares the
	// value, not mere absence of the attacker's string.
	assert.Equal(t, instanceID+":task", st.Variables["idem"],
		"the lenient action must receive the engine's stamp, not the caller's case-variant")
	assert.Equal(t, engine.StatusCompleted, st.Status)
	assert.Empty(t, st.Incidents, "the instance completes with no incident")
}
