package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/expreval"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
)

// gatewayBlockDef builds start → xor -{block()}-> big ; -default-> small → end.
// The conditional flow's expression calls a process-variable function block(),
// so a test can drive the gateway evaluation into a runaway by supplying a
// block() that never returns.
func gatewayBlockDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "gw-block", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewExclusive("xor"),
			activity.NewServiceTask("big", activity.WithTaskAction("noop")),
			activity.NewServiceTask("small", activity.WithTaskAction("noop")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "xor"},
			{ID: "f2", Source: "xor", Target: "big", Condition: "block()"},
			{ID: "f3", Source: "xor", Target: "small", IsDefault: true},
			{ID: "f4", Source: "big", Target: "end"},
			{ID: "f5", Source: "small", Target: "end"},
		},
	}
}

func noopCatalog() action.Catalog {
	return action.NewCatalog(map[string]action.Action{
		"noop": action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
			return in, nil
		}),
	})
}

// TestRunnerWithExpressionTimeoutGuardsGateway proves the DoS guard is reachable
// in-engine when opted in: a Runner built WithExpressionTimeout(50ms) evaluating
// a blocking gateway condition aborts with expreval.ErrEvalTimeout (surfaced as a
// Run error) rather than hanging the driver loop. This is the explicit opt-in the
// ADR-0049 follow-up called for.
func TestRunnerWithExpressionTimeoutGuardsGateway(t *testing.T) {
	fc := clockwork.NewFakeClock()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	driver := runtimetest.MustRunner(t, noopCatalog(), runtimetest.MustMemStore(t),
		runtime.WithClock(fc),
		runtime.WithExpressionTimeout(50*time.Millisecond))

	vars := map[string]any{
		"block": func() bool {
			<-release // never released until cleanup
			return true
		},
	}

	start := time.Now()
	_, err := driver.Drive(t.Context(), gatewayBlockDef(), "g1", vars)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, expreval.ErrEvalTimeout,
		"the opted-in timeout must surface as ErrEvalTimeout through Run")
	assert.Less(t, elapsed, 2*time.Second,
		"Run must return promptly after the timeout, not block on the runaway expression")
}

// TestRunnerDefaultEvaluatesNormallyAndStaysPure proves the DEFAULT runner (no
// expression-timeout option) still evaluates gateway conditions normally and does
// not acquire the wall-clock guard: with a real (non-blocking) condition the
// instance runs to completion. amount=150 takes the "big" branch.
func TestRunnerDefaultEvaluatesNormallyAndStaysPure(t *testing.T) {
	fc := clockwork.NewFakeClock()
	driver := runtimetest.MustRunner(t, noopCatalog(), runtimetest.MustMemStore(t), runtime.WithClock(fc))

	st, err := driver.Drive(t.Context(), exclusiveRuntimeDef(), "d1", map[string]any{"amount": 150})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, st.Status)
}

// exclusiveRuntimeDef mirrors the engine exclusiveDef but with noop actions so a
// runtime Runner can execute it end-to-end.
func exclusiveRuntimeDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "xor-rt", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewExclusive("xor"),
			activity.NewServiceTask("big", activity.WithTaskAction("noop")),
			activity.NewServiceTask("small", activity.WithTaskAction("noop")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "xor"},
			{ID: "f2", Source: "xor", Target: "big", Condition: "amount > 100"},
			{ID: "f3", Source: "xor", Target: "small", IsDefault: true},
			{ID: "f4", Source: "big", Target: "end"},
			{ID: "f5", Source: "small", Target: "end"},
		},
	}
}

// TestRunnerWithConditionEvaluatorInjectsCustom proves the lower-level option
// WithConditionEvaluator threads a caller-supplied evaluator into the engine.
func TestRunnerWithConditionEvaluatorInjectsCustom(t *testing.T) {
	fc := clockwork.NewFakeClock()
	ev := expreval.New(expreval.WithTimeout(0)) // pure, explicit
	driver := runtimetest.MustRunner(t, noopCatalog(), runtimetest.MustMemStore(t),
		runtime.WithClock(fc),
		runtime.WithConditionEvaluator(ev))

	st, err := driver.Drive(t.Context(), exclusiveRuntimeDef(), "c1", map[string]any{"amount": 5})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, st.Status)
}
