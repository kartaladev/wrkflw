package runtime_test

// eventsub_correlation_e2e_test.go — end-to-end proof that ProcessDriver's
// message and signal delivery facades correlate to an event sub-process's own
// trigger arm (ADR-0123). Before the fix, both DeliverMessage and BroadcastSignal
// silently no-op against event-sub arms because the arm is non-token-parked and
// was omitted from the runtime's waiter/subscription reconciliation.

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/signal"
)

// eventSubDef builds a process whose main path parks on a ReceiveTask
// ("DeliveryConfirmed", key=orderId) and which carries a root-level event
// sub-process "handleCancel" that arms at start. The event-sub's inner start is
// triggered by the given trigger option (message or signal); nonInterrupting
// selects the flavor. "notify-cancel" is a no-op ServiceTask so the event-sub
// child scope drains on fire.
//
//	main:  start → await[ReceiveTask "DeliveryConfirmed", key=orderId] → end
//	[event-sub "handleCancel", NO incoming flow]
//	  onTrigger[<trigger>] → notify-cancel[Service noop] → inner-end
func eventSubDef(t *testing.T, startTrigger event.StartOption, nonInterrupting bool) *model.ProcessDefinition {
	t.Helper()
	startOpts := []event.StartOption{startTrigger}
	if nonInterrupting {
		startOpts = append(startOpts, event.WithNonInterrupting())
	}
	inner, err := definition.NewBuilder("handle-cancel", 1).
		Add(event.NewStart("onTrigger", startOpts...)).
		Add(activity.NewServiceTask("notify-cancel", activity.WithTaskAction("noop"))).
		Add(event.NewEnd("inner-end")).
		Connect("onTrigger", "notify-cancel").
		Connect("notify-cancel", "inner-end").
		Build()
	require.NoError(t, err)

	def, err := definition.NewBuilder("order", 1).
		Add(event.NewStart("start")).
		Add(activity.NewReceiveTask("await", "DeliveryConfirmed", activity.WithCorrelationKey("orderId"))).
		Add(event.NewEnd("end")).
		AddSubProcess("handleCancel", inner).
		Connect("start", "await").
		Connect("await", "end").
		Build()
	require.NoError(t, err)
	return def
}

// The event-sub's "notify-cancel" step invokes the shared "noop" action defined
// by noopCatalog() (runtime/expression_timeout_test.go).

func TestDeliverMessageFiresMessageEventSubprocess(t *testing.T) {
	tests := map[string]struct {
		nonInterrupting bool
		assert          func(t *testing.T, final engine.InstanceState)
	}{
		"non-interrupting: fires alongside, main path keeps waiting": {
			nonInterrupting: true,
			assert: func(t *testing.T, final engine.InstanceState) {
				assert.Equal(t, engine.StatusRunning, final.Status,
					"non-interrupting event-sub must not cancel the main path")
				assert.Empty(t, final.EventTriggeredSubprocesses,
					"the fired message event-sub arm must be consumed (proves it was NOT a no-op)")
				require.Len(t, final.Tokens, 1, "main token still parked on await")
				assert.Equal(t, "await", final.Tokens[0].NodeID)
			},
		},
		"interrupting: cancels main path, instance completes via event-sub": {
			nonInterrupting: false,
			assert: func(t *testing.T, final engine.InstanceState) {
				assert.Equal(t, engine.StatusCompleted, final.Status,
					"interrupting event-sub must cancel the enclosing scope and drain to completion")
				assert.Empty(t, final.EventTriggeredSubprocesses, "arms swept on interrupt")
				assert.Empty(t, final.Tokens, "no tokens remain after completion")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			fc := clockwork.NewFakeClock()
			store := runtimetest.MustMemStore(t)
			def := eventSubDef(t, event.WithMessageCorrelator("cancel", "orderId"), tc.nonInterrupting)
			// DeliverMessage resolves the correlated instance's definition from the
			// registry (ADR-0121), so register it even though it is never
			// event-started here.
			reg := kernel.NewMemDefinitionRegistry()
			require.NoError(t, reg.Register(def))
			driver := runtimetest.MustRunner(t, noopCatalog(), store,
				runtime.WithClock(fc), runtime.WithDefinitions(reg))

			parked, err := driver.Drive(ctx, def, "order-1", map[string]any{"orderId": "order-1"})
			require.NoError(t, err)
			require.Equal(t, engine.StatusRunning, parked.Status, "main path must park on await")
			require.Len(t, parked.EventTriggeredSubprocesses, 1, "message event-sub must arm at start")

			// The delivery under test: correlate "cancel" to this instance's
			// event-sub message arm. Pre-fix this is a silent no-op.
			require.NoError(t, driver.DeliverMessage(ctx, "cancel", "order-1", map[string]any{"orderId": "order-1"}))

			final, _, err := store.Load(ctx, "order-1")
			require.NoError(t, err)
			tc.assert(t, final)
		})
	}
}

