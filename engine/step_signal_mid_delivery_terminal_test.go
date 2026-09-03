package engine_test

// step_signal_mid_delivery_terminal_test.go: a signal delivery stops
// dispatching once its own drive has turned the instance terminal.
//
// handleSignalReceived is the only handler with more than one arm-dispatch point
// per delivery (three arm families plus one per snapshotted parked token), so it
// is the only place where an instance can be `running` when dispatch admits the
// trigger and terminal a few statements later. The entry guard cannot see
// that transition: it happens BETWEEN tiers.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
)

var midDeliveryT0 = time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)

// midDeliveryTerminalDef builds the headline reproduction: one signal
// name that both fires an interrupting boundary whose path dies with an uncaught
// error AND resumes a parked catch token on a sibling branch.
//
//	start → fork(parallel) ⇒
//	  A: taskA(user) [bndA interrupting, signal "x"] → errEnd(EndError "boom", uncaught)
//	  B: catchB(intermediate catch, signal "x") → taskB(user) [bndT timer 60m] → endB
//
// Branch declaration order is load-bearing: the boundary arm (tier 2) must fire
// before tier 4 walks the snapshot, so branch A's flow is declared first.
func midDeliveryTerminalDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-mid-delivery-terminal", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewUserTask("taskA"),
			event.NewBoundary("bndA", "taskA", event.WithSignalName("x")),
			event.NewEnd("errEnd", event.WithErrorCode("boom")),
			event.NewEnd("endA"),
			event.NewIntermediateCatch("catchB", event.WithSignalName("x")),
			activity.NewUserTask("taskB"),
			event.NewBoundary("bndT", "taskB", event.WithBoundaryTimer(schedule.AfterDuration(60*time.Minute))),
			event.NewEnd("endT"),
			event.NewEnd("endB"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start-fork", Source: "start", Target: "fork"},
			{ID: "f-fork-a", Source: "fork", Target: "taskA"},
			{ID: "f-taska-enda", Source: "taskA", Target: "endA"},
			{ID: "f-bnda-err", Source: "bndA", Target: "errEnd"},
			{ID: "f-fork-b", Source: "fork", Target: "catchB"},
			{ID: "f-catchb-taskb", Source: "catchB", Target: "taskB"},
			{ID: "f-taskb-endb", Source: "taskB", Target: "endB"},
			{ID: "f-bndt-endt", Source: "bndT", Target: "endT"},
		},
	}
}

// deliverMidDeliveryTerminalSignal starts midDeliveryTerminalDef and delivers
// the single SignalReceived{"x"} that both fires bndA (whose path dies with an
// uncaught error) and would resume catchB.
//
// Every require here is a fixture control, not an assertion about the fix: if
// one of them stops holding, the tests below stop exercising the scenario they
// name. In particular bndT must NOT be armed at delivery time — it is armed only
// if catchB's token is (wrongly) driven into taskB.
func deliverMidDeliveryTerminalSignal(t *testing.T) engine.StepResult {
	t.Helper()

	def := midDeliveryTerminalDef()
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(midDeliveryT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.Tokens, 2, "fixture control: both branches must be parked")
	require.Equal(t, "taskA", r1.State.Tokens[0].NodeID, "fixture control: branch A parks first")
	require.Equal(t, "catchB", r1.State.Tokens[1].NodeID, "fixture control: branch B parks on the catch")
	require.Equal(t, "x", r1.State.Tokens[1].AwaitSignal,
		"fixture control: catchB's token must await the delivered signal")
	require.Len(t, r1.State.Boundaries, 1,
		"fixture control: only bndA is armed — bndT arms only if catchB is (wrongly) resumed")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(midDeliveryT0.Add(time.Second), "x", nil), engine.StepOptions{})
	require.NoError(t, err)
	require.True(t, r2.State.Status.IsTerminal(),
		"control: the delivery's own drive must have made the instance terminal")
	return r2
}

