package processtest_test

// waiter_sources_test.go — ADR-0166. Classify must derive AwaitingSignals and
// AwaitingMessages from the engine's own authorities (InstanceState.SignalWaiters
// / MessageWaiters), which enumerate FOUR sources: token awaits, boundary arms,
// event-based-gateway arms and event-subprocess arms. Deriving them from
// Token.AwaitSignal/AwaitMessage alone drops the latter three, so a definition
// parked purely on an arm cannot be driven through the public harness at all.
//
// Every fixture here is harness-driven rather than an InstanceState literal:
// the arm slices (Boundaries, ArmedEvents, EventTriggeredSubprocesses) have
// unexported element types, so an arm park cannot be constructed by hand.

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/processtest"
)

// noopAction registers a do-nothing action so a boundary's downstream path can
// run. An UNregistered action fails the instance early and masks what these
// tests measure.
func noopAction(name string) processtest.Option {
	return processtest.WithCatalogActionFunc(name, func(context.Context, map[string]any) (map[string]any, error) {
		return nil, nil
	})
}

// signalBoundaryDef parks on a user task carrying an interrupting SIGNAL
// boundary. Nothing sets Token.AwaitSignal for a boundary arm — the name lives
// only in state.Boundaries.
//
//	start → approve[User] → end
//	approve ⊸ esc[Boundary signal "escalate"] → escalated[Service notify] → esc-end
func signalBoundaryDef(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	def, err := definition.NewBuilder("sig-boundary", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("approve", activity.WithEligibleRoles("r"))).
		Add(event.NewEnd("end")).
		Add(event.NewBoundary("esc", "approve", event.WithSignalName("escalate"))).
		Add(activity.NewServiceTask("escalated", activity.WithTaskAction("notify"))).
		Add(event.NewEnd("esc-end")).
		Connect("start", "approve").
		Connect("approve", "end").
		Connect("esc", "escalated").
		Connect("escalated", "esc-end").
		Build()
	require.NoError(t, err)
	return def
}

// messageBoundaryDef is signalBoundaryDef's message twin, with a NON-EMPTY
// correlation key so the key's survival through Classify is observable. The key
// is an expr, so it must be a quoted string literal: bare order-1 parses as the
// subtraction `order - 1`.
//
//	start → approve[User] → end
//	approve ⊸ cancel[Boundary message "Cancelled" key "order-1"] → cancelled[Service notify] → cancel-end
func messageBoundaryDef(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	def, err := definition.NewBuilder("msg-boundary", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("approve", activity.WithEligibleRoles("r"))).
		Add(event.NewEnd("end")).
		Add(event.NewBoundary("cancel", "approve", event.WithMessageCorrelator("Cancelled", `"order-1"`))).
		Add(activity.NewServiceTask("cancelled", activity.WithTaskAction("notify"))).
		Add(event.NewEnd("cancel-end")).
		Connect("start", "approve").
		Connect("approve", "end").
		Connect("cancel", "cancelled").
		Connect("cancelled", "cancel-end").
		Build()
	require.NoError(t, err)
	return def
}

// eventGatewayArmDef parks on an event-based gateway racing a SIGNAL arm against
// a MESSAGE arm. Both arms live in state.ArmedEvents; the token itself parks on a
// bare command wait.
//
//	start → evtgw → sig-catch[signal "approved"] → sig-end
//	              → msg-catch[message "Cancel"]  → msg-end
func eventGatewayArmDef(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	def, err := definition.NewBuilder("evtgw-arms", 1).
		Add(event.NewStart("start")).
		Add(gateway.NewEventBased("evtgw")).
		Add(event.NewIntermediateCatch("sig-catch", event.WithSignalName("approved"))).
		Add(event.NewIntermediateCatch("msg-catch", event.WithMessageCorrelator("Cancel", ""))).
		Add(event.NewEnd("sig-end")).
		Add(event.NewEnd("msg-end")).
		Connect("start", "evtgw").
		Connect("evtgw", "sig-catch").
		Connect("evtgw", "msg-catch").
		Connect("sig-catch", "sig-end").
		Connect("msg-catch", "msg-end").
		Build()
	require.NoError(t, err)
	return def
}

// eventSubprocessSignalDef parks on a user task while a root-level
// SIGNAL-triggered event sub-process stays armed. The arm carries no token at
// all — it lives only in state.EventTriggeredSubprocesses.
//
//	start → approve[User] → end
//	[event-sub "handleAbort", no incoming flow]
//	  onAbort[start signal "abort"] → notify[Service notify] → abort-end
func eventSubprocessSignalDef(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	inner, err := definition.NewBuilder("handle-abort", 1).
		Add(event.NewStart("onAbort", event.WithSignalName("abort"))).
		Add(activity.NewServiceTask("notify-abort", activity.WithTaskAction("notify"))).
		Add(event.NewEnd("abort-end")).
		Connect("onAbort", "notify-abort").
		Connect("notify-abort", "abort-end").
		Build()
	require.NoError(t, err)

	def, err := definition.NewBuilder("evtsub-arm", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("approve", activity.WithEligibleRoles("r"))).
		Add(event.NewEnd("end")).
		AddSubProcess("handleAbort", inner).
		Connect("start", "approve").
		Connect("approve", "end").
		Build()
	require.NoError(t, err)
	return def
}

