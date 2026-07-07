package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/processtest"
	"github.com/zakyalvan/krtlwrkflw/runtime/signal"
)

// messageCatchDef returns: start → message-catch(name, correlationKey="orderId") → end.
func messageCatchDef(msgName string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "message-catch-" + msgName,
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewCatch("wait-msg", event.WithCatchMessage(msgName, "orderId")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait-msg"},
			{ID: "f2", Source: "wait-msg", Target: "end"},
		},
	}
}

// eventGatewayDef returns a process with an event-based gateway racing a 1h timer vs a
// signal "approved":
//
//	start → event-gateway → timer-catch(1h) → timer-end
//	                      → signal-catch("approved") → signal-end
func eventGatewayDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "event-gateway-race",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewEventBased("gw"),
			event.NewCatch("timer-arm", event.WithCatchTimer(schedule.AfterExpr(`"1h"`))),
			event.NewCatch("signal-arm", event.WithCatchSignal("approved")),
			event.NewEnd("timer-end"),
			event.NewEnd("signal-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "gw"},
			{ID: "f2", Source: "gw", Target: "timer-arm"},
			{ID: "f3", Source: "gw", Target: "signal-arm"},
			{ID: "f4", Source: "timer-arm", Target: "timer-end"},
			{ID: "f5", Source: "signal-arm", Target: "signal-end"},
		},
	}
}

// TestSignalBroadcastResumesTwoInstances verifies that a single Publish on the
// SignalBus resumes ALL instances that are currently awaiting that signal.
func TestSignalBroadcastResumesTwoInstances(t *testing.T) {
	ctx := t.Context()

	startAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	store := runtimetest.MustMemStore(t)

	def := runtimetest.SignalCatchDef("approved")

	// Use a forward-reference (pointer-forward) pattern so the same runner (with
	// its signal bus) handles deliveries. This ensures subscriptions/msgWaiters
	// are always in sync — not a separate ephemeral runner.
	var r *runtime.ProcessDriver
	bus := runtimetest.MustSignalBus(t, func(bCtx context.Context, instanceID string, trg engine.Trigger) error {
		_, err := r.Deliver(bCtx, def, instanceID, trg)
		return err
	}, signal.WithClock(fc))

	r = runtimetest.MustRunner(t, action.NewMapCatalog(nil), store, runtime.WithClock(fc), runtime.WithSignalBus(bus))

	// Start two instances; both park at the signal-catch node.
	parked1, err := r.Run(ctx, def, "inst-1", nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, parked1.Status, "inst-1 must park")

	parked2, err := r.Run(ctx, def, "inst-2", nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, parked2.Status, "inst-2 must park")

	// Both should be parked at "wait-signal" with AwaitSignal="approved".
	require.Len(t, parked1.Tokens, 1)
	assert.Equal(t, "approved", parked1.Tokens[0].AwaitSignal)
	require.Len(t, parked2.Tokens, 1)
	assert.Equal(t, "approved", parked2.Tokens[0].AwaitSignal)

	// Publish "approved" once — both instances should be advanced to completion.
	err = bus.Publish(ctx, "approved", map[string]any{"decision": "yes"})
	require.NoError(t, err)

	final1, _, err := store.Load(ctx, "inst-1")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final1.Status, "inst-1 must complete")

	final2, _, err := store.Load(ctx, "inst-2")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final2.Status, "inst-2 must complete")
}

// TestRunnerThrowSignalWithoutBusErrors verifies that a Runner without a SignalBus
// returns a descriptive error when it encounters a ThrowSignal command.
func TestRunnerThrowSignalWithoutBusErrors(t *testing.T) {
	// Process: start → throw("approved") → end.
	// A throw event emits ThrowSignal; without a bus the runner must fail.
	def := &model.ProcessDefinition{
		ID:      "throw-only",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewThrow("throw", event.WithThrowSignal("approved")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "throw"},
			{ID: "f2", Source: "throw", Target: "end"},
		},
	}

	r := runtimetest.MustRunner(t, nil, runtimetest.MustMemStore(t), runtime.WithClock(clockwork.NewFakeClock()))
	// WithSignalBus intentionally omitted.

	_, err := r.Run(t.Context(), def, "i1", nil)
	require.Error(t, err, "Run must fail with a descriptive error when no SignalBus is configured")
	assert.Contains(t, err.Error(), "SignalBus", "error must mention the missing SignalBus")
}

