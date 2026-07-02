package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// timeoutTaskDef builds start → task("t") → end with no retry policy, so a single
// action failure (e.g. a timeout) drives the instance terminal (StatusFailed).
func timeoutTaskDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "timeout-test", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewServiceTask("task", model.WithActionName("t")),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
}

// TestRunner_ActionTimeout verifies WithActionTimeout: an active timeout cancels a
// blocking action (driving the instance to StatusFailed), an explicit zero disables
// the deadline, and the default applies a deadline to the action context.
func TestRunner_ActionTimeout(t *testing.T) {
	t.Parallel()

	type obs struct {
		canceled    bool
		hadDeadline bool
	}

	type testCase struct {
		name      string
		opts      []runtime.Option
		newAction func(o *obs) action.ServiceAction
		assert    func(t *testing.T, st engine.InstanceState, o *obs)
	}

	cases := []testCase{
		{
			name: "active timeout cancels a blocking action",
			opts: []runtime.Option{runtime.WithActionTimeout(20 * time.Millisecond)},
			newAction: func(o *obs) action.ServiceAction {
				return action.Func(func(ctx context.Context, _ map[string]any) (map[string]any, error) {
					select {
					case <-ctx.Done():
						o.canceled = true
						return nil, ctx.Err()
					case <-time.After(2 * time.Second):
						return nil, nil
					}
				})
			},
			assert: func(t *testing.T, st engine.InstanceState, o *obs) {
				assert.Equal(t, engine.StatusFailed, st.Status,
					"a timed-out action (no retry policy) must drive the instance to StatusFailed")
				assert.True(t, o.canceled, "the action context must be cancelled at the timeout")
			},
		},
		{
			name: "explicit zero disables the deadline",
			opts: []runtime.Option{runtime.WithActionTimeout(0)},
			newAction: func(o *obs) action.ServiceAction {
				return action.Func(func(ctx context.Context, _ map[string]any) (map[string]any, error) {
					_, o.hadDeadline = ctx.Deadline()
					return nil, nil
				})
			},
			assert: func(t *testing.T, st engine.InstanceState, o *obs) {
				assert.Equal(t, engine.StatusCompleted, st.Status)
				assert.False(t, o.hadDeadline, "WithActionTimeout(0) must not set a deadline on the action context")
			},
		},
		{
			name: "default applies a deadline",
			opts: nil,
			newAction: func(o *obs) action.ServiceAction {
				return action.Func(func(ctx context.Context, _ map[string]any) (map[string]any, error) {
					_, o.hadDeadline = ctx.Deadline()
					return nil, nil
				})
			},
			assert: func(t *testing.T, st engine.InstanceState, o *obs) {
				assert.Equal(t, engine.StatusCompleted, st.Status)
				assert.True(t, o.hadDeadline, "the default action timeout must set a deadline on the action context")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			o := &obs{}
			cat := action.NewMapCatalog(map[string]action.ServiceAction{"t": tc.newAction(o)})
			opts := append([]runtime.Option{runtime.WithRunnerClock(clockwork.NewFakeClock())}, tc.opts...)
			r := runtime.NewRunner(cat, mustMemStore(t), opts...)

			st, err := r.Run(t.Context(), timeoutTaskDef(), "p1", nil)
			require.NoError(t, err)
			tc.assert(t, st, o)
		})
	}
}