// startParked builds a harness, starts def, and returns the parked state.
func startParked(t *testing.T, def *model.ProcessDefinition, opts ...processtest.Option) engine.InstanceState {
	t.Helper()
	h, err := processtest.New(append([]processtest.Option{noopAction("notify")}, opts...)...)
	require.NoError(t, err)
	st, err := h.Start(t.Context(), def, "i", nil)
	require.NoError(t, err)
	require.False(t, processtest.IsTerminal(st.Status), "fixture must park, not terminate")
	return st
}

// TestClassifyEnumeratesEveryWaiterSource covers spec rows 1-4: each non-token
// arm source must reach Park's discrete await fields. Each row asserts against
// the engine's own authority as well, so a row cannot silently pass by both
// sides being empty.
func TestClassifyEnumeratesEveryWaiterSource(t *testing.T) {
	t.Parallel()

	type testCase struct {
		def    func(*testing.T) *model.ProcessDefinition
		assert func(t *testing.T, p processtest.Park, st engine.InstanceState)
	}

	tests := map[string]testCase{
		// Row 1 — boundary arm, signal side.
		"signal boundary arm reaches AwaitingSignals": {
			def: signalBoundaryDef,
			assert: func(t *testing.T, p processtest.Park, st engine.InstanceState) {
				require.Equal(t, []string{"escalate"}, st.SignalWaiters(), "engine authority")
				assert.Equal(t, []string{"escalate"}, p.AwaitingSignals)
			},
		},
		// Row 2 — event-subprocess arm, signal side.
		"event-subprocess signal arm reaches AwaitingSignals": {
			def: eventSubprocessSignalDef,
			assert: func(t *testing.T, p processtest.Park, st engine.InstanceState) {
				require.Equal(t, []string{"abort"}, st.SignalWaiters(), "engine authority")
				assert.Equal(t, []string{"abort"}, p.AwaitingSignals)
			},
		},
		// Row 3 — event-based-gateway arm, both sides.
		"event-gateway arms reach both await fields": {
			def: eventGatewayArmDef,
			assert: func(t *testing.T, p processtest.Park, st engine.InstanceState) {
				require.Equal(t, []string{"approved"}, st.SignalWaiters(), "engine authority")
				assert.Equal(t, []string{"approved"}, p.AwaitingSignals)
				assert.Equal(t, []engine.MessageWaiter{{Name: "Cancel"}}, p.AwaitingMessages)
			},
		},
		// Row 4 — boundary arm, message side, with a non-empty correlation key.
		// The key is the whole reason AwaitingMessages changed type: a consumer
		// has no other way to discover which key an arm expects.
		"message boundary arm reaches AwaitingMessages with its correlation key": {
			def: messageBoundaryDef,
			assert: func(t *testing.T, p processtest.Park, st engine.InstanceState) {
				want := []engine.MessageWaiter{{Name: "Cancelled", CorrelationKey: "order-1"}}
				require.Equal(t, want, st.MessageWaiters(), "engine authority")
				assert.Equal(t, want, p.AwaitingMessages)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			st := startParked(t, tc.def(t))
			tc.assert(t, processtest.Classify(st), st)
		})
	}
}

// TestPublishSignalResolvesBoundaryOnlyPark covers spec row 5: an arm-only park
// must be driveable end to end through the shipped handler. Before the fix
// PublishSignal iterates an empty AwaitingSignals, returns Pass forever, and the
// drive fails with ErrUnhandledPark.
func TestPublishSignalResolvesBoundaryOnlyPark(t *testing.T) {
	t.Parallel()

	def := signalBoundaryDef(t)
	h, err := processtest.New(noopAction("notify"))
	require.NoError(t, err)
	_, err = h.Start(t.Context(), def, "i", nil)
	require.NoError(t, err)

	final, err := h.DriveToCompletion(t.Context(), def, "i", h.PublishSignal("escalate", nil))
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status)
	assert.Equal(t, 1, h.Catalog().Count("notify"), "the boundary's downstream path must have run")
}

// TestDeliverMessageResolvesMessageBoundaryOnlyPark covers spec row 6, the
// message twin of row 5.
func TestDeliverMessageResolvesMessageBoundaryOnlyPark(t *testing.T) {
	t.Parallel()

	def := messageBoundaryDef(t)
	h, err := processtest.New(noopAction("notify"))
	require.NoError(t, err)
	_, err = h.Start(t.Context(), def, "i", nil)
	require.NoError(t, err)

	final, err := h.DriveToCompletion(t.Context(), def, "i", h.DeliverMessage("Cancelled", "order-1", nil))
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status)
	assert.Equal(t, 1, h.Catalog().Count("notify"), "the boundary's downstream path must have run")
}