// TestSignalDeliveryStopsAtMidDeliveryTerminal pins the headline behaviour:
// once tier 2's drive has failed the instance, tier 4 must not resume the
// sibling catch token.
//
// Measured on unpatched main: the surviving token is driven to
// taskB, s.Boundaries carries bndT on a FAILED instance, taskB is recorded in
// History, and a live ScheduleTimer{Token:i1-t3} escapes to the runtime — the
// last of these is deliberately exempt from the stale-command filter, so
// nothing contains it.
//
// It deliberately asserts WHERE the surviving token is and WHAT it did not do,
// never len(Tokens). Tokens are kept on the failFast branch, so
// the count is 1 before this fix and 1 after: an assertion on it could never
// pass. This test cannot discriminate the guard's PLACEMENT (ahead of the tier-4
// loop vs inside it) — that is TestSignalDeliveryStopsInsideTheTokenLoop's job.
func TestSignalDeliveryStopsAtMidDeliveryTerminal(t *testing.T) {
	t.Parallel()

	r := deliverMidDeliveryTerminalSignal(t)

	for _, c := range r.Commands {
		_, isTimer := c.(engine.ScheduleTimer)
		require.False(t, isTimer, "no ScheduleTimer may escape for a dead instance: %#v", c)
	}
	for _, tok := range r.State.Tokens {
		require.NotEqual(t, "taskB", tok.NodeID,
			"no token may be driven into taskB on a dead instance")
	}
	for _, v := range r.State.History {
		require.NotEqual(t, "taskB", v.NodeID, "taskB must never be visited on a dead instance")
	}
	require.Empty(t, r.State.Boundaries, "no boundary arm may survive on a dead instance")

	var parked int
	for _, tok := range r.State.Tokens {
		if tok.NodeID == "catchB" && tok.AwaitSignal == "x" {
			parked++
		}
	}
	require.Equal(t, 1, parked,
		"the surviving token stays parked at catchB, still awaiting the signal it never consumed")
}

// TestAbortedSignalDeliveryKeepsItsPartialCommands pins that an
// aborted delivery returns the partial commands the earlier tiers legitimately
// produced — including the FailInstance that made the instance terminal — and
// nothing after it.
//
// The two clauses do different jobs, and only the second can fail today:
//
//   - "FailInstance is emitted" is a PIN. It holds on unpatched main too.
//     It exists to falsify the rejected alternative
//     StepResult{State: *s} with nil commands, under which the terminal event's
//     error payload degrades to "instance failed" and the earlier tiers'
//     UpdateTask reconciliation is lost.
//   - "no command follows it" is the RED. Today ScheduleTimer is last.
//
// The "no command follows" clause is scoped to THIS fixture: it is not a general
// invariant that FailInstance is always the final command of any step.
func TestAbortedSignalDeliveryKeepsItsPartialCommands(t *testing.T) {
	t.Parallel()

	r := deliverMidDeliveryTerminalSignal(t)

	last := -1
	for i, c := range r.Commands {
		if fi, ok := c.(engine.FailInstance); ok {
			require.Equal(t, "boom", fi.Err, "the terminal event must carry the uncaught error code")
			last = i
		}
	}
	require.NotEqual(t, -1, last, "the FailInstance that killed the instance must be kept")
	require.Len(t, r.Commands, last+1,
		"for this fixture no command may follow FailInstance: %#v", r.Commands)
}

// twoCatchTokensDef builds the intra-loop reproduction: TWO tokens awaiting the
// same signal, the first of which drives into an uncaught error.
//
//	start → fork(parallel) ⇒
//	  A: catch1(catch signal "y") → errEnd(EndError "boom", uncaught)
//	  B: catch2(catch signal "y") → taskB(user) [bndT timer 60m] → endB
//
// It needs no boundary arm at delivery time — the whole delivery happens inside
// tier 4 — which is what makes it a clean falsifier of the guard's PLACEMENT.
// Branch declaration order is load-bearing: tier 4 walks the snapshot in slice
// order, so catch1 must be first or the erroring branch runs last and there is
// nothing left to wrongly resume.
func twoCatchTokensDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-two-catch-tokens", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			event.NewIntermediateCatch("catch1", event.WithSignalName("y")),
			event.NewEnd("errEnd", event.WithErrorCode("boom")),
			event.NewIntermediateCatch("catch2", event.WithSignalName("y")),
			activity.NewUserTask("taskB"),
			event.NewBoundary("bndT", "taskB", event.WithBoundaryTimer(schedule.AfterDuration(60*time.Minute))),
			event.NewEnd("endT"),
			event.NewEnd("endB"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start-fork", Source: "start", Target: "fork"},
			{ID: "f-fork-1", Source: "fork", Target: "catch1"},
			{ID: "f-catch1-err", Source: "catch1", Target: "errEnd"},
			{ID: "f-fork-2", Source: "fork", Target: "catch2"},
			{ID: "f-catch2-task", Source: "catch2", Target: "taskB"},
			{ID: "f-task-end", Source: "taskB", Target: "endB"},
			{ID: "f-bndt-endt", Source: "bndT", Target: "endT"},
		},
	}
}