// TestEventGatewayTimerWinsUnderFakeClock verifies that when the fake clock is
// advanced past the timer arm's FireAt, the timer branch completes and the signal
// arm is cancelled (late signal deliver is a no-op).
func TestEventGatewayTimerWinsUnderFakeClock(t *testing.T) {
	ctx := t.Context()
	startAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	store := runtimetest.MustMemStore(t)
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	def := eventGatewayDef()

	// bus is wired with a deliver that uses r.Deliver; we break the circular
	// dependency with a forward reference via a pointer.
	var r *runtime.ProcessDriver
	bus := runtimetest.MustSignalBus(t, func(bCtx context.Context, instanceID string, trg engine.Trigger) error {
		_, err := r.Deliver(bCtx, def, instanceID, trg)
		return err
	}, signal.WithClock(fc))

	r = runtimetest.MustRunner(t, nil, store,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched),
		runtime.WithSignalBus(bus),
	)

	const instanceID = "gw-timer-1"
	parked, err := r.Run(ctx, def, instanceID, nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, parked.Status)

	// Advance clock past the 1h timer arm.
	fc.Advance(1*time.Hour + 1*time.Second)
	require.NoError(t, sched.Tick(ctx))

	final, _, err := store.Load(ctx, instanceID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status, "timer branch must complete")
	assert.Empty(t, final.Tokens, "no tokens remain")

	// Verify all history visits are closed.
	for _, v := range final.History {
		assert.NotNilf(t, v.LeftAt, "NodeVisit for %q must be closed", v.NodeID)
	}
}

// TestEventGatewaySignalWinsUnderFakeClock verifies that when the signal is
// delivered before the timer fires, the signal branch completes. The timer arm
// must be cancelled (CancelTimer issued), so Tick after the signal must not re-drive.
func TestEventGatewaySignalWinsUnderFakeClock(t *testing.T) {
	ctx := t.Context()
	startAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	store := runtimetest.MustMemStore(t)
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	def := eventGatewayDef()

	var r *runtime.ProcessDriver
	bus := runtimetest.MustSignalBus(t, func(bCtx context.Context, instanceID string, trg engine.Trigger) error {
		_, err := r.Deliver(bCtx, def, instanceID, trg)
		return err
	}, signal.WithClock(fc))

	r = runtimetest.MustRunner(t, nil, store,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched),
		runtime.WithSignalBus(bus),
	)

	const instanceID = "gw-signal-1"
	parked, err := r.Run(ctx, def, instanceID, nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, parked.Status)

	// Subscribe the instance to the bus so the signal can be published.
	bus.Subscribe(instanceID, "approved")

	// Publish the signal BEFORE the clock advances — signal arm wins.
	err = bus.Publish(ctx, "approved", map[string]any{"from": "bus"})
	require.NoError(t, err)

	final, _, err := store.Load(ctx, instanceID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status, "signal branch must complete")
	assert.Empty(t, final.Tokens, "no tokens remain")

	// Tick after signal — timer should have been cancelled, so no re-run.
	fc.Advance(2 * time.Hour)
	require.NoError(t, sched.Tick(ctx))

	// State must still be completed (not re-driven by a ghost timer).
	stillFinal, _, err := store.Load(ctx, instanceID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, stillFinal.Status, "must still be completed after Tick")
}

// TestDeliverMessageCorrelatesInstance verifies that DeliverMessage targets the
// specific correlated instance (matching name + key) and not other instances.
func TestDeliverMessageCorrelatesInstance(t *testing.T) {
	ctx := t.Context()
	fc := clockwork.NewFakeClock()

	store := runtimetest.MustMemStore(t)
	def := messageCatchDef("order-shipped")

	r := runtimetest.MustRunner(t, nil, store, runtime.WithClock(fc))

	// Start two instances with different orderId values.
	_, err := r.Run(ctx, def, "order-100", map[string]any{"orderId": "100"})
	require.NoError(t, err)

	_, err = r.Run(ctx, def, "order-200", map[string]any{"orderId": "200"})
	require.NoError(t, err)

	// Deliver message targeting orderId=100.
	err = r.DeliverMessage(ctx, def, "order-shipped", "100", map[string]any{"shipped": true})
	require.NoError(t, err)

	// order-100 must complete; order-200 must still be running.
	final100, _, err := store.Load(ctx, "order-100")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final100.Status, "order-100 must complete")

	final200, _, err := store.Load(ctx, "order-200")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, final200.Status, "order-200 must remain running")
}
