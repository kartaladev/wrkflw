package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// TestBoundaryEmitsTriggerNotFireAt asserts that a cron timer boundary arms a
// ScheduleTimer carrying the raw cron TriggerSpec (native recurrence owned by the
// scheduler), and that the engine no longer reduces it to a FireAt instant. Cron
// used to error via triggerDelay; it must now pass through untouched on the wire.
func TestBoundaryEmitsTriggerNotFireAt(t *testing.T) {
	def := receiveTaskBoundaryDef(event.NewBoundary("bnd", "recv", event.WithBoundaryTimer(schedule.Cron(`0 9 * * *`))))
	t0 := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)

	var st *engine.ScheduleTimer
	for _, c := range r1.Commands {
		if s, ok := c.(engine.ScheduleTimer); ok {
			vv := s
			st = &vv
		}
	}
	require.NotNil(t, st, "cron boundary must arm a ScheduleTimer")
	cron, ok := st.Trigger.CronExpr()
	if !ok || cron != `0 9 * * *` {
		t.Fatalf("trigger = %+v, want cron 0 9 * * *", st.Trigger)
	}
	assert.Equal(t, engine.TimerIntermediate, st.Kind, "timer boundary is TimerIntermediate")
}

// interruptingMessageBoundaryDef returns a definition:
//
//	Start → UserTask("work") → End
//	               ↑ interrupting message boundary "cancel" → escalate → End2
func interruptingMessageBoundaryDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-bnd-msg-int", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("work", nil),
			event.NewBoundary("bnd-msg", "work", event.WithBoundaryMessage("cancel", "")),
			activity.NewServiceTask("escalate", activity.WithActionName("escalate-action")),
			event.NewEnd("end"),
			event.NewEnd("end2"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "work"},
			{ID: "f-work-end", Source: "work", Target: "end"},
			{ID: "f-bnd-escalate", Source: "bnd-msg", Target: "escalate"},
			{ID: "f-escalate-end", Source: "escalate", Target: "end2"},
		},
	}
}

// TestInterruptingMessageBoundaryInterruptsHost verifies that a message boundary:
//  1. Is armed when the host activity parks (no ScheduleTimer for message boundaries).
//  2. On a matching MessageReceived, interrupts the host (the "work" token is
//     consumed) and routes a new token onto the boundary's outgoing flow
//     ("escalate" → InvokeAction).
func TestInterruptingMessageBoundaryInterruptsHost(t *testing.T) {
	def := interruptingMessageBoundaryDef()
	t0 := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)

	// Step 1: Start → UserTask parked; message boundary arm recorded.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)

	var awaitHuman *engine.AwaitHuman
	for _, c := range r1.Commands {
		if ah, ok := c.(engine.AwaitHuman); ok {
			vv := ah
			awaitHuman = &vv
		}
	}
	require.NotNil(t, awaitHuman, "expected AwaitHuman for work task")

	// No ScheduleTimer for a message boundary.
	for _, c := range r1.Commands {
		_, isTimer := c.(engine.ScheduleTimer)
		assert.False(t, isTimer, "message boundary must not emit ScheduleTimer")
	}

	require.Len(t, r1.State.Tokens, 1)
	assert.Equal(t, "work", r1.State.Tokens[0].NodeID)
	require.Len(t, r1.State.Boundaries, 1, "message boundary arm must be recorded")

	// Step 2: Matching message fires → host interrupted, token on "escalate".
	r2, err := engine.Step(def, r1.State,
		engine.NewMessageReceived(t0, "cancel", "", map[string]any{"reason": "x"}), engine.StepOptions{})
	require.NoError(t, err)

	var escalateIA *engine.InvokeAction
	for _, c := range r2.Commands {
		if ia, ok := c.(engine.InvokeAction); ok {
			vv := ia
			escalateIA = &vv
		}
	}
	require.NotNil(t, escalateIA, "expected InvokeAction for escalate-action")
	assert.Equal(t, "escalate-action", escalateIA.Name)

	// Host interrupted: exactly one token, now at "escalate", none at "work".
	require.Len(t, r2.State.Tokens, 1, "interrupting boundary: host consumed, one token at escalate")
	assert.Equal(t, "escalate", r2.State.Tokens[0].NodeID)
	assert.Empty(t, r2.State.Boundaries, "boundary arms cleared after interrupting fire")
}

