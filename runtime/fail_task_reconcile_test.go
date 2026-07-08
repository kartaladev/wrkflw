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
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
)

// TestRunnerUnhandledFailureCancelsParkedTask is the end-to-end counterpart to
// TestRunnerCancelInstanceCancelsParkedTask (ADR-0089): when a parallel branch
// fails unhandled while another branch is parked at a UserTask, the instance
// fails and the parked task is Cancelled in the TaskStore — not orphaned in an
// inbox query.
func TestRunnerUnhandledFailureCancelsParkedTask(t *testing.T) {
	fc := clockwork.NewFakeClock()
	actor := authz.Actor{ID: "sam", Roles: []string{"r"}}
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{"r": {actor}})
	tasks := humantask.NewMemTaskStore()
	store := runtimetest.MustMemStore(t)

	cat := action.NewCatalog(map[string]action.Action{
		"boom": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			// Non-retryable → the instance fails immediately (no retry scheduled).
			return nil, action.NonRetryable(errors.New("boom"))
		}),
	})
	r := runtimetest.MustRunner(t, cat, store,
		runtime.WithClock(fc), runtime.WithHumanTasks(resolver, tasks, authz.RoleAuthorizer{}))

	// start → fork → (user[UserTask] | svc[Service "boom"]) → join → end
	def := &model.ProcessDefinition{
		ID: "fail-e2e", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewUserTask("user", []string{"r"}),
			activity.NewServiceTask("svc", activity.WithActionName("boom")),
			gateway.NewParallel("join"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f0", Source: "start", Target: "fork"},
			{ID: "f1", Source: "fork", Target: "user"},
			{ID: "f2", Source: "fork", Target: "svc"},
			{ID: "f3", Source: "user", Target: "join"},
			{ID: "f4", Source: "svc", Target: "join"},
			{ID: "f5", Source: "join", Target: "end"},
		},
	}

	st, err := r.Drive(t.Context(), def, "fe-1", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusFailed, st.Status, "unhandled action failure must fail the instance")

	// The parked task must be Cancelled and gone from the inbox.
	before, err := tasks.ClaimableBy(t.Context(), actor)
	require.NoError(t, err)
	require.Empty(t, before, "a failed instance must not leave tasks in the inbox")

	assigned, err := tasks.AssignedTo(t.Context(), actor.ID)
	require.NoError(t, err)
	for _, ht := range assigned {
		assert.NotEqual(t, humantask.Unclaimed, ht.State)
	}
}