// timerCatchBesideArmDef parks on a timer catch while a SIGNAL-triggered event
// sub-process stays armed — a timer park that coexists with a live arm.
//
//	start → t-catch[timer "1h"] → end
//	[event-sub "handleAbort", no incoming flow]
//	  onAbort[start signal "abort"] → notify-abort[Service notify] → abort-end
func timerCatchBesideArmDef(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	inner, err := definition.NewBuilder("handle-abort", 1).
		Add(event.NewStart("onAbort", event.WithSignalName("abort"))).
		Add(activity.NewServiceTask("notify-abort", activity.WithTaskAction("notify"))).
		Add(event.NewEnd("abort-end")).
		Connect("onAbort", "notify-abort").
		Connect("notify-abort", "abort-end").
		Build()
	require.NoError(t, err)

	def, err := definition.NewBuilder("timer-beside-arm", 1).
		Add(event.NewStart("start")).
		Add(event.NewIntermediateCatch("t-catch", event.WithCatchTimer(schedule.AfterExpr(`"1h"`)))).
		Add(event.NewEnd("end")).
		AddSubProcess("handleAbort", inner).
		Connect("start", "t-catch").
		Connect("t-catch", "end").
		Build()
	require.NoError(t, err)
	return def
}

// TestAutoTimersStillDrivesTimerBesideLiveArm covers spec row 7 — a guard on D3.
// harnessEnv.classify promotes a command-wait token whose command is a pending
// scheduler timer to ReasonTimer, but only FROM ReasonAsyncChild/ReasonUnknown.
// Once arms reach AwaitingSignals, an arm-derived ReasonSignal displaces that
// reason, the promotion never fires, and AutoTimers() — which acts only on
// ReasonTimer — passes forever. The shipped AutoTimers() recipe would have
// silently stopped working on any definition carrying a live arm.
func TestAutoTimersStillDrivesTimerBesideLiveArm(t *testing.T) {
	t.Parallel()

	def := timerCatchBesideArmDef(t)
	h, err := processtest.New(noopAction("notify"))
	require.NoError(t, err)
	_, err = h.Start(t.Context(), def, "i", nil)
	require.NoError(t, err)

	final, err := h.DriveToCompletion(t.Context(), def, "i", processtest.AutoTimers())
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status)
}

// TestTwoSequentialCatchesOfOneSignalBothFire covers spec row 8 — a guard on D4.
// Bounding delivery to "each name at most once" is the obvious way to stop the
// non-interrupting loop (row 10) and it is wrong: two sequential catches of one
// name is ordinary BPMN that passes today. Both catches here are TOKEN catches,
// and a token catch is never bounded: it is consumed when it fires, so it cannot
// re-match and every match is a real one.
func TestTwoSequentialCatchesOfOneSignalBothFire(t *testing.T) {
	t.Parallel()

	def, err := definition.NewBuilder("two-catch", 1).
		Add(event.NewStart("start")).
		Add(event.NewIntermediateCatch("c1", event.WithSignalName("go"))).
		Add(event.NewIntermediateCatch("c2", event.WithSignalName("go"))).
		Add(event.NewEnd("end")).
		Connect("start", "c1").
		Connect("c1", "c2").
		Connect("c2", "end").
		Build()
	require.NoError(t, err)

	h, err := processtest.New()
	require.NoError(t, err)
	_, err = h.Start(t.Context(), def, "i", nil)
	require.NoError(t, err)

	final, err := h.DriveToCompletion(t.Context(), def, "i", h.PublishSignal("go", nil))
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status, "both catches must fire")
}

// TestNonInterruptingBoundaryTerminatesInsteadOfSpinning covers spec row 10 — the
// other guard on D4, and the regression this whole fix would otherwise introduce.
// A token signal-catch is CONSUMED when it fires and cannot re-match; a
// non-interrupting arm stays armed indefinitely. So once arms reach
// AwaitingSignals, PublishSignal re-matches the identical park forever and the
// drive spins to its step limit without ever reaching CompleteTasks.
func TestNonInterruptingBoundaryTerminatesInsteadOfSpinning(t *testing.T) {
	t.Parallel()

	def, err := definition.NewBuilder("non-interrupting", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("approve", activity.WithEligibleRoles("r"))).
		Add(event.NewEnd("end")).
		Add(event.NewBoundary("ping", "approve",
			event.WithSignalName("ping"), event.WithBoundaryNonInterrupting())).
		Add(activity.NewServiceTask("pinged", activity.WithTaskAction("notify"))).
		Add(event.NewEnd("ping-end")).
		Connect("start", "approve").
		Connect("approve", "end").
		Connect("ping", "pinged").
		Connect("pinged", "ping-end").
		Build()
	require.NoError(t, err)

	h, err := processtest.New(noopAction("notify"))
	require.NoError(t, err)
	_, err = h.Start(t.Context(), def, "i", nil)
	require.NoError(t, err)

	decide := func(humantask.HumanTask) (authz.Actor, map[string]any, bool) {
		return authz.Actor{ID: "alice", Roles: []string{"r"}}, nil, true
	}
	final, err := h.DriveToCompletion(t.Context(), def, "i",
		processtest.Chain(h.PublishSignal("ping", nil), h.CompleteTasks(decide)))
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status)
	assert.Equal(t, 1, h.Catalog().Count("notify"), "the arm fires once per waiter set, not once per drive step")
}

