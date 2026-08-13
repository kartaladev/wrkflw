package processtest_test

// timer_sources_test.go — ADR-0177's consequence for the harness. Widening
// engine.InstanceState.HasArmedTimers from "a record in s.Timers" to "any of the
// five timer-arm sources" moves parks in the classification ladder, and the
// ladder is priority-ordered:
//
//	terminal > openTasks > incidents > signals > messages > timers > commandWait
//
// A park can therefore only move if EVERY rung above `timers` is empty for it,
// which leaves `commandWait` and `unknown` as the only reasons the widening can
// displace. Derived from the ladder, the shapes that flip are:
//
//  1. a plain timer intermediate catch — its token parks on the timer id, so it
//     read as commandWait before;
//  2. an event-based gateway whose arms are all timers — its token parks on an
//     "evtgw:" sentinel, so it read as commandWait too;
//  3. an arm-borne timer on an instance with no command wait at all, which read
//     as unknown.
//
// ⚠ Not "exactly one shape", which this comment claimed until `/code-review`
// measured otherwise: the widening ALSO reached a boundary or event-sub-process
// arm sitting beside an in-flight action, flipping it to ReasonTimer and firing
// it under AutoTimers. That is not in the list above because it is not a park
// the timer resolves — see TestSecondaryTimerArmDoesNotOutrankACommandWait,
// which pins the repair, and the unexported primaryTimerPark in park.go, which draws
// the line.
//
// Two test functions live here. The first pins the rungs ABOVE timers (openTasks
// and messages); the second pins the rung BELOW it (commandWait). Their fixtures
// are built differently on purpose — harness-driven for the first, engine.Step
// for the second — because a service action must stay IN FLIGHT for the second,
// which the harness's synchronous catalog cannot do. Neither can be built by
// hand: the arm slices have unexported element types, and a definition declaring
// no boundary node would assert nothing.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/processtest"
)

// plainTimerCatchDef parks on a plain timer intermediate catch and nothing else.
// The token holds the timer id in AwaitCommand, which is what made the free
// Classify report ReasonAsyncChild before Token.AwaitTimer existed.
//
//	start → t-catch[timer "1h"] → end
func plainTimerCatchDef(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	def, err := definition.NewBuilder("plain-timer-catch", 1).
		Add(event.NewStart("start")).
		Add(event.NewIntermediateCatch("t-catch", event.WithCatchTimer(schedule.AfterExpr(`"1h"`)))).
		Add(event.NewEnd("end")).
		Connect("start", "t-catch").
		Connect("t-catch", "end").
		Build()
	require.NoError(t, err)
	return def
}

// timerBoundaryOnReceiveTaskDef parks on a receive task guarded by a timer
// boundary: an armed timer coexisting with a genuine TOKEN message await.
//
//	start → recv[Receive "Cancelled"] → end
//	recv ⊸ bnd[Boundary timer "3h"] → escalated[Service notify] → bnd-end
func timerBoundaryOnReceiveTaskDef(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	def, err := definition.NewBuilder("timer-boundary-on-receive", 1).
		Add(event.NewStart("start")).
		Add(activity.NewReceiveTask("recv", "Cancelled")).
		Add(event.NewEnd("end")).
		Add(event.NewBoundary("bnd", "recv", event.WithBoundaryTimer(schedule.AfterExpr(`"3h"`)))).
		Add(activity.NewServiceTask("escalated", activity.WithTaskAction("notify"))).
		Add(event.NewEnd("bnd-end")).
		Connect("start", "recv").
		Connect("recv", "end").
		Connect("bnd", "escalated").
		Connect("escalated", "bnd-end").
		Build()
	require.NoError(t, err)
	return def
}

// timerBoundaryOnUserTaskDef parks on a user task guarded by a timer boundary:
// an armed timer coexisting with an OPEN HUMAN TASK. It is the rung the message
// case does not pin — openTasks sits two places above messages.
//
//	start → approve[User] → end
//	approve ⊸ bnd[Boundary timer "3h"] → escalated[Service notify] → bnd-end
func timerBoundaryOnUserTaskDef(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	def, err := definition.NewBuilder("timer-boundary-on-user", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("approve", activity.WithEligibleRoles("r"))).
		Add(event.NewEnd("end")).
		Add(event.NewBoundary("bnd", "approve", event.WithBoundaryTimer(schedule.AfterExpr(`"3h"`)))).
		Add(activity.NewServiceTask("escalated", activity.WithTaskAction("notify"))).
		Add(event.NewEnd("bnd-end")).
		Connect("start", "approve").
		Connect("approve", "end").
		Connect("bnd", "escalated").
		Connect("escalated", "bnd-end").
		Build()
	require.NoError(t, err)
	return def
}

