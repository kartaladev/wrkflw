package runtime_test

// The two tests below both call driver.BroadcastSignal but are kept as standalone
// TestXxx funcs (not a table) because their setup is structurally different: the
// happy path needs a forward-referenced SignalBus plus two instances driven to
// park, while the error path deliberately constructs a bus-less driver.

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/signal"
)

// TestBroadcastSignalResumesParkedInstances verifies that BroadcastSignal — the
// ProcessDriver facade over the owned SignalBus — resumes every instance parked
// on the given signal name, so a consumer never has to reach into the bus itself.
func TestBroadcastSignalResumesParkedInstances(t *testing.T) {
	ctx := t.Context()
	fc := clockwork.NewFakeClockAt(time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC))
	store := runtimetest.MustMemStore(t)
	def := runtimetest.SignalCatchDef("approved")

	// Forward-reference wiring: the bus delivers via driver.ApplyTrigger, and the driver
	// owns the bus. This is the same graph a consumer builds once at startup.
	var driver *runtime.ProcessDriver
	bus := runtimetest.MustSignalBus(t, func(bCtx context.Context, instanceID string, trg engine.Trigger) error {
		_, err := driver.ApplyTrigger(bCtx, def, instanceID, trg)
		return err
	}, signal.WithClock(fc))
	driver = runtimetest.MustRunner(t, action.NewCatalog(nil), store,
		runtime.WithClock(fc), runtime.WithSignalBus(bus))

	for _, id := range []string{"inst-1", "inst-2"} {
		parked, err := driver.Drive(ctx, def, id, nil)
		require.NoError(t, err)
		require.Equal(t, engine.StatusRunning, parked.Status, "%s must park", id)
	}

	// Broadcast through the driver facade — no direct bus.Publish call.
	err := driver.BroadcastSignal(ctx, "approved", map[string]any{"decision": "yes"})
	require.NoError(t, err)

	for _, id := range []string{"inst-1", "inst-2"} {
		final, _, err := store.Load(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, engine.StatusCompleted, final.Status, "%s must complete after broadcast", id)
	}
}

// TestBroadcastSignalWithoutBusErrors verifies that BroadcastSignal returns a
// descriptive error (mentioning the SignalBus) when no bus is configured, rather
// than silently dropping the signal.
func TestBroadcastSignalWithoutBusErrors(t *testing.T) {
	driver := runtimetest.MustRunner(t, nil, runtimetest.MustMemStore(t),
		runtime.WithClock(clockwork.NewFakeClock()))
	// WithSignalBus intentionally omitted.

	err := driver.BroadcastSignal(t.Context(), "approved", nil)
	require.Error(t, err, "BroadcastSignal must fail when no SignalBus is configured")
	assert.Contains(t, err.Error(), "SignalBus", "error must mention the missing SignalBus")
}
