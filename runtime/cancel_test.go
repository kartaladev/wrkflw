package runtime_test

import (
	"context"
	"errors"
	"testing"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// cancelDef parks at a human task so Run returns with the instance Running;
// CancelActions lists the names of ServiceActions to run best-effort on cancel.
func cancelDef(cancelActions []string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "cancel-def", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewUserTask("wait", []string{"r"}),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait"},
			{ID: "f2", Source: "wait", Target: "end"},
		},
		CancelActions: cancelActions,
	}
}

func cancelRunner(t *testing.T, cat action.Catalog, fc clockwork.Clock) *runtime.ProcessDriver {
	t.Helper()
	store := mustMemStore(t)
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()
	return mustRunner(t, cat, store, runtime.WithRunnerClock(fc), runtime.WithHumanTasks(resolver, tasks, nil))
}

// TestRunnerCancelInstanceRunsCancelActions verifies that:
//  1. Both cancel actions run in definition order.
//  2. A failing cancel action (returns an error) does NOT fail CancelInstance.
//  3. The returned state is StatusTerminated with no live tokens.
func TestRunnerCancelInstanceRunsCancelActions(t *testing.T) {
	fc := clockwork.NewFakeClock()
	var ran []string
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"notify": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			ran = append(ran, "notify")
			return nil, nil
		}),
		"boom": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			ran = append(ran, "boom")
			return nil, errors.New("cancel action failed on purpose")
		}),
	})
	r := cancelRunner(t, cat, fc)
	def := cancelDef([]string{"notify", "boom"})

	_, err := r.Run(t.Context(), def, "c1", nil)
	require.NoError(t, err)

	// Cancel: both actions run; the failing "boom" is logged but must NOT fail the cancel.
	st, err := r.CancelInstance(t.Context(), def, "c1")
	require.NoError(t, err, "a failing cancel action must not fail CancelInstance")
	assert.Equal(t, engine.StatusTerminated, st.Status)
	assert.Empty(t, st.Tokens)
	assert.Equal(t, []string{"notify", "boom"}, ran, "both cancel actions ran in order")
}

// TestRunnerCancelInstanceMissingActionIsBestEffort verifies that an unresolved
// cancel action name is silently logged and skipped — CancelInstance still returns
// StatusTerminated with nil error.
func TestRunnerCancelInstanceMissingActionIsBestEffort(t *testing.T) {
	fc := clockwork.NewFakeClock()
	// No catalog entry for "ghost".
	r := cancelRunner(t, action.NewMapCatalog(nil), fc)
	def := cancelDef([]string{"ghost"})

	_, err := r.Run(t.Context(), def, "c2", nil)
	require.NoError(t, err)

	st, err := r.CancelInstance(t.Context(), def, "c2")
	require.NoError(t, err, "an unresolved cancel action must not fail CancelInstance")
	assert.Equal(t, engine.StatusTerminated, st.Status)
}