// TestAutoTimersStillDrivesTimerBesideLiveMessageArm is row 7's message twin. D3
// widens the promotion for arm-derived ReasonSignal AND ReasonMessage; without
// this the message half of that widening would ship unexercised.
func TestAutoTimersStillDrivesTimerBesideLiveMessageArm(t *testing.T) {
	t.Parallel()

	inner, err := definition.NewBuilder("handle-cancel", 1).
		Add(event.NewStart("onCancel", event.WithMessageCorrelator("Cancelled", ""))).
		Add(activity.NewServiceTask("notify-cancel", activity.WithTaskAction("notify"))).
		Add(event.NewEnd("cancel-inner-end")).
		Connect("onCancel", "notify-cancel").
		Connect("notify-cancel", "cancel-inner-end").
		Build()
	require.NoError(t, err)

	def, err := definition.NewBuilder("timer-beside-msg-arm", 1).
		Add(event.NewStart("start")).
		Add(event.NewIntermediateCatch("t-catch", event.WithCatchTimer(schedule.AfterExpr(`"1h"`)))).
		Add(event.NewEnd("end")).
		AddSubProcess("handleCancel", inner).
		Connect("start", "t-catch").
		Connect("t-catch", "end").
		Build()
	require.NoError(t, err)

	h, err := processtest.New(noopAction("notify"))
	require.NoError(t, err)
	_, err = h.Start(t.Context(), def, "i", nil)
	require.NoError(t, err)

	final, err := h.DriveToCompletion(t.Context(), def, "i", processtest.AutoTimers())
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status)
}

// TestNonInterruptingMessageBoundaryTerminatesInsteadOfSpinning is row 10's
// message twin: DeliverMessage carries its own arm bound, so the spin it prevents
// must be pinned on the message side too.
func TestNonInterruptingMessageBoundaryTerminatesInsteadOfSpinning(t *testing.T) {
	t.Parallel()

	def, err := definition.NewBuilder("non-interrupting-msg", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("approve", activity.WithEligibleRoles("r"))).
		Add(event.NewEnd("end")).
		Add(event.NewBoundary("nudge", "approve",
			event.WithMessageCorrelator("Nudge", `"order-1"`), event.WithBoundaryNonInterrupting())).
		Add(activity.NewServiceTask("nudged", activity.WithTaskAction("notify"))).
		Add(event.NewEnd("nudge-end")).
		Connect("start", "approve").
		Connect("approve", "end").
		Connect("nudge", "nudged").
		Connect("nudged", "nudge-end").
		Build()
	require.NoError(t, err)

	h, err := processtest.New(noopAction("notify"))
	require.NoError(t, err)
	_, err = h.Start(t.Context(), def, "i", nil)
	require.NoError(t, err)

	decide := func(humantask.HumanTask) (authz.Actor, map[string]any, bool) {
		return authz.Actor{ID: "alice", Roles: []string{"r"}}, nil, true
	}
	final, err := h.DriveToCompletion(t.Context(), def, "i",
		processtest.Chain(h.DeliverMessage("Nudge", "order-1", nil), h.CompleteTasks(decide)))
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status)
	assert.Equal(t, 1, h.Catalog().Count("notify"))
}

// TestHandlersPassOnUnawaitedName pins the name guard: a handler for a name the
// instance does not await must Pass, so it composes under Chain rather than
// delivering something the engine will merely no-op on.
//
// It asserts the DECISION, not the drive outcome. An earlier version of this test
// drove to completion and asserted ErrUnhandledPark — which is vacuous: with the
// guard removed the handler delivers an unawaited signal, the engine no-ops, and
// the drive still ends in ErrUnhandledPark. Replacing both guards with `if false`
// left the whole package green.
func TestHandlersPassOnUnawaitedName(t *testing.T) {
	t.Parallel()

	st := startParked(t, signalBoundaryDef(t))
	p := processtest.Classify(st)
	h, err := processtest.New(noopAction("notify"))
	require.NoError(t, err)

	sigDecision, err := h.PublishSignal("other", nil)(t.Context(), p)
	require.NoError(t, err)
	assert.Equal(t, processtest.Pass(), sigDecision, "PublishSignal must Pass on an unawaited name")

	msgDecision, err := h.DeliverMessage("other", "k", nil)(t.Context(), p)
	require.NoError(t, err)
	assert.Equal(t, processtest.Pass(), msgDecision, "DeliverMessage must Pass on an unawaited name")

	// Contrast: the awaited name must NOT Pass, else the assertions above would
	// hold for a handler that passes unconditionally.
	live, err := h.PublishSignal("escalate", nil)(t.Context(), p)
	require.NoError(t, err)
	assert.NotEqual(t, processtest.Pass(), live, "the awaited name must deliver")
}

