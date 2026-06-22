package engine_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zakyalvan/krtlwrkflw/engine"
)

func TestSentinelWrappingGraph(t *testing.T) {
	cases := []struct {
		name   string
		assert func(t *testing.T)
	}{
		{
			name: "ErrTokenNotFound wraps ErrInvalidTransition",
			assert: func(t *testing.T) {
				assert.ErrorIs(t, engine.ErrTokenNotFound, engine.ErrInvalidTransition,
					"a token-not-awaiting error is a wrong-state transition")
			},
		},
		{
			name: "ErrNoMatchingFlow is not a wrong-state transition",
			assert: func(t *testing.T) {
				assert.NotErrorIs(t, engine.ErrNoMatchingFlow, engine.ErrInvalidTransition,
					"gateway routing failure is a definition error, not wrong-state")
			},
		},
		{
			name: "ErrUnknownTrigger is not a wrong-state transition",
			assert: func(t *testing.T) {
				assert.NotErrorIs(t, engine.ErrUnknownTrigger, engine.ErrInvalidTransition,
					"unsupported trigger type is an infrastructure error, not wrong-state")
			},
		},
		{
			name: "wrapped ErrTokenNotFound still satisfies both sentinels",
			assert: func(t *testing.T) {
				wrapped := fmt.Errorf("workflow-runtime: step: %w", engine.ErrTokenNotFound)
				assert.ErrorIs(t, wrapped, engine.ErrTokenNotFound)
				assert.ErrorIs(t, wrapped, engine.ErrInvalidTransition)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}
