package signal_test

import (
	"context"
	"sync"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/runtime/signal"
)

// signalWaitDef parks at an intermediate catch event awaiting the "go" signal:
//
//	sw-start → sw-wait (catch signal "go") → sw-end
func signalWaitDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "signal-wait",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("sw-start"),
			event.NewIntermediateCatch("sw-wait", event.WithSignalName("go")),
			event.NewEnd("sw-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "swf1", Source: "sw-start", Target: "sw-wait"},
			{ID: "swf2", Source: "sw-wait", Target: "sw-end"},
		},
	}
}

// TestSignalBusPublishToleratesATerminalFanOutTarget pins the property that
// makes SignalReceived's rejectSilently classification correct.
//
// SignalBus.Publish is best-effort: it attempts every registered waiter and
// accumulates per-waiter failures with errors.Join. A broadcast therefore fails
// as a whole if ANY single target errors. Because a terminal instance now
// absorbs SignalReceived silently — engine.Step returns (state, nil) — a dead
// subscriber contributes no error and the broadcast still reports success for
// the live ones.
//
// Had SignalReceived been classified rejectWithError instead, every terminal
// instance still registered on the bus (subscriptions are reconciled lazily by
// the next deliverLoop's Sync, so a terminal instance CAN still be registered)
// would poison every broadcast of that signal name for every live instance.
//
// The "one target genuinely fails" case is the discriminator: it proves the
// require.NoError assertions above are not vacuous — Publish really does
// propagate a per-waiter failure through errors.Join.
func TestSignalBusPublishToleratesATerminalFanOutTarget(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// injectErr, when non-nil, is returned instead of delivering to the
		// terminal instance.
		injectErr error
		assert    func(t *testing.T, publishErr error, delivered []string, perTarget map[string]error, live engine.InstanceState)
	}

	cases := []testCase{
		{
			name: "a terminal subscriber does not fail the broadcast",
			assert: func(t *testing.T, publishErr error, delivered []string, perTarget map[string]error, live engine.InstanceState) {
				require.NoError(t, publishErr,
					"one dead instance must not fail a broadcast for every live one")
				assert.ElementsMatch(t, []string{"dead-1", "live-1"}, delivered,
					"fan-out must still attempt the terminal target")
				require.Contains(t, perTarget, "dead-1")
				require.NoError(t, perTarget["dead-1"],
					"SignalReceived is rejectSilently: a terminal instance must absorb it with a nil error")
				assert.Equal(t, engine.StatusCompleted, live.Status,
					"the live instance must have consumed the signal and run to its end event")
			},
		},
		{
			name:      "a genuinely failing target does fail the broadcast",
			injectErr: assert.AnError,
			assert: func(t *testing.T, publishErr error, _ []string, _ map[string]error, live engine.InstanceState) {
				require.Error(t, publishErr,
					"errors.Join must surface a per-waiter failure — this is what the terminal case must NOT trip")
				assert.ErrorIs(t, publishErr, assert.AnError)
				assert.Equal(t, engine.StatusCompleted, live.Status,
					"best-effort: the live instance is still delivered to despite the sibling failure")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			def := signalWaitDef()

			store, err := kernel.NewMemInstanceStore()
			require.NoError(t, err)
			reg := kernel.NewMapDefinitionRegistry(def)

			driver, err := runtime.NewProcessDriver(
				runtime.WithInstanceStore(store),
				runtime.WithDefinitions(reg),
				runtime.WithClock(clockwork.NewFakeClock()),
			)
			require.NoError(t, err)

			// live-1 parks awaiting the signal.
			liveParked, err := driver.Drive(ctx, def, "live-1", nil)
			require.NoError(t, err)
			require.Equal(t, engine.StatusRunning, liveParked.Status,
				"precondition: live-1 must park awaiting the signal")

			// dead-1 parks the same way, then is cancelled into a terminal status
			// while still registered on the bus.
			_, err = driver.Drive(ctx, def, "dead-1", nil)
			require.NoError(t, err)
			deadFinal, err := driver.CancelInstance(ctx, def, "dead-1")
			require.NoError(t, err)
			require.True(t, deadFinal.Status.IsTerminal(),
				"precondition: dead-1 must be terminal, got %v", deadFinal.Status)

			var (
				mu        sync.Mutex
				delivered []string
				perTarget = map[string]error{}
			)
			deliver := signal.DeliverFunc(func(ctx context.Context, instanceID string, trg engine.Trigger) error {
				if tc.injectErr != nil && instanceID == "dead-1" {
					mu.Lock()
					delivered = append(delivered, instanceID)
					perTarget[instanceID] = tc.injectErr
					mu.Unlock()
					return tc.injectErr
				}
				_, applyErr := driver.ApplyTrigger(ctx, def, instanceID, trg)
				mu.Lock()
				delivered = append(delivered, instanceID)
				perTarget[instanceID] = applyErr
				mu.Unlock()
				return applyErr
			})

			bus, err := signal.NewSignalBus(deliver, signal.WithClock(clockwork.NewFakeClock()))
			require.NoError(t, err)
			bus.Subscribe("live-1", "go")
			bus.Subscribe("dead-1", "go")

			publishErr := bus.Publish(ctx, "go", map[string]any{"why": "pin"})

			liveFinal, _, err := store.Load(ctx, "live-1")
			require.NoError(t, err)

			mu.Lock()
			seenIDs := append([]string(nil), delivered...)
			seenErrs := make(map[string]error, len(perTarget))
			for k, v := range perTarget {
				seenErrs[k] = v
			}
			mu.Unlock()

			tc.assert(t, publishErr, seenIDs, seenErrs, liveFinal)
		})
	}
}
