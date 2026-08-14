package engine_test

// state_timer_waiters_test.go — ADR-0177. InstanceState.TimerWaiters() is the
// single authority enumerating EVERY timer arm an instance holds: the token
// await (Token.AwaitTimer), the three arm families, and the s.Timers record
// table. Before it existed, HasArmedTimers() read s.Timers alone and reported
// "no armed timers" for four of the five sources.
//
// Every fixture here is DRIVEN through engine.Step rather than hand-built: the
// arm slices (Boundaries, ArmedEvents, EventTriggeredSubprocesses) have
// unexported element types, so an arm park cannot be constructed by hand — and
// a fixture whose definition declares no arm node would assert nothing.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
)

// timerArmFixtureAt is the instant every fixture in this file starts at.
var timerArmFixtureAt = time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)

// plainCatchTimerDef parks a token on a PLAIN intermediate catch timer — the
// source that lives on no arm slice and no timer record at all.
//
//	start → wait[timer "1h"] → hold[UserTask] → end
//
// The trailing user task keeps the token alive after the timer fires, which is
// what makes the AwaitTimer CLEAR observable (see TestTokenAwaitTimerIsClearedOnResume).
func plainCatchTimerDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-tw-plain-catch", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewIntermediateCatch("wait", event.WithCatchTimer(schedule.AfterExpr(`"1h"`))),
			activity.NewUserTask("hold"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait"},
			{ID: "f2", Source: "wait", Target: "hold"},
			{ID: "f3", Source: "hold", Target: "end"},
		},
	}
}

// boundaryTimerArmDef parks a token on a user task guarded by an interrupting
// TIMER boundary. The arm lives in s.Boundaries; nothing on the token names it.
//
//	start → work[UserTask] → end
//	work ⊸ bnd-timer[Boundary timer "3h"] → escalate[Service] → end2
func boundaryTimerArmDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-tw-boundary", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("work"),
			event.NewBoundary("bnd-timer", "work", event.WithBoundaryTimer(schedule.AfterExpr(`"3h"`))),
			activity.NewServiceTask("escalate", activity.WithTaskAction("escalate-action")),
			event.NewEnd("end"),
			event.NewEnd("end2"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "work"},
			{ID: "f-work-end", Source: "work", Target: "end"},
			{ID: "f-bnd", Source: "bnd-timer", Target: "escalate"},
			{ID: "f-escalate-end", Source: "escalate", Target: "end2"},
		},
	}
}

// gatewayTimerArmDef parks a token on an event-based gateway racing a TIMER arm
// against a SIGNAL arm. The timer arm lives in s.ArmedEvents; the signal arm
// must contribute nothing, so the case cannot pass by counting arms.
//
//	start → evtgw → tcatch[timer "1h"]        → end1
//	              → scatch[signal "approved"] → end2
func gatewayTimerArmDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-tw-evtgw", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewEventBased("evtgw"),
			event.NewIntermediateCatch("tcatch", event.WithCatchTimer(schedule.AfterExpr(`"1h"`))),
			event.NewIntermediateCatch("scatch", event.WithSignalName("approved")),
			event.NewEnd("end1"),
			event.NewEnd("end2"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "evtgw"},
			{ID: "f-gw-timer", Source: "evtgw", Target: "tcatch"},
			{ID: "f-gw-signal", Source: "evtgw", Target: "scatch"},
			{ID: "f-timer-end", Source: "tcatch", Target: "end1"},
			{ID: "f-signal-end", Source: "scatch", Target: "end2"},
		},
	}
}

// eventSubprocessTimerArmDef arms a root-level TIMER-triggered event
// sub-process while the main branch parks on a user task. The arm carries no
// token at all — it lives only in s.EventTriggeredSubprocesses.
//
//	start → work[UserTask] → end
//	[event-sub "root-esp", no incoming flow]
//	  esp-start[start timer "1h"] → esp-svc[Service] → esp-end
func eventSubprocessTimerArmDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "p-tw-esp-inner", Version: 1,
		Nodes: []model.Node{
			event.NewStart("esp-start", event.WithStartTimer(schedule.AfterExpr(`"1h"`))),
			activity.NewServiceTask("esp-svc", activity.WithTaskAction("esp-action")),
			event.NewEnd("esp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "e1", Source: "esp-start", Target: "esp-svc"},
			{ID: "e2", Source: "esp-svc", Target: "esp-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p-tw-esp", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("work"),
			event.NewEnd("end"),
			activity.NewSubProcess("root-esp", inner),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "work"},
			{ID: "f-work-end", Source: "work", Target: "end"},
		},
	}
}

