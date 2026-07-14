package eventing

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/runtime/kernel"
)

func TestIsBenignDriverShutdown(t *testing.T) {
	type testCase struct {
		err    error
		assert func(t *testing.T, got bool)
	}
	cases := map[string]testCase{
		"wrapped driver-shutting-down is benign": {
			err:    fmt.Errorf("workflow-runtime: chain start successor %q: %w", "id", kernel.ErrDriverShuttingDown),
			assert: func(t *testing.T, got bool) { assert.True(t, got) },
		},
		"generic error is not benign": {
			err:    errors.New("db down"),
			assert: func(t *testing.T, got bool) { assert.False(t, got) },
		},
		"nil is not benign": {
			err:    nil,
			assert: func(t *testing.T, got bool) { assert.False(t, got) },
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tc.assert(t, isBenignDriverShutdown(tc.err))
		})
	}
}
