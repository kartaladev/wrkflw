package processtest_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/processtest"
)

// TestReview_BusRoutesPerDefinition covers finding #1: with the signal bus and
// two instances of DIFFERENT definitions parked on the same signal, a single
// Publish must resume each instance against ITS OWN definition. defB has an extra
// service task that defA lacks; if instance A were resumed with defB, the "noop"
// action would run for A too (Count == 2 instead of 1).
func TestReview_BusRoutesPerDefinition(t *testing.T) {
	t.Parallel()

	defA, err := definition.NewBuilder("defA", 1).
		Add(event.NewStart("start")).
		Add(event.NewIntermediateCatch("await", event.WithSignalName("go"))).
		Add(event.NewEnd("end")).
		Connect("start", "await").Connect("await", "end").
		Build()
	require.NoError(t, err)

	defB, err := definition.NewBuilder("defB", 1).
		Add(event.NewStart("start")).
		Add(event.NewIntermediateCatch("await", event.WithSignalName("go"))).
		Add(activity.NewServiceTask("run", activity.WithTaskAction("noop"))).
		Add(event.NewEnd("end")).
		Connect("start", "await").Connect("await", "run").Connect("run", "end").
		Build()
	require.NoError(t, err)

	h, err := processtest.New(
		processtest.WithSignalBus(),
		processtest.WithAction("noop", action.ActionFunc(func(_ context.Context, m map[string]any) (map[string]any, error) { return nil, nil })),
	)
	require.NoError(t, err)

	_, err = h.Start(t.Context(), defA, "A", nil)
	require.NoError(t, err)
	_, err = h.Start(t.Context(), defB, "B", nil)
	require.NoError(t, err)

	require.NoError(t, h.Bus().Publish(t.Context(), "go", nil))

	finalA, _, err := h.Store().Load(t.Context(), "A")
	require.NoError(t, err)
	finalB, _, err := h.Store().Load(t.Context(), "B")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, finalA.Status)
	assert.Equal(t, engine.StatusCompleted, finalB.Status)
	assert.Equal(t, 1, h.Catalog().Count("noop"), "noop must run only for defB, not for defA")
}

// TestReview_BusSignalUsesFakeClock covers finding #3 (bus path): a signal
// delivered via the bus must be stamped with the shared FakeClock, so a timer
// armed after the signal fires at a deterministic instant.
func TestReview_BusSignalUsesFakeClock(t *testing.T) {
	t.Parallel()

	def, err := definition.NewBuilder("sigtimer", 1).
		Add(event.NewStart("start")).
		Add(event.NewIntermediateCatch("await", event.WithSignalName("go"))).
		Add(event.NewIntermediateCatch("wait", event.WithCatchTimer(schedule.AfterExpr(`"1h"`)))).
		Add(event.NewEnd("end")).
		Connect("start", "await").Connect("await", "wait").Connect("wait", "end").
		Build()
	require.NoError(t, err)

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h, err := processtest.New(processtest.WithSignalBus(), processtest.WithClockStart(start))
	require.NoError(t, err)

	_, err = h.Start(t.Context(), def, "i", nil)
	require.NoError(t, err)
	require.NoError(t, h.Bus().Publish(t.Context(), "go", nil))

	// After the bus signal, drive the timer to completion.
	final, err := h.DriveToCompletion(t.Context(), def, "i", processtest.AutoTimers())
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, final.Status)

	// The timer was armed at signal-time (fake clock) + 1h, so the clock lands
	// exactly one hour past start — not near wall-clock time.
	assert.Equal(t, start.Add(time.Hour), h.Clock().Now(),
		"bus signal must be stamped with the fake clock so the timer fires deterministically")
}

// TestReview_PublishBeforeStartNoPanic covers finding #9: publishing on the bus
// before any instance is started must not panic on a nil definition.
func TestReview_PublishBeforeStartNoPanic(t *testing.T) {
	t.Parallel()

	h, err := processtest.New(processtest.WithSignalBus())
	require.NoError(t, err)

	assert.NotPanics(t, func() {
		_ = h.Bus().Publish(t.Context(), "go", nil)
	})
}