// TestTokenCatchLoopRedeliversAtTheSameNode is finding 2 of the pre-delivery
// review. A token that loops back to the SAME catch node keeps both its id and its
// node, so the fingerprint this bundle first shipped — {tokenID@nodeID, arm
// counts} — was byte-identical at both parks and dropped the second delivery:
// ordinary BPMN that completes on main failing with ErrUnhandledPark.
//
// The root fix is not a wider key: a TOKEN catch is CONSUMED when it fires and
// therefore can never spin, so it must not be bounded at all. Only an ARM can
// re-match, and only arms are bounded.
func TestTokenCatchLoopRedeliversAtTheSameNode(t *testing.T) {
	t.Parallel()

	def, err := definition.NewBuilder("catch-loop", 1).
		Add(event.NewStart("start")).
		Add(event.NewIntermediateCatch("c1", event.WithSignalName("go"))).
		Add(activity.NewServiceTask("tick", activity.WithTaskAction("tick"))).
		Add(gateway.NewExclusive("gw")).
		Add(event.NewEnd("end")).
		Connect("start", "c1").
		Connect("c1", "tick").
		Connect("tick", "gw").
		Connect("gw", "c1", flow.WithCondition("n < 2")).
		Connect("gw", "end").
		Build()
	require.NoError(t, err)

	ticks := 0
	h, err := processtest.New(processtest.WithCatalogActionFunc("tick",
		func(_ context.Context, vars map[string]any) (map[string]any, error) {
			ticks++
			n, _ := vars["n"].(int)
			return map[string]any{"n": n + 1}, nil
		}))
	require.NoError(t, err)
	_, err = h.Start(t.Context(), def, "i", map[string]any{"n": 0})
	require.NoError(t, err)

	final, err := h.DriveToCompletion(t.Context(), def, "i", h.PublishSignal("go", nil))
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status)
	assert.Equal(t, 2, ticks, "the catch must fire on every loop iteration")
}

// TestNonInterruptingArmDoesNotSpinWhenItsBranchArms is finding 1. Keying the
// delivery bound on INSTANCE-WIDE arm counts defeats it: the arm's own downstream
// branch here arms a deadline boundary, so len(state.Boundaries) grows on every
// firing, the key changes, and delivery is re-authorised forever.
//
// The bound must be scoped to the waiters for THIS NAME, not to how many arms the
// instance happens to hold.
func TestNonInterruptingArmDoesNotSpinWhenItsBranchArms(t *testing.T) {
	t.Parallel()

	def, err := definition.NewBuilder("arming-branch", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("approve", activity.WithEligibleRoles("r"))).
		Add(event.NewEnd("end")).
		Add(event.NewBoundary("ping", "approve",
			event.WithSignalName("ping"), event.WithBoundaryNonInterrupting())).
		// The arm's branch is itself a task carrying a deadline boundary, so each
		// firing adds an entry to state.Boundaries.
		Add(activity.NewUserTask("review", activity.WithEligibleRoles("r"))).
		Add(event.NewBoundary("rdead", "review",
			event.WithBoundaryTimer(schedule.AfterExpr(`"24h"`)))).
		Add(event.NewEnd("review-end")).
		Add(event.NewEnd("rdead-end")).
		Connect("start", "approve").
		Connect("approve", "end").
		Connect("ping", "review").
		Connect("review", "review-end").
		Connect("rdead", "rdead-end").
		Build()
	require.NoError(t, err)

	h, err := processtest.New()
	require.NoError(t, err)
	_, err = h.Start(t.Context(), def, "i", nil)
	require.NoError(t, err)

	decide := func(humantask.HumanTask) (authz.Actor, map[string]any, bool) {
		return authz.Actor{ID: "alice", Roles: []string{"r"}}, nil, true
	}
	final, err := h.DriveToCompletion(t.Context(), def, "i",
		processtest.Chain(h.PublishSignal("ping", nil), h.CompleteTasks(decide)))
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status)
}