// TestSignalDeliveryStopsInsideTheTokenLoop pins that the
// re-check belongs INSIDE tier 4's token loop, not only ahead of it.
//
// This is the only test in the bundle that discriminates the guard's placement,
// and the measurement was reproduced here rather than inherited: RED on
// unpatched main (a live ScheduleTimer{i1-tm1 Token:i1-t3} escaped), RED again
// with the guard hoisted to sit only AHEAD of the tier-4 loop (same escape),
// GREEN only with the guard inside the loop. Under that same hoisted mutation
// TestSignalDeliveryStopsAtMidDeliveryTerminal and
// TestAbortedSignalDeliveryKeepsItsPartialCommands both still PASS, so neither
// proves anything about placement.
func TestSignalDeliveryStopsInsideTheTokenLoop(t *testing.T) {
	t.Parallel()

	def := twoCatchTokensDef()
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(midDeliveryT0, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Fixture control. snapshotIDs is unexported; its observable equivalent is
	// exactly the set of tokens whose AwaitSignal is the delivered name.
	var awaiting []string
	for _, tok := range r1.State.Tokens {
		if tok.AwaitSignal == "y" {
			awaiting = append(awaiting, tok.NodeID)
		}
	}
	require.Equal(t, []string{"catch1", "catch2"}, awaiting,
		"fixture control: exactly two tokens must await 'y' at delivery time, catch1 first "+
			"(this is len(snapshotIDs) == 2 observed from the outside)")
	require.Empty(t, r1.State.Boundaries,
		"fixture control: no boundary arm exists yet — bndT arms only if catch2 is (wrongly) resumed")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(midDeliveryT0.Add(time.Second), "y", nil), engine.StepOptions{})
	require.NoError(t, err)
	require.True(t, r2.State.Status.IsTerminal(),
		"control: catch1's drive must have made the instance terminal")

	var fails int
	for _, c := range r2.Commands {
		if _, ok := c.(engine.FailInstance); ok {
			fails++
		}
		_, isTimer := c.(engine.ScheduleTimer)
		require.False(t, isTimer, "no ScheduleTimer may be armed by the second token: %#v", c)
		_, isAwait := c.(engine.AwaitHuman)
		require.False(t, isAwait, "no AwaitHuman may be minted by the second token: %#v", c)
	}
	require.Equal(t, 1, fails, "control: the FailInstance that killed the instance must be kept")
	require.Empty(t, r2.State.Boundaries, "no boundary arm may be recorded on a dead instance")
	for _, tok := range r2.State.Tokens {
		require.NotEqual(t, "taskB", tok.NodeID, "the second token must not be driven into taskB")
	}
	for _, v := range r2.State.History {
		require.NotEqual(t, "taskB", v.NodeID, "taskB must never be visited on a dead instance")
	}

	var stillAwaiting int
	for _, tok := range r2.State.Tokens {
		if tok.NodeID == "catch2" && tok.AwaitSignal == "y" {
			stillAwaiting++
		}
	}
	require.Equal(t, 1, stillAwaiting,
		"catch2's token stays parked, untouched by the aborted delivery")
}

