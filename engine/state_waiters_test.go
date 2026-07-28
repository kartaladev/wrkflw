package engine

// state_waiters_test.go — white-box tests for the InstanceState waiter
// accessors. Uses package engine (not engine_test) to construct the unexported
// arm/boundary types directly, mirroring engine/state_esp_test.go.

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMessageEventSubprocessWaiters(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		arms   []eventTriggeredSubprocessArm
		assert func(t *testing.T, got []MessageWaiter)
	}{
		"none": {
			arms:   nil,
			assert: func(t *testing.T, got []MessageWaiter) { assert.Nil(t, got) },
		},
		"only message arms, in slice order, timer/signal skipped": {
			arms: []eventTriggeredSubprocessArm{
				{EventSubprocessNode: "esp-msg", triggerMatch: triggerMatch{Message: "cancel", MessageKey: "order-1"}},
				{EventSubprocessNode: "esp-timer", triggerMatch: triggerMatch{TimerID: "t1"}},
				{EventSubprocessNode: "esp-sig", triggerMatch: triggerMatch{Signal: "sig-a"}},
				{EventSubprocessNode: "esp-msg2", triggerMatch: triggerMatch{Message: "amend", MessageKey: ""}},
			},
			assert: func(t *testing.T, got []MessageWaiter) {
				assert.Equal(t, []MessageWaiter{
					{Name: "cancel", CorrelationKey: "order-1"},
					{Name: "amend", CorrelationKey: ""},
				}, got)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := &InstanceState{EventTriggeredSubprocesses: tc.arms}
			tc.assert(t, s.MessageEventSubprocessWaiters())
		})
	}
}

func TestSignalEventSubprocessNames(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		arms   []eventTriggeredSubprocessArm
		assert func(t *testing.T, got []string)
	}{
		"none": {
			arms:   nil,
			assert: func(t *testing.T, got []string) { assert.Nil(t, got) },
		},
		"only signal arms, in slice order, timer/message skipped": {
			arms: []eventTriggeredSubprocessArm{
				{EventSubprocessNode: "esp-sig", triggerMatch: triggerMatch{Signal: "sig-a"}},
				{EventSubprocessNode: "esp-msg", triggerMatch: triggerMatch{Message: "cancel"}},
				{EventSubprocessNode: "esp-sig2", triggerMatch: triggerMatch{Signal: "sig-b"}},
				{EventSubprocessNode: "esp-timer", triggerMatch: triggerMatch{TimerID: "t1"}},
			},
			assert: func(t *testing.T, got []string) {
				assert.Equal(t, []string{"sig-a", "sig-b"}, got)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := &InstanceState{EventTriggeredSubprocesses: tc.arms}
			tc.assert(t, s.SignalEventSubprocessNames())
		})
	}
}

func TestMessageWaiters_UnionInOrder(t *testing.T) {
	t.Parallel()

	s := &InstanceState{
		Tokens: []Token{
			{ID: "t1", AwaitMessage: "tok-msg", AwaitMessageKey: "k1"},
			{ID: "t2"}, // not awaiting a message
		},
		Boundaries: []boundaryArm{
			{HostToken: "h1", BoundaryNode: "bnd", triggerMatch: triggerMatch{Message: "bnd-msg", MessageKey: "k2"}},
			{HostToken: "h2", BoundaryNode: "bnd-timer", triggerMatch: triggerMatch{TimerID: "bt1"}}, // contributes nothing
		},
		ArmedEvents: []armedEvent{
			{GatewayToken: "g1", CatchNode: "c1", triggerMatch: triggerMatch{Message: "gw-msg", MessageKey: "k3"}},
		},
		EventTriggeredSubprocesses: []eventTriggeredSubprocessArm{
			{EventSubprocessNode: "esp", triggerMatch: triggerMatch{Message: "esp-msg", MessageKey: "k4"}},
			{EventSubprocessNode: "esp-timer", triggerMatch: triggerMatch{TimerID: "et1"}}, // contributes nothing
		},
	}

	// Order: tokens, then boundaries, then gateway arms, then event-subs.
	assert.Equal(t, []MessageWaiter{
		{Name: "tok-msg", CorrelationKey: "k1"},
		{Name: "bnd-msg", CorrelationKey: "k2"},
		{Name: "gw-msg", CorrelationKey: "k3"},
		{Name: "esp-msg", CorrelationKey: "k4"},
	}, s.MessageWaiters())
}

