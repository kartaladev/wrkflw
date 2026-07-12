package expreval_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/expreval"
)

// TestEvaluatorHonorsTimeout asserts that every Eval* method aborts a runaway
// expression with ErrEvalTimeout instead of blocking the caller — the engine's
// single-threaded driver loop — indefinitely (the DoS the audit flagged). The
// "runaway" is an env function that blocks until the test releases it, so the
// evaluation goroutine is cleaned up deterministically rather than spinning a core.
func TestEvaluatorHonorsTimeout(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		run  func(ev *expreval.Evaluator, code string, env map[string]any) error
	}

	cases := []testCase{
		{name: "EvalBool", run: func(ev *expreval.Evaluator, code string, env map[string]any) error {
			_, err := ev.EvalBool(code, env)
			return err
		}},
		{name: "EvalDuration", run: func(ev *expreval.Evaluator, code string, env map[string]any) error {
			_, err := ev.EvalDuration(code, env)
			return err
		}},
		{name: "EvalString", run: func(ev *expreval.Evaluator, code string, env map[string]any) error {
			_, err := ev.EvalString(code, env)
			return err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			env := map[string]any{
				"block": func() bool {
					<-release
					return true
				},
			}
			ev := expreval.New(expreval.WithTimeout(50 * time.Millisecond))

			start := time.Now()
			err := tc.run(ev, "block()", env)
			elapsed := time.Since(start)

			require.ErrorIs(t, err, expreval.ErrEvalTimeout)
			assert.Less(t, elapsed, 2*time.Second,
				"must return promptly after the timeout, not block on the runaway expression")
		})
	}
}