// TestTwoSequentialArmsOfOneNameBothFire is the /code-review headline finding: the
// arm bound must distinguish two genuinely DIFFERENT arms of the same name that
// are live at different times. A bound keyed on the SIZE of the waiter set cannot
// — both parks report exactly one "go" waiter — so the second arm was silently
// never delivered to.
//
// It is the same falsification the design audit already found for token catches
// (start → catch("go") → catch("go")), reproduced one level up on the arm side.
func TestTwoSequentialArmsOfOneNameBothFire(t *testing.T) {
	t.Parallel()

	def, err := definition.NewBuilder("two-arms", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("approve1", activity.WithEligibleRoles("r"))).
		Add(event.NewBoundary("esc1", "approve1", event.WithSignalName("go"))).
		Add(activity.NewUserTask("approve2", activity.WithEligibleRoles("r"))).
		Add(event.NewBoundary("esc2", "approve2", event.WithSignalName("go"))).
		Add(activity.NewServiceTask("done", activity.WithTaskAction("notify"))).
		Add(event.NewEnd("end")).
		Connect("start", "approve1").
		Connect("esc1", "approve2").
		Connect("esc2", "done").
		Connect("done", "end").
		Connect("approve1", "end").
		Connect("approve2", "end").
		Build()
	require.NoError(t, err)

	h, err := processtest.New(noopAction("notify"))
	require.NoError(t, err)
	_, err = h.Start(t.Context(), def, "i", nil)
	require.NoError(t, err)

	final, err := h.DriveToCompletion(t.Context(), def, "i", h.PublishSignal("go", nil))
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status)
	assert.Equal(t, 1, h.Catalog().Count("notify"), "the SECOND arm must be delivered to as well")
}

// TestNonInterruptingArmDoesNotSpinWhenItsBranchArmsTheSameName is the /code-review
// Low that is the exact mirror of the test above: when the arm's own branch arms
// another waiter of the SAME name, a size-keyed bound grows on every firing and
// re-authorises delivery forever.
//
// The two together are why the bound keys on WHICH nodes have been delivered
// against, not on how many waiters there are: the count moves in the wrong
// direction in one case and fails to move in the other.
func TestNonInterruptingArmDoesNotSpinWhenItsBranchArmsTheSameName(t *testing.T) {
	t.Parallel()

	def, err := definition.NewBuilder("same-name-rearm", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("approve", activity.WithEligibleRoles("r"))).
		Add(event.NewEnd("end")).
		Add(event.NewBoundary("ping", "approve",
			event.WithSignalName("ping"), event.WithBoundaryNonInterrupting())).
		Add(activity.NewUserTask("review", activity.WithEligibleRoles("r"))).
		Add(event.NewBoundary("ping2", "review",
			event.WithSignalName("ping"), event.WithBoundaryNonInterrupting())).
		Add(activity.NewServiceTask("pinged", activity.WithTaskAction("notify"))).
		Add(event.NewEnd("review-end")).
		Add(event.NewEnd("ping-end")).
		Connect("start", "approve").
		Connect("approve", "end").
		Connect("ping", "review").
		Connect("review", "review-end").
		Connect("ping2", "pinged").
		Connect("pinged", "ping-end").
		Build()
	require.NoError(t, err)

	h, err := processtest.New(noopAction("notify"), processtest.WithDriveLimit(60))
	require.NoError(t, err)
	_, err = h.Start(t.Context(), def, "i", nil)
	require.NoError(t, err)

	decide := func(humantask.HumanTask) (authz.Actor, map[string]any, bool) {
		return authz.Actor{ID: "alice", Roles: []string{"r"}}, nil, true
	}
	final, err := h.DriveToCompletion(t.Context(), def, "i",
		processtest.Chain(h.PublishSignal("ping", nil), h.CompleteTasks(decide)))
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status)
}

// TestSharedHandlerIsDeterministicAcrossConcurrentDrives is finding 3. A single
// last-key slot does not collide across instances once the key carries the
// instance id — it DISPLACES: instance B's key evicts A's, so A's next identical
// park is delivered to again. The mutex makes that race-free, not correct.
//
// The non-interrupting arm is what exposes it; the interrupting boundary used by
// TestOneHandlerValueDrivesTwoInstancesConcurrently delivers once per instance
// anyway, so alternation is invisible there.
func TestSharedHandlerIsDeterministicAcrossConcurrentDrives(t *testing.T) {
	t.Parallel()

	def, err := definition.NewBuilder("shared-nonint", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("approve", activity.WithEligibleRoles("r"))).
		Add(event.NewEnd("end")).
		Add(event.NewBoundary("ping", "approve",
			event.WithSignalName("ping"), event.WithBoundaryNonInterrupting())).
		Add(activity.NewServiceTask("pinged", activity.WithTaskAction("notify"))).
		Add(event.NewEnd("ping-end")).
		Connect("start", "approve").
		Connect("approve", "end").
		Connect("ping", "pinged").
		Connect("pinged", "ping-end").
		Build()
	require.NoError(t, err)

	h, err := processtest.New(noopAction("notify"))
	require.NoError(t, err)

	ids := []string{"i1", "i2", "i3", "i4"}
	for _, id := range ids {
		_, serr := h.Start(t.Context(), def, id, nil)
		require.NoError(t, serr)
	}

	decide := func(humantask.HumanTask) (authz.Actor, map[string]any, bool) {
		return authz.Actor{ID: "alice", Roles: []string{"r"}}, nil, true
	}
	shared := processtest.Chain(h.PublishSignal("ping", nil), h.CompleteTasks(decide))

	var wg sync.WaitGroup
	errs := make([]error, len(ids))
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = h.DriveToCompletion(t.Context(), def, id, shared)
		}()
	}
	wg.Wait()

	for i, id := range ids {
		require.NoErrorf(t, errs[i], "instance %s", id)
	}
	assert.Equal(t, len(ids), h.Catalog().Count("notify"),
		"the arm must fire exactly once per instance, not a run-varying number of times")
}

