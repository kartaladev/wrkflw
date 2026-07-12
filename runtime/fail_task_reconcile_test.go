package runtime_test

import (
	"context"
	"errors"
	"testing"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
)

// TestProcessDriverUnhandledFailureCancelsParkedTask is the end-to-end counterpart to
// TestProcessDriverCancelInstanceCancelsParkedTask (ADR-0089): when a parallel branch
// fails unhandled while another branch is parked at a UserTask, the instance
// fails and the parked task is Cancelled in the TaskStore — not orphaned in an
// inbox query.
func TestProcessDriverUnhandledFailureCancelsParkedTask(t *testing.T) {
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
	r := runtimetest.MustProcessDriver(t, cat, store,
		runtime.WithClock(fc), runtime.WithHumanTasks(resolver, tasks, authz.RoleAuthorizer{}))

	// start → fork → (user[UserTask] | svc[Service "boom"]) → join → end
	def := &model.ProcessDefinition{
		ID: "fail-e2e", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewUserTask("user", activity.WithEligibleRoles("r")),
			activity.NewServiceTask("svc", activity.WithTaskAction("boom")),
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
