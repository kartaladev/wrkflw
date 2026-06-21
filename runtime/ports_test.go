package runtime_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func TestToken(t *testing.T) {
	tests := map[string]struct {
		assert func(t *testing.T)
	}{
		"is comparable": {
			assert: func(t *testing.T) {
				tok1 := runtime.Token(42)
				tok2 := runtime.Token(42)
				tok3 := runtime.Token(43)
				require.Equal(t, tok1, tok2)
				require.NotEqual(t, tok1, tok3)
			},
		},
		"orders as int64": {
			assert: func(t *testing.T) {
				tok1 := runtime.Token(1)
				tok2 := runtime.Token(2)
				assert.True(t, tok1 < tok2)
				assert.True(t, tok2 > tok1)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}

func TestOutboxEvent(t *testing.T) {
	tests := map[string]struct {
		assert func(t *testing.T)
	}{
		"constructs and exposes fields": {
			assert: func(t *testing.T) {
				payload := map[string]any{"key": "value"}
				ev := runtime.OutboxEvent{Topic: "test.topic", Payload: payload}
				require.Equal(t, "test.topic", ev.Topic)
				require.Equal(t, payload, ev.Payload)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}

func TestAppliedStep(t *testing.T) {
	tests := map[string]struct {
		assert func(t *testing.T)
	}{
		"constructs and exposes fields": {
			assert: func(t *testing.T) {
				state := engine.InstanceState{InstanceID: "i1", Status: engine.StatusRunning}
				trigger := engine.NewStartInstance(time.Unix(0, 0), map[string]any{})
				events := []runtime.OutboxEvent{{Topic: "test", Payload: map[string]any{}}}
				step := runtime.AppliedStep{State: state, Trigger: trigger, Events: events}
				require.Equal(t, "i1", step.State.InstanceID)
				require.Equal(t, trigger, step.Trigger)
				require.Equal(t, events, step.Events)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}

func TestSentinelErrors(t *testing.T) {
	tests := map[string]struct {
		assert func(t *testing.T)
	}{
		"ErrConcurrentUpdate is self-identifying": {
			assert: func(t *testing.T) {
				require.ErrorIs(t, runtime.ErrConcurrentUpdate, runtime.ErrConcurrentUpdate)
			},
		},
		"ErrInstanceNotFound is self-identifying": {
			assert: func(t *testing.T) {
				require.ErrorIs(t, runtime.ErrInstanceNotFound, runtime.ErrInstanceNotFound)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}
