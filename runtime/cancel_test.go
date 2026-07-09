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
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
)

// cancelDef parks at a human task so Run returns with the instance Running;
// CancelActions lists the names of Actions to run best-effort on cancel.
func cancelDef(cancelActions []string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "cancel-def", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("wait", activity.WithCandidateRoles("r")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait"},
			{ID: "f2", Source: "wait", Target: "end"},
		},
		CancelActions: cancelActions,
	}
}

func cancelRunner(t *testing.T, cat action.Catalog, fc clockwork.Clock) *runtime.ProcessDriver {
	t.Helper()
	store := runtimetest.MustMemStore(t)
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()
	return runtimetest.MustRunner(t, cat, store, runtime.WithClock(fc), runtime.WithHumanTasks(resolver, tasks, nil))
}

// TestRunnerCancelInstanceRunsCancelActions verifies that:
//  1. Both cancel actions run in definition order.
//  2. A failing cancel action (returns an error) does NOT fail CancelInstance.
//  3. The returned state is StatusTerminated with no live tokens.
func TestRunnerCancelInstanceRunsCancelActions(t *testing.T) {
	fc := clockwork.NewFakeClock()
	var ran []string
	cat := action.NewCatalog(map[string]action.Action{
		"notify": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			ran = append(ran, "notify")
			return nil, nil
		}),
		"boom": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			ran = append(ran, "boom")
			return nil, errors.New("cancel action failed on purpose")
		}),
	})
	r := cancelRunner(t, cat, fc)
	def := cancelDef([]string{"notify", "boom"})

	_, err := r.Drive(t.Context(), def, "c1", nil)
	require.NoError(t, err)

	// Cancel: both actions run; the failing "boom" is logged but must NOT fail the cancel.
	st, err := r.CancelInstance(t.Context(), def, "c1")
	require.NoError(t, err, "a failing cancel action must not fail CancelInstance")
	assert.Equal(t, engine.StatusTerminated, st.Status)
	assert.Empty(t, st.Tokens)
	assert.Equal(t, []string{"notify", "boom"}, ran, "both cancel actions ran in order")
}

// TestRunnerCancelInstanceCancelsParkedTask verifies the end-to-end reconciliation
// (ADR-0088): after CancelInstance, a task parked at a UserTask is Cancelled in the
// TaskStore and no longer surfaces in an inbox (ClaimableBy) query.
func TestRunnerCancelInstanceCancelsParkedTask(t *testing.T) {
	fc := clockwork.NewFakeClock()
	actor := authz.Actor{ID: "sam", Roles: []string{"r"}}
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{"r": {actor}})
	tasks := humantask.NewMemTaskStore()
	store := runtimetest.MustMemStore(t)
	r := runtimetest.MustRunner(t, action.NewCatalog(nil), store,
		runtime.WithClock(fc), runtime.WithHumanTasks(resolver, tasks, authz.RoleAuthorizer{}))
	def := cancelDef(nil)

	_, err := r.Drive(t.Context(), def, "c3", nil)
	require.NoError(t, err)

	// Precondition: the task is claimable before cancel.
	before, err := tasks.ClaimableBy(t.Context(), actor)
	require.NoError(t, err)
	require.Len(t, before, 1, "task must be claimable before cancel")
	token := before[0].TaskToken

	st, err := r.CancelInstance(t.Context(), def, "c3")
	require.NoError(t, err)
	require.Equal(t, engine.StatusTerminated, st.Status)

	// The task is now Cancelled in the store and gone from the inbox.
	got, err := tasks.Get(t.Context(), token)
	require.NoError(t, err)
	assert.Equal(t, humantask.Cancelled, got.State, "parked task must be Cancelled after CancelInstance")

	after, err := tasks.ClaimableBy(t.Context(), actor)
	require.NoError(t, err)
	assert.Empty(t, after, "a cancelled instance must not leave tasks in the inbox")
}

// TestRunnerCancelInstanceMissingActionIsBestEffort verifies that an unresolved
// cancel action name is silently logged and skipped — CancelInstance still returns
// StatusTerminated with nil error.
func TestRunnerCancelInstanceMissingActionIsBestEffort(t *testing.T) {
	fc := clockwork.NewFakeClock()
	// No catalog entry for "ghost".
	r := cancelRunner(t, action.NewCatalog(nil), fc)
	def := cancelDef([]string{"ghost"})

	_, err := r.Drive(t.Context(), def, "c2", nil)
	require.NoError(t, err)

	st, err := r.CancelInstance(t.Context(), def, "c2")
	require.NoError(t, err, "an unresolved cancel action must not fail CancelInstance")
	assert.Equal(t, engine.StatusTerminated, st.Status)
}
