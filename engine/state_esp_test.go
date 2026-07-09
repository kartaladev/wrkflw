package engine

// state_esp_test.go — white-box tests for InstanceState event-subprocess arm
// sweep helpers. Uses package engine (not engine_test) to access unexported
// types (eventSubprocessArm) and the unexported helper under test.

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// InstanceState.removeAllEventSubprocessArms
// ---------------------------------------------------------------------------

func TestInstanceState_removeAllEventSubprocessArms(t *testing.T) {
	t.Parallel()

	s := &InstanceState{
		EventSubprocesses: []eventSubprocessArm{
			{
				EnclosingScopeID:    "",
				EventSubprocessNode: "esp-timer",
				TimerID:             "esp-t1",
			},
			{
				EnclosingScopeID:    "scope-1",
				EventSubprocessNode: "esp-signal",
				Signal:              "sig-a",
			},
		},
	}

	got := s.removeAllEventSubprocessArms()

	assert.Equal(t, []string{"esp-t1"}, got)
	assert.Nil(t, s.EventSubprocesses)
}
