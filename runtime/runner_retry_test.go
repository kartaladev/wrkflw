package runtime_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
)

// retryContractDef returns a minimal process that parks a service task named "a"
// with no node-level RetryPolicy (and no default policy supplied to the runner),
// so any ActionFailed is the only trigger that terminates the task. The journal
// records the ActionFailed trigger, which the test inspects for Retryable.
//
//	start → task("a") → end
func retryContractDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "retry-contract", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("task", activity.WithActionName("a")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
}

// TestActionFailedHonoursRetryContract asserts that when a service action returns
// an action.NonRetryable error, the runtime emits ActionFailed with Retryable=false,
// and that a plain error stays Retryable=true (the historical default).
//
// Harness: runtime.NewProcessDriver + kernel.NewMemStore (same as retry_test.go /
// action_panic_test.go). The runner drives the process to terminal state (StatusFailed,
// no retry policy). The MemStore.Entries journal records every applied trigger;
// the ActionFailed trigger is the one immediately preceding the FailInstance
// terminal step. We locate it by type-asserting each journal entry.
func TestActionFailedHonoursRetryContract(t *testing.T) {
	tests := map[string]struct {
		actErr        error
		wantRetryable bool
	}{
		"plain error stays retryable":        {errors.New("boom"), true},
		"NonRetryable becomes non-retryable": {action.NonRetryable(errors.New("4xx")), false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			act := action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
				return nil, tc.actErr
			})

			store := runtimetest.MustMemStore(t)
			cat := action.NewMapCatalog(map[string]action.Action{"a": act})
			// No retry policy: ActionFailed is terminal → instance reaches StatusFailed.
			runner := runtimetest.MustRunner(t, cat, store)

			const instanceID = "rc-1"
			st, err := runner.Run(t.Context(), retryContractDef(), instanceID, nil)
			require.NoError(t, err)
			assert.Equal(t, engine.StatusFailed, st.Status, "action error with no retry policy must fail instance")

			// Inspect the journal: locate the ActionFailed trigger.
			entries, err := store.Entries(t.Context(), instanceID)
			require.NoError(t, err)
			var af engine.ActionFailed
			var found bool
			for _, entry := range entries {
				if v, ok := entry.(engine.ActionFailed); ok {
					af = v
					found = true
					break
				}
			}
			require.True(t, found, "journal must contain an ActionFailed trigger; entries: %v", entries)
			assert.Equal(t, tc.wantRetryable, af.Retryable,
				"ActionFailed.Retryable must match action.IsRetryable(err)")
		})
	}
}
