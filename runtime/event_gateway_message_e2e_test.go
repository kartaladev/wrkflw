package runtime_test

import (
	"context"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
)

// eventGatewayCorrelatedMsgDef returns a definition whose event-based gateway
// races a correlated MESSAGE arm against a signal arm:
//
//	start → evtgw → msg-catch("payment-confirmed", key=order) → ship(Service)   → end-shipped
//	             → sig-catch("cancelled")                      → cancel(Service) → end-cancelled
//
// The message arm carries a correlation-key expression ("order"), so the resolved
// key depends on the instance variable `order` at arm time.
func eventGatewayCorrelatedMsgDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "evtgw-msg-e2e",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewEventBased("evtgw"),
			event.NewIntermediateCatch("msg-catch", event.WithCatchMessage("payment-confirmed", "order")),
			event.NewIntermediateCatch("sig-catch", event.WithCatchSignal("cancelled")),
			activity.NewServiceTask("ship", activity.WithActionName("ship-order")),
			activity.NewServiceTask("cancel", activity.WithActionName("cancel-order")),
			event.NewEnd("end-shipped"),
			event.NewEnd("end-cancelled"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "evtgw"},
			{ID: "f-gw-msg", Source: "evtgw", Target: "msg-catch"},
			{ID: "f-gw-sig", Source: "evtgw", Target: "sig-catch"},
			{ID: "f-msg-ship", Source: "msg-catch", Target: "ship"},
			{ID: "f-sig-cancel", Source: "sig-catch", Target: "cancel"},
			{ID: "f-ship-end", Source: "ship", Target: "end-shipped"},
			{ID: "f-cancel-end", Source: "cancel", Target: "end-cancelled"},
		},
	}
}

// TestDeliverMessageFiresEventGatewayArm verifies that DeliverMessage reaches a
// correlated MESSAGE arm of an event-based gateway. The arm is tracked as an
// armedEvent (not a token carrying AwaitMessage), so the runtime must register it
// as a message waiter for delivery to correlate to the parked instance. Delivering
// the correlated message must win the gateway race and complete via the ship flow.
func TestDeliverMessageFiresEventGatewayArm(t *testing.T) {
	ctx := t.Context()
	fc := clockwork.NewFakeClock()
	store := runtimetest.MustMemStore(t)

	cat := action.NewMapCatalog(map[string]action.Action{
		"ship-order": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"shipped": true}, nil
		}),
		"cancel-order": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"cancelled": true}, nil
		}),
	})

	r := runtimetest.MustRunner(t, cat, store, runtime.WithClock(fc))
	def := eventGatewayCorrelatedMsgDef()

	st, err := r.Drive(ctx, def, "order-fast", map[string]any{"order": "order-fast"})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, st.Status, "instance must park at the event gateway")
	require.Len(t, st.ArmedEvents, 2, "both gateway arms must be armed")

	// Deliver the correlated message. It must route to the parked instance even
	// though no token carries AwaitMessage == "payment-confirmed" (the event-gateway
	// arm holds it), and the correlation key must match the resolved value.
	err = r.DeliverMessage(ctx, def, "payment-confirmed", "order-fast", map[string]any{"amount": 4200})
	require.NoError(t, err)

	final, _, err := store.Load(ctx, "order-fast")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status,
		"the message arm must win the gateway race and complete via the ship flow")
	require.NotEmpty(t, final.History)
	var reachedShipped bool
	for _, v := range final.History {
		if v.NodeID == "end-shipped" {
			reachedShipped = true
		}
	}
	assert.True(t, reachedShipped, "instance must terminate at end-shipped")
}
