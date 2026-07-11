package runtime

// terminal_waiter_test.go — white-box (package runtime) test that a terminal
// instance holds no correlation waiter, even when a repeatable root event-sub arm
// is still armed in its snapshot (ADR-0124). Uses the unexported findMessageWaiter
// to assert directly on the msgWaiters table.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

func TestCompletedInstanceHoldsNoEventSubWaiter(t *testing.T) {
	ctx := t.Context()

	// Event-sub inner def: non-interrupting, message-triggered on "cancel".
	inner, err := definition.NewBuilder("handle-cancel", 1).
		Add(event.NewStart("onCancel",
			event.WithMessageCorrelator("cancel", "orderId"),
			event.WithNonInterrupting())).
		Add(activity.NewServiceTask("notify-cancel", activity.WithTaskAction("noop"))).
		Add(event.NewEnd("inner-end")).
		Connect("onCancel", "notify-cancel").
		Connect("notify-cancel", "inner-end").
		Build()
	require.NoError(t, err)

	// Main def: start → await[DeliveryConfirmed] → end, plus the root event-sub.
	def, err := definition.NewBuilder("order", 1).
		Add(event.NewStart("start")).
		Add(activity.NewReceiveTask("await", "DeliveryConfirmed", activity.WithCorrelationKey("orderId"))).
		Add(event.NewEnd("end")).
		AddSubProcess("handleCancel", inner).
		Connect("start", "await").
		Connect("await", "end").
		Build()
	require.NoError(t, err)

	cat := action.NewCatalog(map[string]action.Action{
		"noop": action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
			return in, nil
		}),
	})
	store, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	reg := kernel.NewMemDefinitionRegistry()
	require.NoError(t, reg.Register(def))
	driver, err := NewProcessDriver(WithActionCatalog(cat), WithInstanceStore(store), WithDefinitions(reg))
	require.NoError(t, err)

	// Drive: main path parks on await; the "cancel" event-sub arms.
	parked, err := driver.Drive(ctx, def, "order-1", map[string]any{"orderId": "order-1"})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status)
	require.Len(t, parked.EventTriggeredSubprocesses, 1, "cancel event-sub armed")
	if _, ok := driver.findMessageWaiter("cancel", "order-1"); !ok {
		t.Fatal("while running, the cancel event-sub message waiter must be registered")
	}

	// Fire "cancel" TWICE — repeatable arm survives each delivery (ADR-0124) and
	// the runtime keeps correlating the second delivery to the still-armed arm.
	require.NoError(t, driver.DeliverMessage(ctx, "cancel", "order-1", map[string]any{"orderId": "order-1"}))
	require.NoError(t, driver.DeliverMessage(ctx, "cancel", "order-1", map[string]any{"orderId": "order-1"}))
	afterFire, _, err := store.Load(ctx, "order-1")
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, afterFire.Status)
	require.Len(t, afterFire.EventTriggeredSubprocesses, 1, "repeatable cancel arm still armed after repeated firing")

	// Complete the instance WITHOUT firing cancel again: deliver DeliveryConfirmed.
	require.NoError(t, driver.DeliverMessage(ctx, "DeliveryConfirmed", "order-1", map[string]any{"orderId": "order-1"}))
	final, _, err := store.Load(ctx, "order-1")
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, final.Status, "main path completes")

	// The instance is terminal: it must hold NO waiter, even though its snapshot
	// may still carry the armed (repeatable) root event-sub arm. Otherwise a later
	// "cancel" would misroute to this dead instance.
	_, ok := driver.findMessageWaiter("cancel", "order-1")
	assert.False(t, ok, "a completed instance must hold no event-sub message waiter (ADR-0124)")
}