// TestArmDerivedSignalDoesNotDemoteATokenMessageAwait is finding 5. The
// arm-derived test asked only "does any token carry AwaitSignal", so a signal ARM
// coexisting with a genuine TOKEN message await was judged arm-derived and
// promoted to ReasonTimer — advancing the shared clock and taking a timer branch
// that main leaves alone.
func TestArmDerivedSignalDoesNotDemoteATokenMessageAwait(t *testing.T) {
	t.Parallel()

	def, err := definition.NewBuilder("msg-await-beside-arm", 1).
		Add(event.NewStart("start")).
		Add(gateway.NewParallel("fork")).
		Add(activity.NewReceiveTask("recv", "PaymentReceived")).
		Add(event.NewBoundary("esc", "recv", event.WithSignalName("escalate"))).
		Add(activity.NewServiceTask("escalated", activity.WithTaskAction("notify"))).
		Add(event.NewEnd("esc-end")).
		Add(event.NewIntermediateCatch("t-catch", event.WithCatchTimer(schedule.AfterExpr(`"1h"`)))).
		Add(gateway.NewParallel("join")).
		Add(event.NewEnd("end")).
		Connect("start", "fork").
		Connect("fork", "recv").
		Connect("fork", "t-catch").
		Connect("recv", "join").
		Connect("t-catch", "join").
		Connect("join", "end").
		Connect("esc", "escalated").
		Connect("escalated", "esc-end").
		Build()
	require.NoError(t, err)

	h, err := processtest.New(noopAction("notify"))
	require.NoError(t, err)
	_, err = h.Start(t.Context(), def, "i", nil)
	require.NoError(t, err)

	before := h.Clock().Now()
	_, err = h.DriveToCompletion(t.Context(), def, "i", processtest.AutoTimers())
	require.Error(t, err, "AutoTimers alone cannot resolve a message/signal park")
	assert.Equal(t, before, h.Clock().Now(),
		"a live TOKEN message await must keep AutoTimers from firing the timer")
}

// TestSecondaryArmedTimerDoesNotPromoteHigherPriorityPark pins the other side of
// D3's widening: only an ARM-derived signal/message reason yields to the timer
// promotion. A human-task park that merely coexists with a pending timer on a
// parallel branch keeps ReasonHumanTask, so AutoTimers leaves it for the task
// handler instead of firing it to timeout — the composition AutoTimers documents.
func TestSecondaryArmedTimerDoesNotPromoteHigherPriorityPark(t *testing.T) {
	t.Parallel()

	def, err := definition.NewBuilder("task-beside-timer", 1).
		Add(event.NewStart("start")).
		Add(gateway.NewParallel("fork")).
		Add(activity.NewUserTask("approve", activity.WithEligibleRoles("r"))).
		Add(event.NewIntermediateCatch("t-catch", event.WithCatchTimer(schedule.AfterExpr(`"1h"`)))).
		Add(gateway.NewParallel("join")).
		Add(event.NewEnd("end")).
		Connect("start", "fork").
		Connect("fork", "approve").
		Connect("fork", "t-catch").
		Connect("approve", "join").
		Connect("t-catch", "join").
		Connect("join", "end").
		Build()
	require.NoError(t, err)

	h, err := processtest.New()
	require.NoError(t, err)
	_, err = h.Start(t.Context(), def, "i", nil)
	require.NoError(t, err)

	// The CLOCK is what discriminates here, not the error: a wrong promotion makes
	// AutoTimers fire the timer, after which the still-open task parks the drive
	// anyway — so both outcomes report an unhandled human-task park, and only the
	// advanced clock reveals that the timer was fired.
	before := h.Clock().Now()
	_, err = h.DriveToCompletion(t.Context(), def, "i", processtest.AutoTimers())
	require.ErrorIs(t, err, processtest.ErrUnhandledPark)
	assert.Contains(t, err.Error(), "human-task", "the park keeps its higher-priority reason")
	assert.Equal(t, before, h.Clock().Now(), "AutoTimers must not have fired the secondary timer")
}