// deadlineRecordDef is the CONTROL source: a user-task deadline, the one arm
// kind that already lands in s.Timers and that HasArmedTimers() saw before
// ADR-0177. Without it the other four rows could pass by everything being empty.
//
//	start → work[UserTask, deadline "3h" → flow "escalate"] → end
func deadlineRecordDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-tw-deadline", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("work", activity.WithWaitDeadline(schedule.AfterExpr(`"3h"`), "escalate")),
			event.NewEnd("end"),
			event.NewEnd("escalate-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "work"},
			{ID: "f-work-end", Source: "work", Target: "end"},
			{ID: "escalate", Source: "work", Target: "escalate-end"},
		},
	}
}

// startParkedState starts def and returns the parked snapshot, asserting the
// fixture really parked (a terminated instance holds no arms and would make
// every assertion below vacuous).
func startParkedState(t *testing.T, def *model.ProcessDefinition) engine.InstanceState {
	t.Helper()
	res, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(timerArmFixtureAt, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.False(t, res.State.Status.IsTerminal(), "fixture must park, not terminate")
	return res.State
}

// TestTimerWaiters covers ADR-0177's five timer-arm sources, one per row. Each
// row's definition declares a REAL arm node, and each asserts the whole
// TimerWaiter — TimerID, Kind, NodeID and TokenID — so a row cannot pass on a
// half-populated result.
func TestTimerWaiters(t *testing.T) {
	t.Parallel()

	type testCase struct {
		def    *model.ProcessDefinition
		assert func(t *testing.T, st engine.InstanceState, got []engine.TimerWaiter)
	}

	cases := map[string]testCase{
		"token await — plain intermediate catch timer": {
			def: plainCatchTimerDef(),
			assert: func(t *testing.T, st engine.InstanceState, got []engine.TimerWaiter) {
				require.Len(t, st.Tokens, 1)
				require.Equal(t, "wait", st.Tokens[0].NodeID, "fixture must park ON the catch node")
				require.Len(t, got, 1)
				assert.Equal(t, engine.TimerWaiter{
					TimerID: st.Tokens[0].AwaitCommand,
					Kind:    engine.TimerIntermediate,
					NodeID:  "wait",
					TokenID: st.Tokens[0].ID,
				}, got[0])
			},
		},
		"boundary arm — interrupting timer boundary on a user task": {
			def: boundaryTimerArmDef(),
			assert: func(t *testing.T, st engine.InstanceState, got []engine.TimerWaiter) {
				require.Len(t, st.Tokens, 1)
				require.Len(t, got, 1)
				assert.NotEmpty(t, got[0].TimerID)
				assert.Equal(t, engine.TimerIntermediate, got[0].Kind)
				assert.Equal(t, "bnd-timer", got[0].NodeID, "the BOUNDARY node owns the arm, not its host")
				assert.Equal(t, st.Tokens[0].ID, got[0].TokenID, "the parked host token")
			},
		},
		"event-gateway arm — timer arm racing a signal arm": {
			def: gatewayTimerArmDef(),
			assert: func(t *testing.T, st engine.InstanceState, got []engine.TimerWaiter) {
				require.Len(t, st.Tokens, 1)
				require.Len(t, got, 1, "the SIGNAL arm must contribute nothing")
				assert.NotEmpty(t, got[0].TimerID)
				assert.Equal(t, engine.TimerIntermediate, got[0].Kind)
				assert.Equal(t, "tcatch", got[0].NodeID)
				assert.Equal(t, st.Tokens[0].ID, got[0].TokenID, "the parked gateway token")
			},
		},
		"event-subprocess arm — root timer-triggered start": {
			def: eventSubprocessTimerArmDef(),
			assert: func(t *testing.T, st engine.InstanceState, got []engine.TimerWaiter) {
				require.Len(t, got, 1)
				assert.NotEmpty(t, got[0].TimerID)
				assert.Equal(t, engine.TimerIntermediate, got[0].Kind)
				assert.Equal(t, "root-esp", got[0].NodeID)
				assert.Empty(t, got[0].TokenID, "an event-sub arm is keyed to a SCOPE, not a token")
			},
		},
		"timer record — CONTROL: a user-task deadline in s.Timers": {
			def: deadlineRecordDef(),
			assert: func(t *testing.T, st engine.InstanceState, got []engine.TimerWaiter) {
				require.Len(t, st.Tokens, 1)
				records := engine.TimerRecords(&st)
				require.Len(t, records, 1, "control: the arm really is in s.Timers")
				require.Len(t, got, 1)
				assert.Equal(t, engine.TimerWaiter{
					TimerID: records[0].TimerID,
					Kind:    engine.TimerDeadline,
					NodeID:  "work",
					TokenID: st.Tokens[0].ID,
				}, got[0])
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			st := startParkedState(t, tc.def)
			tc.assert(t, st, st.TimerWaiters())
		})
	}
}

// TestTokenAwaitTimerIsSetOnPlainIntermediateCatch pins the field that gives the
// fifth source a home. A plain intermediate-catch timer parks on the OVERLOADED
// AwaitCommand, measured holding human-task ids, event-gateway sentinels, timer
// ids and "" across fixtures — so the source is not identifiable from state
// alone without a dedicated marker.
//
// Fails before Token.AwaitTimer exists: the field is undefined (compile error).
// The dual-write is asserted too — AwaitCommand must keep its current value, or
// handleTimerFired's path-5 fall-through stops routing the fire.
func TestTokenAwaitTimerIsSetOnPlainIntermediateCatch(t *testing.T) {
	t.Parallel()

	st := startParkedState(t, plainCatchTimerDef())
	require.Len(t, st.Tokens, 1)
	tok := st.Tokens[0]
	require.Equal(t, "wait", tok.NodeID, "fixture must park ON the catch node")

	assert.NotEmpty(t, tok.AwaitTimer, "the plain intermediate catch must mark its timer")
	assert.Equal(t, tok.AwaitCommand, tok.AwaitTimer,
		"dual-write: AwaitCommand keeps the timer id so path-5 dispatch is untouched")
}

// TestTokenAwaitTimerIsClearedOnResume is the audit's CRITICAL case, and the one
// that decides whether ADR-0177 works or inverts.
//
// A set-only AwaitTimer stays populated after the timer fires, so the resumed
// token keeps naming a timer that no longer exists: TimerWaiters() reports a
// phantom arm forever, HasArmedTimers() never goes false again, and a harness
// spins firing an id path 5 treats as a stale no-op.
//
// The fixture parks on a USER TASK after the catch, so the token survives the
// resume — a definition ending at an end event would drop the token and make
// every assertion below true by vacuum.
//
// Fails against a set-only implementation: the TimerWaiters assertion sees the
// stale token waiter. ⚠ The HasArmedTimers assertion only becomes falsifiable
// once HasArmedTimers is defined over TimerWaiters; before that it reads
// s.Timers, which this fixture never populates.
func TestTokenAwaitTimerIsClearedOnResume(t *testing.T) {
	t.Parallel()

	def := plainCatchTimerDef()
	parked := startParkedState(t, def)
	require.Len(t, parked.Tokens, 1)
	timerID := parked.Tokens[0].AwaitTimer
	require.NotEmpty(t, timerID, "control: the arm really is marked before it fires")
	require.Len(t, parked.TimerWaiters(), 1, "control: the arm really is enumerated before it fires")

	res, err := engine.Step(t.Context(), def, parked,
		engine.NewTimerFired(timerArmFixtureAt.Add(time.Hour), timerID), engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, res.State.Tokens, 1, "the token must SURVIVE the resume, or this test is vacuous")
	assert.Equal(t, "hold", res.State.Tokens[0].NodeID, "the token advanced past the catch")
	assert.Empty(t, res.State.Tokens[0].AwaitTimer, "a resumed token awaits no timer")
	assert.Empty(t, res.State.TimerWaiters(), "a fired timer leaves no phantom waiter")
	assert.False(t, res.State.HasArmedTimers(), "nothing is armed once the catch has fired")
}

// TestHasArmedTimersSeesEveryArmSource is the behavioural point of ADR-0177.
// Before it, HasArmedTimers() read s.Timers alone and measured FALSE for the
// boundary, event-gateway, event-sub-process and plain-intermediate-catch
// sources — four of five — so a harness reported "no armed timers" for an
// instance that was waiting on exactly one.
//
// The deadline row is the control that already passed; the stall row is
// ADR-0175's exclusion, which the widening must not lose: a compensation-stall
// record is a DETECTION deadline, and firing it manufactures the incident the
// window exists to detect.
func TestHasArmedTimersSeesEveryArmSource(t *testing.T) {
	t.Parallel()

	type testCase struct {
		state  func(t *testing.T) engine.InstanceState
		assert func(t *testing.T, st engine.InstanceState)
	}

	fromDef := func(def *model.ProcessDefinition) func(*testing.T) engine.InstanceState {
		return func(t *testing.T) engine.InstanceState {
			t.Helper()
			return startParkedState(t, def)
		}
	}
	armed := func(t *testing.T, st engine.InstanceState) {
		t.Helper()
		require.NotEmpty(t, st.TimerWaiters(), "control: the fixture really armed a timer")
		assert.True(t, st.HasArmedTimers())
	}

	cases := map[string]testCase{
		"plain intermediate catch timer": {state: fromDef(plainCatchTimerDef()), assert: armed},
		"boundary timer arm":             {state: fromDef(boundaryTimerArmDef()), assert: armed},
		"event-gateway timer arm":        {state: fromDef(gatewayTimerArmDef()), assert: armed},
		"event-subprocess timer arm":     {state: fromDef(eventSubprocessTimerArmDef()), assert: armed},
		"CONTROL: user-task deadline record": {
			state:  fromDef(deadlineRecordDef()),
			assert: armed,
		},
		"compensation-stall record stays excluded": {
			state: func(t *testing.T) engine.InstanceState {
				t.Helper()
				res, err := engine.Step(t.Context(), threeCompensableDef(), runThreeCompensableActivities(t),
					engine.NewCancelRequested(timerArmFixtureAt),
					engine.StepOptions{CompensationStallAfter: stallWindow})
				require.NoError(t, err)
				return res.State
			},
			assert: func(t *testing.T, st engine.InstanceState) {
				require.Len(t, stallTimerRecords(st), 1, "control: a stall timer IS armed")
				require.Len(t, st.TimerWaiters(), 1, "the authority enumerates it; only the predicate filters")
				assert.False(t, st.HasArmedTimers(),
					"a stall timer is a detection deadline, not work a harness may fire")
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, tc.state(t))
		})
	}
}

