package kernel_test

import (
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToken(t *testing.T) {
	tests := map[string]struct {
		assert func(t *testing.T)
	}{
		"is comparable": {
			assert: func(t *testing.T) {
				tok1 := kernel.Version(42)
				tok2 := kernel.Version(42)
				tok3 := kernel.Version(43)
				require.Equal(t, tok1, tok2)
				require.NotEqual(t, tok1, tok3)
			},
		},
		"orders as int64": {
			assert: func(t *testing.T) {
				tok1 := kernel.Version(1)
				tok2 := kernel.Version(2)
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
				ev := kernel.OutboxEvent{Topic: "test.topic", Payload: payload}
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
				events := []kernel.OutboxEvent{{Topic: "test", Payload: map[string]any{}}}
				step := kernel.AppliedStep{State: state, Trigger: trigger, Events: events}
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
				require.ErrorIs(t, kernel.ErrConcurrentUpdate, kernel.ErrConcurrentUpdate)
			},
		},
		"ErrInstanceNotFound is self-identifying": {
			assert: func(t *testing.T) {
				require.ErrorIs(t, kernel.ErrInstanceNotFound, kernel.ErrInstanceNotFound)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}