// gatewayWinKillsInstanceDef matches ONE signal name against all three arm
// families while the gateway's own branch dies on an uncaught error:
//
//	start → fork(parallel) ⇒
//	  A: gw(event-based) → catchZ(catch signal "z") → errEnd(EndError "boom", uncaught)
//	  B: taskB(user) [bndZ interrupting, signal "z"] → endB ; bndZ → svcB(b-action) → endBnd
//	[root event sub-process "esp", NON-interrupting: esp-start(signal "z") → svcE(esp-action) → esp-end]
//
// All three arms match one delivery — the INTENTIONAL BROADCAST that
// handleSignalReceived documents — so tier 1 fires first and kills the instance
// while tiers 2 and 3 still have arms to find.
func gatewayWinKillsInstanceDef() *model.ProcessDefinition {
	espBody := &model.ProcessDefinition{
		ID: "esp-body-gateway-win", Version: 1,
		Nodes: []model.Node{
			event.NewStart("esp-start", event.WithSignalName("z"), event.WithNonInterrupting()),
			activity.NewServiceTask("svcE", activity.WithTaskAction("esp-action")),
			event.NewEnd("esp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "ef1", Source: "esp-start", Target: "svcE"},
			{ID: "ef2", Source: "svcE", Target: "esp-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p-gateway-win-kills", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			gateway.NewEventBased("gw"),
			event.NewIntermediateCatch("catchZ", event.WithSignalName("z")),
			event.NewEnd("errEnd", event.WithErrorCode("boom")),
			activity.NewUserTask("taskB"),
			event.NewBoundary("bndZ", "taskB", event.WithSignalName("z")),
			activity.NewServiceTask("svcB", activity.WithTaskAction("b-action")),
			event.NewEnd("endB"),
			event.NewEnd("endBnd"),
			activity.NewSubProcess("esp", espBody),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start-fork", Source: "start", Target: "fork"},
			{ID: "f-fork-gw", Source: "fork", Target: "gw"},
			{ID: "f-gw-catch", Source: "gw", Target: "catchZ"},
			{ID: "f-catch-err", Source: "catchZ", Target: "errEnd"},
			{ID: "f-fork-b", Source: "fork", Target: "taskB"},
			{ID: "f-taskb-endb", Source: "taskB", Target: "endB"},
			{ID: "f-bndz-svcb", Source: "bndZ", Target: "svcB"},
			{ID: "f-svcb-end", Source: "svcB", Target: "endBnd"},
		},
	}
}

// TestNoArmFiresAfterAMidDeliveryTerminal covers the guard site shared by tiers
// 1–3: a gateway win whose own drive kills the instance must not be followed by
// a boundary arm or an event sub-process arm firing on the same signal.
//
// ⚠ It is a PIN, not a RED, and it does NOT discriminate the guard. It was
// prescribed as a test that "fails today"; EXECUTED, it does not,
// and no fixture can make it. Both terminal routes reachable from tier 1 were
// measured on this fixture, guarded tree vs unpatched main, and the output is
// byte-identical:
//
//	uncaught error    both trees: status=failed     boundaries=0 esp=0
//	                              history=[start fork gw taskB errEnd]
//	                              cmds=[UpdateTask{i1-h1 cancelled}, FailInstance{boom}]
//	force-termination both trees: status=terminated boundaries=0 esp=0
//	                              cmds=[UpdateTask{i1-h1 cancelled}, FailInstance{gateway-terminates}]
//
// The reason is that every terminal transition routes through endInstance, whose
// cancelAllScheduledWork drains ArmedEvents, Boundaries AND the event
// sub-process arms across all scopes — so by the time tiers 2 and 3
// run their lookups there is nothing left to find. The guard ahead of those two
// tiers is therefore defence in depth, not a fix for an observable defect; the
// only observable exposure is tier 4's loop, which owns no arm state and is
// covered by TestSignalDeliveryStopsInsideTheTokenLoop.
//
// It is kept because the property is real and the two mechanisms are
// independent: if a later delivery narrows that drain — the backlog's ESP-hole
// ADR is a live candidate, since it concerns arms surviving a teardown — this
// test is what notices that the arms then start firing on a dead instance.
func TestNoArmFiresAfterAMidDeliveryTerminal(t *testing.T) {
	t.Parallel()

	def := gatewayWinKillsInstanceDef()
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(midDeliveryT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.ArmedEvents, 1, "fixture control: the gateway arm must be armed")
	require.Len(t, r1.State.Boundaries, 1, "fixture control: the boundary arm must be armed")
	require.Len(t, r1.State.EventTriggeredSubprocesses, 1,
		"fixture control: the event sub-process arm must be armed")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(midDeliveryT0.Add(time.Second), "z", nil), engine.StepOptions{})
	require.NoError(t, err)
	require.True(t, r2.State.Status.IsTerminal(),
		"control: the gateway branch's drive must have made the instance terminal")

	for _, v := range r2.State.History {
		require.NotEqual(t, "svcB", v.NodeID, "the boundary path must not be visited on a dead instance")
		require.NotEqual(t, "svcE", v.NodeID,
			"the event sub-process body must not be visited on a dead instance")
	}
	require.Empty(t, r2.State.Scopes,
		"no event sub-process scope may be spawned on a dead instance")
	for _, c := range r2.Commands {
		if ia, ok := c.(engine.InvokeAction); ok {
			require.NotContains(t, []string{"b-action", "esp-action"}, ia.Name,
				"no arm dispatched after the instance died may reach an action: %#v", ia)
		}
	}
	var parked int
	for _, tok := range r2.State.Tokens {
		if tok.NodeID == "taskB" {
			parked++
		}
	}
	require.Equal(t, 1, parked,
		"taskB's token survives the failFast branch, still parked at taskB")
}

// allThreeArmFamiliesDef matches ONE signal name against all three arm families
// at once — an event-gateway arm, a boundary arm and an event sub-process arm:
//
//	start → fork(parallel) ⇒
//	  A: gw(event-based) → catchZ(catch signal "z") → svcG(gw-action) → endG
//	  B: taskB(user) [bndZ NON-interrupting, signal "z"] → endB ; bndZ → svcBnd(bnd-action) → endBnd
//	[root event sub-process "esp", NON-interrupting: esp-start(signal "z") → svcE(esp-action) → esp-end]
//
// handleSignalReceived's own "INTENTIONAL BROADCAST" comment states all three
// can match simultaneously, so this is legitimate BPMN rather than a contrivance.
// Both the boundary and the event sub-process are NON-interrupting on purpose:
// an interrupting one would cancel tokens whose InvokeAction commands
// dropStaleTokenCommands then removes, which would confuse an assertion about
// command ORDER with one about command survival.
func allThreeArmFamiliesDef() *model.ProcessDefinition {
	espBody := &model.ProcessDefinition{
		ID: "esp-body-tier-order", Version: 1,
		Nodes: []model.Node{
			event.NewStart("esp-start", event.WithSignalName("z"), event.WithNonInterrupting()),
			activity.NewServiceTask("svcE", activity.WithTaskAction("esp-action")),
			event.NewEnd("esp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "ef1", Source: "esp-start", Target: "svcE"},
			{ID: "ef2", Source: "svcE", Target: "esp-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p-all-three-arm-families", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			gateway.NewEventBased("gw"),
			event.NewIntermediateCatch("catchZ", event.WithSignalName("z")),
			activity.NewServiceTask("svcG", activity.WithTaskAction("gw-action")),
			event.NewEnd("endG"),
			activity.NewUserTask("taskB"),
			event.NewBoundary("bndZ", "taskB",
				event.WithSignalName("z"), event.WithBoundaryNonInterrupting()),
			activity.NewServiceTask("svcBnd", activity.WithTaskAction("bnd-action")),
			event.NewEnd("endB"),
			event.NewEnd("endBnd"),
			activity.NewSubProcess("esp", espBody),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start-fork", Source: "start", Target: "fork"},
			{ID: "f-fork-gw", Source: "fork", Target: "gw"},
			{ID: "f-gw-catch", Source: "gw", Target: "catchZ"},
			{ID: "f-catch-svcg", Source: "catchZ", Target: "svcG"},
			{ID: "f-svcg-end", Source: "svcG", Target: "endG"},
			{ID: "f-fork-b", Source: "fork", Target: "taskB"},
			{ID: "f-taskb-endb", Source: "taskB", Target: "endB"},
			{ID: "f-bndz-svcbnd", Source: "bndZ", Target: "svcBnd"},
			{ID: "f-svcbnd-end", Source: "svcBnd", Target: "endBnd"},
		},
	}
}

// TestSignalDispatchTierOrderIsGatewayBoundaryEventSubprocess pins
// handleSignalReceived's dispatch order: event-gateway arm, then boundary arm,
// then event sub-process arm.
//
// It is a PIN, not a RED: the order is correct today. It exists because nothing
// else in the repo protects it — measured during the bundle's audit against the
// existing suite, swapping tier 2 with tier 3 left `go test ./engine/` at
// EXIT=0, and so did swapping tier 1 with tier 2 (DELETING a tier was caught).
// The three tiers are folded into a closure slice, where a reorder is a
// one-line edit — exactly the mutation a slice literal invites and the one the
// suite could not see.
//
// This test closes that hole, and both swaps were re-run against it here:
//
//	swap tier 2 ↔ 3: got [gw-action esp-action bnd-action] — RED
//	swap tier 1 ↔ 2: got [bnd-action gw-action esp-action] — RED
func TestSignalDispatchTierOrderIsGatewayBoundaryEventSubprocess(t *testing.T) {
	t.Parallel()

	def := allThreeArmFamiliesDef()
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(midDeliveryT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.ArmedEvents, 1, "fixture control: the gateway arm must be armed")
	require.Len(t, r1.State.Boundaries, 1, "fixture control: the boundary arm must be armed")
	require.Len(t, r1.State.EventTriggeredSubprocesses, 1,
		"fixture control: the event sub-process arm must be armed")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(midDeliveryT0.Add(time.Second), "z", nil), engine.StepOptions{})
	require.NoError(t, err)
	require.False(t, r2.State.Status.IsTerminal(),
		"fixture control: this delivery must NOT be an aborted one — it exists to pin ORDER")

	var fired []string
	for _, c := range r2.Commands {
		if ia, ok := c.(engine.InvokeAction); ok {
			fired = append(fired, ia.Name)
		}
	}
	require.Equal(t, []string{"gw-action", "bnd-action", "esp-action"}, fired,
		"dispatch order must stay gateway → boundary → event sub-process")
}