func TestMessageWaiters_Empty(t *testing.T) {
	t.Parallel()
	assert.Nil(t, (&InstanceState{}).MessageWaiters())
}

func TestSignalBoundaryNames(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		arms   []boundaryArm
		assert func(t *testing.T, got []string)
	}{
		"none": {
			arms:   nil,
			assert: func(t *testing.T, got []string) { assert.Nil(t, got) },
		},
		"only signal arms, in slice order, timer/message skipped": {
			arms: []boundaryArm{
				{HostToken: "h1", BoundaryNode: "bnd-sig", triggerMatch: triggerMatch{Signal: "escalate"}},
				{HostToken: "h2", BoundaryNode: "bnd-timer", triggerMatch: triggerMatch{TimerID: "t1"}},
				{HostToken: "h3", BoundaryNode: "bnd-msg", triggerMatch: triggerMatch{Message: "cancel"}},
				{HostToken: "h4", BoundaryNode: "bnd-sig2", triggerMatch: triggerMatch{Signal: "abort"}},
			},
			assert: func(t *testing.T, got []string) {
				assert.Equal(t, []string{"escalate", "abort"}, got)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := &InstanceState{Boundaries: tc.arms}
			tc.assert(t, s.SignalBoundaryNames())
		})
	}
}

func TestSignalArmedEventNames(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		arms   []armedEvent
		assert func(t *testing.T, got []string)
	}{
		"none": {
			arms:   nil,
			assert: func(t *testing.T, got []string) { assert.Nil(t, got) },
		},
		"only signal arms, in slice order, timer/message skipped": {
			arms: []armedEvent{
				{GatewayToken: "g1", CatchNode: "c-sig", triggerMatch: triggerMatch{Signal: "approved"}},
				{GatewayToken: "g1", CatchNode: "c-timer", triggerMatch: triggerMatch{TimerID: "t1"}},
				{GatewayToken: "g1", CatchNode: "c-msg", triggerMatch: triggerMatch{Message: "m"}},
			},
			assert: func(t *testing.T, got []string) {
				assert.Equal(t, []string{"approved"}, got)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := &InstanceState{ArmedEvents: tc.arms}
			tc.assert(t, s.SignalArmedEventNames())
		})
	}
}

func TestSignalWaiters_Union(t *testing.T) {
	t.Parallel()

	s := &InstanceState{
		Tokens: []Token{
			{ID: "t1", AwaitSignal: "tok-sig"},
			{ID: "t2"},
		},
		Boundaries: []boundaryArm{
			{HostToken: "h1", BoundaryNode: "bnd", triggerMatch: triggerMatch{Signal: "bnd-sig"}},
			{HostToken: "h2", BoundaryNode: "bnd-timer", triggerMatch: triggerMatch{TimerID: "bt1"}}, // contributes nothing
		},
		ArmedEvents: []armedEvent{
			{GatewayToken: "g1", CatchNode: "c1", triggerMatch: triggerMatch{Signal: "gw-sig"}},
		},
		EventTriggeredSubprocesses: []eventTriggeredSubprocessArm{
			{EventSubprocessNode: "esp", triggerMatch: triggerMatch{Signal: "esp-sig"}},
			{EventSubprocessNode: "esp-msg", triggerMatch: triggerMatch{Message: "m"}}, // contributes nothing
		},
	}

	// Order mirrors MessageWaiters: tokens, then boundaries, then gateway arms,
	// then event-subs.
	assert.Equal(t, []string{"tok-sig", "bnd-sig", "gw-sig", "esp-sig"}, s.SignalWaiters())
}

func TestSignalWaiters_Empty(t *testing.T) {
	t.Parallel()
	assert.Nil(t, (&InstanceState{}).SignalWaiters())
}