// TestTimerTokenWaitersRehydrationLimit pins ADR-0177's KNOWN LIMITATION and,
// in the same table, the assertion that makes the pin worth having.
//
// The limitation: an instance parked on a plain intermediate-catch timer BEFORE
// AwaitTimer shipped has no value for it in its stored row, so after rehydration
// the source stays invisible until the arm is re-created. Backfilling it would
// mean recognising a timer id by its shape — the id-sniffing ADR-0152 forbids
// and this ADR rejected.
//
// ⚠ The pin row alone is near-vacuous: it is falsified ONLY by a backfill
// implemented inside TimerTokenWaiters, and a backfill done in a migration or
// the rehydrate path would leave it green. The second row is the one that
// genuinely failed before the field existed — same token, AwaitTimer set,
// waiter produced — so the pair cannot both pass on an engine that never
// enumerates the token source.
func TestTimerTokenWaitersRehydrationLimit(t *testing.T) {
	t.Parallel()

	// The shape a pre-ADR-0177 build persisted: the timer id lives in
	// AwaitCommand and nowhere else.
	rehydrated := func() engine.InstanceState {
		return engine.InstanceState{
			InstanceID: "i1",
			Tokens: []engine.Token{{
				ID:           "i1-t1",
				NodeID:       "wait",
				State:        engine.TokenWaiting,
				AwaitCommand: "i1-tm1",
			}},
		}
	}

	type testCase struct {
		state  func() engine.InstanceState
		assert func(t *testing.T, got []engine.TimerWaiter, st engine.InstanceState)
	}

	cases := map[string]testCase{
		"pre-change snapshot yields no token waiter": {
			state: rehydrated,
			assert: func(t *testing.T, got []engine.TimerWaiter, st engine.InstanceState) {
				require.Equal(t, "i1-tm1", st.Tokens[0].AwaitCommand,
					"control: the timer id IS present, just not under a name the engine can trust")
				assert.Nil(t, got, "AwaitCommand alone must not be sniffed for a timer id")
				assert.False(t, st.HasArmedTimers())
			},
		},
		"the same token with AwaitTimer set yields a waiter": {
			state: func() engine.InstanceState {
				st := rehydrated()
				st.Tokens[0].AwaitTimer = "i1-tm1"
				return st
			},
			assert: func(t *testing.T, got []engine.TimerWaiter, st engine.InstanceState) {
				assert.Equal(t, []engine.TimerWaiter{{
					TimerID: "i1-tm1",
					Kind:    engine.TimerIntermediate,
					NodeID:  "wait",
					TokenID: "i1-t1",
				}}, got)
				assert.True(t, st.HasArmedTimers())
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			st := tc.state()
			tc.assert(t, st.TimerTokenWaiters(), st)
		})
	}
}