// TestTimerArmReclassifiesOnlyTheLowestPark pins the rungs ABOVE timers: a
// reclassification may not reach a park an open task or an awaited message
// already holds. Every row asserts Park.HasArmedTimers is TRUE first: without
// that, the two "stays" rows would pass for a build in which the widening never
// happened at all.
//
// It says nothing about the rung BELOW timers, which is where the widening's
// regression was — see TestSecondaryTimerArmDoesNotOutrankACommandWait.
func TestTimerArmReclassifiesOnlyTheLowestPark(t *testing.T) {
	t.Parallel()

	type testCase struct {
		def    func(*testing.T) *model.ProcessDefinition
		reason processtest.Reason
		why    string
	}

	cases := map[string]testCase{
		// (a) The shape ADR-0177 set out to flip — one of the three the file
		// header enumerates, not "the one". Before ADR-0177 the plain catch's
		// timer was invisible to HasArmedTimers, so the ladder fell through to the
		// token's AwaitCommand and reported an async child — and AutoTimers(),
		// which acts only on ReasonTimer, passed forever.
		"timer arm with no task, signal or message becomes ReasonTimer": {
			def:    plainTimerCatchDef,
			reason: processtest.ReasonTimer,
			why:    "a timer park must classify as a timer park, not as an async child",
		},
		// (b) The messages rung outranks timers.
		"timer boundary beside an awaited message stays ReasonMessage": {
			def:    timerBoundaryOnReceiveTaskDef,
			reason: processtest.ReasonMessage,
			why:    "a live token message await outranks a secondary timer arm",
		},
		// (c) The openTasks rung outranks timers — and outranks messages, so (b)
		// says nothing about it.
		"timer boundary beside an open user task stays ReasonHumanTask": {
			def:    timerBoundaryOnUserTaskDef,
			reason: processtest.ReasonHumanTask,
			why:    "an open human task outranks a secondary timer arm",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			st := startParked(t, tc.def(t))
			p := processtest.Classify(st)

			require.True(t, p.HasArmedTimers,
				"control: the fixture really armed a timer the engine can see")
			assert.Equal(t, tc.reason, p.Reason, tc.why)
		})
	}
}

// ---------------------------------------------------------------------------
// The commandWait rung — the one the widening actually displaced.
// ---------------------------------------------------------------------------

// timerBoundaryOnServiceTaskDef parks on a service task whose action is still in
// flight, guarded by a timer boundary: a SECONDARY armed timer coexisting with a
// genuine token COMMAND WAIT.
//
//	start → svc[Service work] → end
//	svc ⊸ bnd[Boundary timer "3h"] → escalated[Service notify] → bnd-end
func timerBoundaryOnServiceTaskDef(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	def, err := definition.NewBuilder("timer-boundary-on-service", 1).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("svc", activity.WithTaskAction("work"))).
		Add(event.NewEnd("end")).
		Add(event.NewBoundary("bnd", "svc", event.WithBoundaryTimer(schedule.AfterExpr(`"3h"`)))).
		Add(activity.NewServiceTask("escalated", activity.WithTaskAction("notify"))).
		Add(event.NewEnd("bnd-end")).
		Connect("start", "svc").
		Connect("svc", "end").
		Connect("bnd", "escalated").
		Connect("escalated", "bnd-end").
		Build()
	require.NoError(t, err)
	return def
}

