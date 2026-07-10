package runtime_test

// TestBroadcastSignalResumesParkedInstances and TestBroadcastSignalWithoutBusErrors
// are kept as standalone TestXxx funcs (not a table) because their setup is
// structurally different: the happy path needs a forward-referenced SignalBus plus
// two instances driven to park, while the error path deliberately constructs a
// bus-less driver with no signal-start definitions at all (the one remaining case
// where BroadcastSignal must still fail).
//
// TestBroadcastSignalFanOut below IS a table: every case shares the same call
// shape (register defs, call BroadcastSignal, assert on the store), so it follows
// the mandatory table-test form.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/idgen"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/signal"
)

// errFakeIDGen and errFakeDelivery are test-only sentinel errors used to force
// the id-generation and SignalBus-delivery failure branches of BroadcastSignal's
// fan-out (see TestBroadcastSignalFanOut), so errors.Join composition is
// actually exercised rather than merely reachable in principle.
var (
	errFakeIDGen    = errors.New("fake id generator failure")
	errFakeDelivery = errors.New("fake signal delivery failure")
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

// signalStartDef builds a minimal signal-start definition: a signal-start event
// (on signalName) flowing straight to an end, so a created instance runs to
// completion with no external collaborators.
func signalStartDef(t *testing.T, defID, signalName string) *model.ProcessDefinition {
	t.Helper()
	return &model.ProcessDefinition{
		ID:      defID,
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start", event.WithSignalName(signalName)),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "end"},
		},
	}
}

// countCompletedInstances returns the number of instances in store whose status
// is StatusCompleted.
func countCompletedInstances(t *testing.T, store *kernel.MemInstanceStore) int {
	t.Helper()
	page, err := store.List(t.Context(), kernel.InstanceFilter{Limit: 200})
	require.NoError(t, err)
	n := 0
	for _, item := range page.Items {
		if item.Status == engine.StatusCompleted {
			n++
		}
	}
	return n
}

// TestBroadcastSignalFanOut verifies BroadcastSignal's signal-start fan-out
// create (ADR-0121): on top of resuming parked waiters through the SignalBus, it
// creates one new instance per registered definition whose start event listens
// for the broadcast signal name — and the relaxed nil-sigbus guard, which now
// only errors when there is neither a bus nor any signal-start match.
func TestBroadcastSignalFanOut(t *testing.T) {
	type testCase struct {
		name string
		defs []*model.ProcessDefinition
		// broadcast overrides the signal name passed to BroadcastSignal; nil means
		// the default "order.completed". A non-nil pointer to "" exercises the
		// empty-name guard.
		broadcast *string
		// moreOpts builds extra driver options beyond WithDefinitions(reg); nil
		// means none. Used to inject a failing SignalBus or id generator for the
		// error-composition cases below.
		moreOpts func(t *testing.T) []runtime.Option
		assert   func(t *testing.T, store *kernel.MemInstanceStore, err error)
	}

	strptr := func(s string) *string { return &s }

	cases := []testCase{
		{
			name: "fans out to every registered signal-start def, creating one instance each",
			defs: []*model.ProcessDefinition{
				signalStartDef(t, "payment", "order.completed"),
				signalStartDef(t, "shipment", "order.completed"),
			},
			assert: func(t *testing.T, store *kernel.MemInstanceStore, err error) {
				require.NoError(t, err)
				assert.Equal(t, 2, countCompletedInstances(t, store), "one completed instance per signal-start def")
			},
		},
		{
			name: "no SignalBus configured but a signal-start matches: still creates, no error",
			defs: []*model.ProcessDefinition{
				signalStartDef(t, "solo", "order.completed"),
			},
			assert: func(t *testing.T, store *kernel.MemInstanceStore, err error) {
				require.NoError(t, err)
				assert.Equal(t, 1, countCompletedInstances(t, store))
			},
		},
		{
			name: "no SignalBus and no signal-start match: still errors",
			defs: nil,
			assert: func(t *testing.T, store *kernel.MemInstanceStore, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "SignalBus")
				assert.Equal(t, 0, countCompletedInstances(t, store))
			},
		},
		{
			name: "empty signal name is a clean no-op and never matches a manual start",
			defs: []*model.ProcessDefinition{
				// A manual (trigger-less) start has SignalName == ""; the guard must
				// stop it from matching an empty broadcast name.
				{
					ID: "manual-sig", Version: 1,
					Nodes: []model.Node{event.NewStart("start"), event.NewEnd("end")},
					Flows: []flow.SequenceFlow{{ID: "f1", Source: "start", Target: "end"}},
				},
			},
			broadcast: strptr(""),
			assert: func(t *testing.T, store *kernel.MemInstanceStore, err error) {
				require.NoError(t, err, "empty signal name must be a clean no-op, not an error")
				assert.Equal(t, 0, countCompletedInstances(t, store), "empty signal name must not spawn any instance")
			},
		},
		{
			name: "signal-start id-generation failure is joined into the returned error",
			defs: []*model.ProcessDefinition{
				signalStartDef(t, "flaky", "order.completed"),
			},
			moreOpts: func(t *testing.T) []runtime.Option {
				t.Helper()
				failingGen := idgen.Func(func() (string, error) { return "", errFakeIDGen })
				return []runtime.Option{runtime.WithIDGenerator(failingGen)}
			},
			assert: func(t *testing.T, store *kernel.MemInstanceStore, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, errFakeIDGen)
				assert.Equal(t, 0, countCompletedInstances(t, store), "no instance is created when id generation fails")
			},
		},
		{
			name: "SignalBus publish failure is joined into the error but signal-start creation still proceeds",
			defs: []*model.ProcessDefinition{
				signalStartDef(t, "solo2", "order.completed"),
			},
			moreOpts: func(t *testing.T) []runtime.Option {
				t.Helper()
				bus := runtimetest.MustSignalBus(t, func(context.Context, string, engine.Trigger) error {
					return errFakeDelivery
				})
				bus.Subscribe("waiter-1", "order.completed") // gives Publish a waiter to fail on
				return []runtime.Option{runtime.WithSignalBus(bus)}
			},
			assert: func(t *testing.T, store *kernel.MemInstanceStore, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, errFakeDelivery)
				assert.Equal(t, 1, countCompletedInstances(t, store), "signal-start creation still proceeds despite the bus publish failure")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := kernel.NewMemDefinitionRegistry()
			for _, d := range tc.defs {
				require.NoError(t, reg.Register(d))
			}
			store := runtimetest.MustMemStore(t)
			opts := []runtime.Option{runtime.WithDefinitions(reg)}
			if tc.moreOpts != nil {
				opts = append(opts, tc.moreOpts(t)...)
			}
			driver := runtimetest.MustRunner(t, nil, store, opts...)

			signalName := "order.completed"
			if tc.broadcast != nil {
				signalName = *tc.broadcast
			}
			err := driver.BroadcastSignal(t.Context(), signalName, map[string]any{"orderId": "7"})
			tc.assert(t, store, err)
		})
	}
}