// TestDuplicateSignalNamesCollapse covers spec row 11. SignalWaiters explicitly
// does NOT dedup (a set-based SignalBus.Sync collapses names downstream), while
// Park documents its await fields as distinct — so Classify must dedup. Here a
// boundary arm and an event-subprocess arm await ONE name.
func TestDuplicateSignalNamesCollapse(t *testing.T) {
	t.Parallel()

	inner, err := definition.NewBuilder("handle-escalate", 1).
		Add(event.NewStart("onEscalate", event.WithSignalName("escalate"))).
		Add(activity.NewServiceTask("notify-esc", activity.WithTaskAction("notify"))).
		Add(event.NewEnd("esc-inner-end")).
		Connect("onEscalate", "notify-esc").
		Connect("notify-esc", "esc-inner-end").
		Build()
	require.NoError(t, err)

	def, err := definition.NewBuilder("dup-signal", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("approve", activity.WithEligibleRoles("r"))).
		Add(event.NewEnd("end")).
		Add(event.NewBoundary("esc", "approve", event.WithSignalName("escalate"))).
		Add(activity.NewServiceTask("escalated", activity.WithTaskAction("notify"))).
		Add(event.NewEnd("esc-end")).
		AddSubProcess("handleEscalate", inner).
		Connect("start", "approve").
		Connect("approve", "end").
		Connect("esc", "escalated").
		Connect("escalated", "esc-end").
		Build()
	require.NoError(t, err)

	st := startParked(t, def)
	require.Len(t, st.SignalWaiters(), 2, "the engine authority reports both arms, undeduped")
	assert.Equal(t, []string{"escalate"}, processtest.Classify(st).AwaitingSignals)
}

// TestSameMessageNameDifferentKeysStayDistinct covers spec row 12, the other half
// of dedup: two arms awaiting one name under DIFFERENT correlation keys are
// genuinely different waiters, so collapsing on name alone would lose one. This is
// what makes the dedup key the {Name, CorrelationKey} pair.
func TestSameMessageNameDifferentKeysStayDistinct(t *testing.T) {
	t.Parallel()

	def, err := definition.NewBuilder("two-keys", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("approve", activity.WithEligibleRoles("r"))).
		Add(event.NewEnd("end")).
		Add(event.NewBoundary("c1", "approve", event.WithMessageCorrelator("Cancelled", `"order-1"`))).
		Add(event.NewBoundary("c2", "approve", event.WithMessageCorrelator("Cancelled", `"order-2"`))).
		Add(activity.NewServiceTask("cancelled", activity.WithTaskAction("notify"))).
		Add(event.NewEnd("cancel-end")).
		Connect("start", "approve").
		Connect("approve", "end").
		Connect("c1", "cancelled").
		Connect("c2", "cancelled").
		Connect("cancelled", "cancel-end").
		Build()
	require.NoError(t, err)

	st := startParked(t, def)
	assert.Equal(t, []engine.MessageWaiter{
		{Name: "Cancelled", CorrelationKey: "order-1"},
		{Name: "Cancelled", CorrelationKey: "order-2"},
	}, processtest.Classify(st).AwaitingMessages)
}

// TestOneHandlerValueDrivesTwoInstancesConcurrently covers spec row 13. The park
// arm bound is per handler VALUE, and the harness documents race-freedom, so a
// handler shared across concurrent drives must be safe under -race. It must also
// stay CORRECT: the bound is recorded per instance, so one instance's delivery
// neither suppresses nor re-authorises another's.
func TestOneHandlerValueDrivesTwoInstancesConcurrently(t *testing.T) {
	t.Parallel()

	def := signalBoundaryDef(t)
	h, err := processtest.New(noopAction("notify"))
	require.NoError(t, err)

	ids := []string{"i1", "i2"}
	for _, id := range ids {
		_, serr := h.Start(t.Context(), def, id, nil)
		require.NoError(t, serr)
	}

	shared := h.PublishSignal("escalate", nil)

	var wg sync.WaitGroup
	results := make([]engine.InstanceState, len(ids))
	errs := make([]error, len(ids))
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = h.DriveToCompletion(t.Context(), def, id, shared)
		}()
	}
	wg.Wait()

	for i, id := range ids {
		require.NoErrorf(t, errs[i], "instance %s", id)
		assert.Equalf(t, engine.StatusCompleted, results[i].Status, "instance %s", id)
	}
}

// TestArmDerivedParkNamesItsNode covers spec row 9 (D5). Classify resolves Node
// from Token.AwaitSignal, which no arm sets, so an arm-derived park would report
// Node == "" — degrading both errors a consumer actually sees. It falls back to
// the waiting token's node instead. The arm's OWN node stays unreachable: the arm
// slices have unexported element types.
func TestArmDerivedParkNamesItsNode(t *testing.T) {
	t.Parallel()

	st := startParked(t, eventGatewayArmDef(t))
	p := processtest.Classify(st)

	require.Equal(t, processtest.ReasonSignal, p.Reason, "an arm competes in the ladder like a token await")
	assert.Equal(t, "evtgw", p.Node)
}