// eventSubprocessTimerArmDef parks on a service task whose action is in flight
// while a root-level TIMER-triggered event sub-process stays armed. The arm
// carries no token at all (TokenID is always ""), so it is secondary for the
// same reason a boundary arm is.
//
//	start → svc[Service work] → end
//	[event-sub "handleTimeout", no incoming flow]
//	  onTimer[start timer "5h"] → notify-esp[Service notify] → esp-end
func eventSubprocessTimerArmDef(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	inner, err := definition.NewBuilder("on-timeout", 1).
		Add(event.NewStart("onTimer", event.WithStartTimer(schedule.AfterExpr(`"5h"`)))).
		Add(activity.NewServiceTask("notify-esp", activity.WithTaskAction("notify"))).
		Add(event.NewEnd("esp-end")).
		Connect("onTimer", "notify-esp").
		Connect("notify-esp", "esp-end").
		Build()
	require.NoError(t, err)

	def, err := definition.NewBuilder("evtsub-timer-arm", 1).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("svc", activity.WithTaskAction("work"))).
		Add(event.NewEnd("end")).
		AddSubProcess("handleTimeout", inner).
		Connect("start", "svc").
		Connect("svc", "end").
		Build()
	require.NoError(t, err)
	return def
}

// eventGatewayTimerArmDef parks an event-based gateway whose ONLY arm is a
// timer. Its token holds an "evtgw:" sentinel in AwaitCommand — a command wait
// no handler can deliver — so the timer race IS the park.
//
//	start → egw ⊸ t-arm[ICE timer "1h"] → end
func eventGatewayTimerArmDef(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	def, err := definition.NewBuilder("evtgw-timer-arm", 1).
		Add(event.NewStart("start")).
		Add(gateway.NewEventBased("egw")).
		Add(event.NewIntermediateCatch("t-arm", event.WithCatchTimer(schedule.AfterExpr(`"1h"`)))).
		Add(event.NewEnd("end")).
		Connect("start", "egw").
		Connect("egw", "t-arm").
		Connect("t-arm", "end").
		Build()
	require.NoError(t, err)
	return def
}

// retryBackoffDef parks a service task token on a TimerRetry RECORD after one
// retryable failure. The record was visible to HasArmedTimers long before
// ADR-0177 widened it, so this row is a regression guard on behaviour that
// predates this delivery entirely, not a new claim.
//
//	start → svc[Service work, retry 3×] → end
func retryBackoffDef(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	def, err := definition.NewBuilder("retry-backoff", 1).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("svc",
			activity.WithTaskAction("work"),
			activity.WithRetryPolicy(&model.RetryPolicy{
				MaxAttempts:     3,
				InitialInterval: time.Minute,
				BackoffCoef:     2.0,
			}))).
		Add(event.NewEnd("end")).
		Connect("start", "svc").
		Connect("svc", "end").
		Build()
	require.NoError(t, err)
	return def
}

// stepParked drives def to its first park with [engine.Step] rather than through
// a [processtest.Harness], failing the first dispatched action when failAction is
// set (the only way to reach a retry-backoff park).
//
// The harness cannot produce these fixtures: it resolves an action through its
// catalog SYNCHRONOUSLY, so a service task either completes in the same drive or
// — measured with the action name absent from the catalog — fails the whole
// instance (status=failed, reason=terminal). engine.Step leaves the InvokeAction
// command undelivered, which is precisely the in-flight command wait a real async
// worker produces. Ids are deterministic ("i-t1", "i-c1", "i-tm1").
func stepParked(t *testing.T, def *model.ProcessDefinition, failAction bool) engine.InstanceState {
	t.Helper()
	at := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	res, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	if failAction {
		var cmdID string
		for _, c := range res.Commands {
			if ia, ok := c.(engine.InvokeAction); ok {
				cmdID = ia.CommandID
			}
		}
		require.NotEmpty(t, cmdID, "fixture: the service branch must dispatch an action to fail")
		res, err = engine.Step(t.Context(), def, res.State,
			engine.NewActionFailed(at.Add(time.Minute), cmdID, "boom", true), engine.StepOptions{})
		require.NoError(t, err)
	}

	require.False(t, processtest.IsTerminal(res.State.Status), "fixture must park, not terminate")
	return res.State
}

