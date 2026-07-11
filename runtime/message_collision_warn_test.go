package runtime

// message_collision_warn_test.go — white-box (package runtime) test that
// syncMsgWaiters emits a WARN when two RUNNING instances park awaiting the SAME
// (message, correlationKey), surfacing the 1:1-invariant violation (ADR-0125).
// Delivery stays point-to-point: exactly one instance receives the message.

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

func TestAmbiguousMessageCorrelationWarns(t *testing.T) {
	ctx := t.Context()

	// start → await[DeliveryConfirmed, correlation orderId] → end.
	def, err := definition.NewBuilder("order", 1).
		Add(event.NewStart("start")).
		Add(activity.NewReceiveTask("await", "DeliveryConfirmed", activity.WithCorrelationKey("orderId"))).
		Add(event.NewEnd("end")).
		Connect("start", "await").
		Connect("await", "end").
		Build()
	require.NoError(t, err)

	reg := kernel.NewMemDefinitionRegistry()
	require.NoError(t, reg.Register(def))
	store, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	driver, err := NewProcessDriver(
		WithInstanceStore(store),
		WithDefinitions(reg),
		WithLogger(logger),
	)
	require.NoError(t, err)

	// Two instances BOTH correlate on the SAME key value "dup-key": both park
	// awaiting ("DeliveryConfirmed", "dup-key") — an ambiguous 1:1 correlation.
	first, err := driver.Drive(ctx, def, "inst-1", map[string]any{"orderId": "dup-key"})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, first.Status)

	second, err := driver.Drive(ctx, def, "inst-2", map[string]any{"orderId": "dup-key"})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, second.Status)

	// The second park re-registers a key already owned by inst-1 → WARN naming both.
	logged := buf.String()
	assert.Contains(t, logged, "ambiguous message correlation",
		"a cross-instance correlation collision must be WARN-logged")
	assert.Contains(t, logged, "inst-1", "WARN must name the incumbent instance")
	assert.Contains(t, logged, "inst-2", "WARN must name the joining instance")
	assert.Contains(t, logged, "DeliveryConfirmed", "WARN must name the message")
	assert.Contains(t, logged, "dup-key", "WARN must name the correlation key")

	// Delivery is unchanged (point-to-point): exactly one instance receives it.
	require.NoError(t, driver.DeliverMessage(ctx, "DeliveryConfirmed", "dup-key",
		map[string]any{"orderId": "dup-key"}))

	st1, _, err := store.Load(ctx, "inst-1")
	require.NoError(t, err)
	st2, _, err := store.Load(ctx, "inst-2")
	require.NoError(t, err)

	completed := 0
	for _, s := range []engine.Status{st1.Status, st2.Status} {
		if s == engine.StatusCompleted {
			completed++
		}
	}
	assert.Equal(t, 1, completed, "delivery is 1:1 — exactly one instance completes")
}