// TestMessageBoundaryWaitersExposesArmedMessageBoundaries verifies the exported
// accessor that lets a runtime register message-boundary waiters (so a delivered
// message can be correlated to the parked instance even though no token carries
// an AwaitMessage for the boundary).
func TestMessageBoundaryWaitersExposesArmedMessageBoundaries(t *testing.T) {
	def := interruptingMessageBoundaryDef()
	t0 := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)

	waiters := r1.State.MessageBoundaryWaiters()
	require.Len(t, waiters, 1, "armed message boundary must be exposed as a waiter")
	assert.Equal(t, "cancel", waiters[0].Name)
	assert.Empty(t, waiters[0].CorrelationKey)

	// A timer/signal boundary contributes no message waiter.
	sigDef := nonInterruptingBoundaryDef()
	r2, err := engine.Step(sigDef, engine.InstanceState{InstanceID: "i2"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Empty(t, r2.State.MessageBoundaryWaiters(),
		"a signal boundary contributes no message waiter")
}

// TestMessageBoundaryCorrelationKey verifies that a message boundary configured
// with a correlation key only fires when the delivered key matches the resolved
// key, and is a clean no-op on a non-matching key.
func TestMessageBoundaryCorrelationKey(t *testing.T) {
	t.Parallel()

	// Definition: UserTask("work") with a message boundary "cancel" correlated on
	// the instance variable orderId (expr expression). Start vars set orderId=42,
	// so the resolved correlation key is "42".
	newDef := func() *model.ProcessDefinition {
		return &model.ProcessDefinition{
			ID: "p-bnd-msg-corr", Version: 1,
			Nodes: []model.Node{
				event.NewStart("start"),
				activity.NewUserTask("work", nil),
				event.NewBoundary("bnd-msg", "work",
					event.WithBoundaryMessage("cancel", "string(orderId)")),
				activity.NewServiceTask("escalate", activity.WithActionName("escalate-action")),
				event.NewEnd("end"),
				event.NewEnd("end2"),
			},
			Flows: []flow.SequenceFlow{
				{ID: "f-start", Source: "start", Target: "work"},
				{ID: "f-work-end", Source: "work", Target: "end"},
				{ID: "f-bnd-escalate", Source: "bnd-msg", Target: "escalate"},
				{ID: "f-escalate-end", Source: "escalate", Target: "end2"},
			},
		}
	}

	t0 := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name           string
		correlationKey string
		assert         func(t *testing.T, r engine.StepResult, err error)
	}

	cases := []testCase{
		{
			name:           "matching key fires the boundary",
			correlationKey: "42",
			assert: func(t *testing.T, r engine.StepResult, err error) {
				require.NoError(t, err)
				require.Len(t, r.State.Tokens, 1)
				assert.Equal(t, "escalate", r.State.Tokens[0].NodeID,
					"matching key must interrupt host and route to escalate")
				assert.Empty(t, r.State.Boundaries, "boundary cleared after fire")
			},
		},
		{
			name:           "non-matching key is a clean no-op",
			correlationKey: "99",
			assert: func(t *testing.T, r engine.StepResult, err error) {
				require.NoError(t, err)
				require.Len(t, r.State.Tokens, 1)
				assert.Equal(t, "work", r.State.Tokens[0].NodeID,
					"non-matching key must leave the host parked")
				require.Len(t, r.State.Boundaries, 1, "boundary arm must remain armed")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := newDef()
			r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
				engine.NewStartInstance(t0, map[string]any{"orderId": 42}), engine.StepOptions{})
			require.NoError(t, err)
			require.Len(t, r1.State.Boundaries, 1, "message boundary arm must be recorded")
			assert.Equal(t, "42", r1.State.Boundaries[0].MessageKey,
				"resolved correlation key must be evaluated at arm time")

			r2, err := engine.Step(def, r1.State,
				engine.NewMessageReceived(t0, "cancel", tc.correlationKey, nil), engine.StepOptions{})
			tc.assert(t, r2, err)
		})
	}
}

// nonInterruptingMessageBoundaryDef returns a definition:
//
//	Start → UserTask("work") → End
//	               ↑ non-interrupting message boundary "notify" → notify-svc → End2
func nonInterruptingMessageBoundaryDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-bnd-msg-nonint", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("work", nil),
			event.NewBoundary("bnd-msg", "work",
				event.WithBoundaryMessage("notify", ""), event.WithBoundaryNonInterrupting()),
			activity.NewServiceTask("notify-svc", activity.WithActionName("notify-action")),
			event.NewEnd("end"),
			event.NewEnd("end2"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "work"},
			{ID: "f-work-end", Source: "work", Target: "end"},
			{ID: "f-bnd-notify", Source: "bnd-msg", Target: "notify-svc"},
			{ID: "f-notify-end", Source: "notify-svc", Target: "end2"},
		},
	}
}

// TestNonInterruptingMessageBoundarySpawnsParallelToken verifies that a
// non-interrupting message boundary spawns an additional token on the boundary
// flow while leaving the host parked, and that the host can still complete.
func TestNonInterruptingMessageBoundarySpawnsParallelToken(t *testing.T) {
	def := nonInterruptingMessageBoundaryDef()
	t0 := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)

	var awaitHuman *engine.AwaitHuman
	for _, c := range r1.Commands {
		if ah, ok := c.(engine.AwaitHuman); ok {
			vv := ah
			awaitHuman = &vv
		}
	}
	require.NotNil(t, awaitHuman, "expected AwaitHuman for work task")
	require.Len(t, r1.State.Boundaries, 1, "message boundary arm must be recorded")

	// Step 2: Message fires → additional token on "notify-svc"; host still parked.
	r2, err := engine.Step(def, r1.State,
		engine.NewMessageReceived(t0, "notify", "", nil), engine.StepOptions{})
	require.NoError(t, err)

	var notifyIA *engine.InvokeAction
	for _, c := range r2.Commands {
		if ia, ok := c.(engine.InvokeAction); ok {
			vv := ia
			notifyIA = &vv
		}
	}
	require.NotNil(t, notifyIA, "expected InvokeAction for notify-action")
	assert.Equal(t, "notify-action", notifyIA.Name)

	require.Len(t, r2.State.Tokens, 2, "non-interrupting: host + new boundary token")
	nodeIDs := make(map[string]bool)
	for _, tok := range r2.State.Tokens {
		nodeIDs[tok.NodeID] = true
	}
	assert.True(t, nodeIDs["work"], "host token must still be at work")
	assert.True(t, nodeIDs["notify-svc"], "new token must be at notify-svc")
	assert.Empty(t, r2.State.Boundaries, "fired non-interrupting arm removed")

	// Step 3: Host can still be completed normally.
	r3, err := engine.Step(def, r2.State,
		engine.NewHumanCompleted(t0, awaitHuman.TaskToken, nil, authz.Actor{ID: "user1"}), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r3.State.Status)
}