// TestSecondaryTimerArmDoesNotOutrankACommandWait pins the commandWait rung
// against [AutoTimers]' documented contract: "a park that merely has a
// *secondary* armed timer … is left for the task handler rather than fired to its
// timeout" (see AutoTimers' doc comment). ADR-0177's widening broke that for two
// shapes; the rule that repairs it must not swallow the three shapes for which an
// armed timer IS the park.
//
// Every row asserts BOTH the classification and the [AutoTimers] decision it
// drives — a Reason nobody acts on is not the thing the contract is about — and
// every row's fixture holds a command wait, so no row can pass by the rungs being
// trivially distinguishable.
//
// Measured on these exact fixtures before the fix (instance "i"), which is what
// makes rows (a) and (b) fail today and rows (c)–(e) regression guards:
//
//	(a) svc ⊸ bnd[timer]   reason=timer       ← wrong, AutoTimers fired the boundary
//	(b) evtsub timer arm   reason=timer       ← wrong, same shape without a token
//	(c) plain ICE          reason=timer       ← right
//	(d) egw ⊸ timer arm    reason=timer       ← right (async-child before ADR-0177)
//	(e) retry backoff      reason=timer       ← right, and true since long before ADR-0177
//
// ⚠ Rows (c)–(e) are not decoration. Each of their tokens parks on a command too
// (the timer id, the timer id, and an "evtgw:" sentinel), so a predicate that
// simply lets any command wait win reddens all three; the AwaitTimer-based
// predicate this fix was first drafted with reddens (d) and (e).
func TestSecondaryTimerArmDoesNotOutrankACommandWait(t *testing.T) {
	t.Parallel()

	type testCase struct {
		def        func(*testing.T) *model.ProcessDefinition
		failAction bool
		assert     func(t *testing.T, p processtest.Park, decision processtest.Decision)
	}

	secondary := func(t *testing.T, p processtest.Park, decision processtest.Decision) {
		assert.Equal(t, processtest.ReasonAsyncChild, p.Reason,
			"a live command wait outranks a timer arm the instance is not waiting ON")
		assert.Equal(t, processtest.Pass(), decision,
			"AutoTimers must leave this park to the action handler, not fire the arm")
	}
	primary := func(t *testing.T, p processtest.Park, decision processtest.Decision) {
		assert.Equal(t, processtest.ReasonTimer, p.Reason,
			"a timer the instance is genuinely waiting ON is a timer park")
		assert.Equal(t, processtest.AdvanceTimers(), decision,
			"AutoTimers must still resolve a real timer park")
	}

	cases := map[string]testCase{
		// (a) The finding: a boundary arm beside an in-flight action.
		"timer boundary beside an in-flight action stays ReasonAsyncChild": {
			def:    timerBoundaryOnServiceTaskDef,
			assert: secondary,
		},
		// (b) The same shape with no token on the arm at all.
		"event-subprocess timer arm beside an in-flight action stays ReasonAsyncChild": {
			def:    eventSubprocessTimerArmDef,
			assert: secondary,
		},
		// (c) The shape ADR-0177 set out to fix.
		"plain timer catch stays ReasonTimer": {
			def:    plainTimerCatchDef,
			assert: primary,
		},
		// (d) The other shape ADR-0177 fixed: the gateway's sentinel command wait
		// is undeliverable, so the timer race is the only way out.
		"event-gateway timer arm stays ReasonTimer": {
			def:    eventGatewayTimerArmDef,
			assert: primary,
		},
		// (e) Older than this delivery: a retry backoff record.
		"retry backoff stays ReasonTimer": {
			def:        retryBackoffDef,
			failAction: true,
			assert:     primary,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			st := stepParked(t, tc.def(t), tc.failAction)
			p := processtest.Classify(st)

			require.True(t, p.HasArmedTimers,
				"control: the fixture really armed a timer the engine can see — without "+
					"this, the two secondary rows would pass on a build where the widening "+
					"never happened at all")
			require.True(t, hasCommandWait(st),
				"control: EVERY fixture parks a token on a command, so no row can pass "+
					"by the two rungs being trivially distinguishable")

			decision, err := processtest.AutoTimers()(t.Context(), p)
			require.NoError(t, err)

			tc.assert(t, p, decision)
		})
	}
}

// hasCommandWait mirrors the classifier's own command-wait predicate so each row
// can assert its fixture really holds one.
func hasCommandWait(st engine.InstanceState) bool {
	for _, tok := range st.Tokens {
		if tok.State == engine.TokenWaiting && tok.AwaitCommand != "" {
			return true
		}
	}
	return false
}