func TestBroadcastSignalFiresSignalEventSubprocess(t *testing.T) {
	tests := map[string]struct {
		nonInterrupting bool
		assert          func(t *testing.T, final engine.InstanceState)
	}{
		"non-interrupting: fires alongside, main path keeps waiting": {
			nonInterrupting: true,
			assert: func(t *testing.T, final engine.InstanceState) {
				assert.Equal(t, engine.StatusRunning, final.Status,
					"non-interrupting event-sub must not cancel the main path")
				assert.Empty(t, final.EventTriggeredSubprocesses,
					"the fired signal event-sub arm must be consumed (proves it was NOT a no-op)")
				require.Len(t, final.Tokens, 1, "main token still parked on await")
				assert.Equal(t, "await", final.Tokens[0].NodeID)
			},
		},
		"interrupting: cancels main path, instance completes via event-sub": {
			nonInterrupting: false,
			assert: func(t *testing.T, final engine.InstanceState) {
				assert.Equal(t, engine.StatusCompleted, final.Status,
					"interrupting event-sub must cancel the enclosing scope and drain to completion")
				assert.Empty(t, final.EventTriggeredSubprocesses, "arms swept on interrupt")
				assert.Empty(t, final.Tokens, "no tokens remain after completion")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			fc := clockwork.NewFakeClockAt(time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC))
			store := runtimetest.MustMemStore(t)
			def := eventSubDef(t, event.WithSignalName("cancel-sig"), tc.nonInterrupting)

			// Forward-reference wiring: the bus delivers via driver.ApplyTrigger,
			// and the driver owns the bus (same graph a consumer builds once).
			var driver *runtime.ProcessDriver
			bus := runtimetest.MustSignalBus(t, func(bCtx context.Context, instanceID string, trg engine.Trigger) error {
				_, derr := driver.ApplyTrigger(bCtx, def, instanceID, trg)
				return derr
			}, signal.WithClock(fc))
			driver = runtimetest.MustRunner(t, noopCatalog(), store,
				runtime.WithClock(fc), runtime.WithSignalBus(bus))

			parked, err := driver.Drive(ctx, def, "order-1", map[string]any{"orderId": "order-1"})
			require.NoError(t, err)
			require.Equal(t, engine.StatusRunning, parked.Status, "main path must park on await")
			require.Len(t, parked.EventTriggeredSubprocesses, 1, "signal event-sub must arm at start")

			// The delivery under test: broadcast "cancel-sig" must reach this
			// instance's event-sub signal arm. Pre-fix this is a silent no-op.
			require.NoError(t, driver.BroadcastSignal(ctx, "cancel-sig", map[string]any{"orderId": "order-1"}))

			final, _, err := store.Load(ctx, "order-1")
			require.NoError(t, err)
			tc.assert(t, final)
		})
	}
}
