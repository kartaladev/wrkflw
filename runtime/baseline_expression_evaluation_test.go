package runtime_test

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/internal/expreval"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
)

// blockingCatchDef builds start → catch → task → end, where the intermediate
// catch event's own expression (configured by opt) calls the process-variable
// function block(), so the test can make that evaluation never return.
func blockingCatchDef(id string, opt event.CatchOption) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: id, Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewIntermediateCatch("wait", opt),
			activity.NewServiceTask("task", activity.WithTaskAction("noop")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait"},
			{ID: "f2", Source: "wait", Target: "task"},
			{ID: "f3", Source: "task", Target: "end"},
		},
	}
}

// TestBaselineExpressionTimeoutThroughDriver characterizes the timer-duration and
// correlation-key rows of "A runaway expression is abandoned under the opt-in
// timeout" (openspec/specs/expression-evaluation/spec.md) through a
// ProcessDriver; the gateway-condition row is
// TestProcessDriverWithExpressionTimeoutGuardsGateway.
func TestBaselineExpressionTimeoutThroughDriver(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, elapsed time.Duration, err error)
	}

	abandoned := func(t *testing.T, elapsed time.Duration, err error) {
		t.Helper()
		require.Error(t, err)
		assert.ErrorIs(t, err, expreval.ErrEvalTimeout,
			"the opted-in timeout must surface as ErrEvalTimeout through Drive")
		assert.Less(t, elapsed, 2*time.Second,
			"Drive must return promptly after the timeout, not block on the runaway expression")
	}

	cases := []testCase{
		{
			name:   "timer duration",
			def:    blockingCatchDef("timer-block", event.WithCatchTimer(schedule.AfterExpr("block()"))),
			assert: abandoned,
		},
		{
			name:   "message correlation key",
			def:    blockingCatchDef("corr-block", event.WithMessageCorrelator("order-paid", "block()")),
			assert: abandoned,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			release := make(chan struct{})
			t.Cleanup(func() { close(release) })

			driver := runtimetest.MustProcessDriver(t, noopCatalog(), runtimetest.MustMemStore(t),
				runtime.WithClock(clockwork.NewFakeClock()),
				runtime.WithExpressionTimeout(50*time.Millisecond))

			vars := map[string]any{
				"block": func() string {
					<-release // never released until cleanup
					return "1h"
				},
			}

			start := time.Now()
			_, err := driver.Drive(t.Context(), tc.def, "b1", vars)
			tc.assert(t, time.Since(start), err)
		})
	}
}
