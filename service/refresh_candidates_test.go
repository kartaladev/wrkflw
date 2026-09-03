package service_test

import (
	"context"
	"sync"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/runtime/task"
	"github.com/kartaladev/wrkflw/service"
)

// swapResolver is an [humantask.ActorResolver] whose result can be swapped
// between task creation and refresh, so a test can observe that the refreshed
// list really replaced the creation-time one. It honours context cancellation so
// the lifecycle path through RefreshTaskCandidates is exercisable.
type swapResolver struct {
	mu     sync.Mutex
	actors []authz.Actor
}

func (r *swapResolver) set(actors []authz.Actor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.actors = actors
}

func (r *swapResolver) Candidates(ctx context.Context, _ authz.AuthzSpec, _ map[string]any) ([]authz.Actor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]authz.Actor(nil), r.actors...), nil
}

// refreshFixture is an approval instance parked at its user task, whose
// candidates come from one swapResolver shared by the driver (creation-time
// resolution) and the engine (refresh-time re-resolution).
type refreshFixture struct {
	svc      *service.ProcessEngine
	resolver *swapResolver
	tasks    *humantask.MemTaskStore
	taskID   string
}

// newRefreshFixture wires the fixture and drives the instance to its user-task
// park. withResolver=false omits service.WithActorResolver so the
// no-resolver-configured path is reachable.
func newRefreshFixture(t *testing.T, ctx context.Context, withResolver bool) refreshFixture {
	t.Helper()

	def := approvalDef()
	resolver := &swapResolver{actors: []authz.Actor{{ID: "alice", Roles: []string{"manager"}}}}
	tasks := humantask.NewMemTaskStore()
	az := authz.RoleAuthorizer{}
	clk := clockwork.NewFakeClock()

	store, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	reg := kernel.NewMapDefinitionRegistry(def)

	driver, err := runtime.NewProcessDriver(
		runtime.WithInstanceStore(store),
		runtime.WithClock(clk),
		runtime.WithDefinitions(reg),
		runtime.WithHumanTasks(resolver, tasks, az),
	)
	require.NoError(t, err)

	opts := []service.Option{
		service.WithProcessDriver(driver),
		service.WithInstanceStore(store),
		service.WithDefinitions(reg),
		service.WithLister(store),
		service.WithHumanTasks(tasks, az),
		service.WithClock(clk),
	}
	if withResolver {
		opts = append(opts, service.WithActorResolver(resolver))
	}
	svc, err := service.NewProcessEngine(opts...)
	require.NoError(t, err)

	parked, err := driver.Drive(ctx, def, "refresh-inst", nil)
	require.NoError(t, err)
	require.Len(t, parked.Tokens, 1, "must park at the user task")
	taskID := parked.Tokens[0].AwaitCommand
	require.NotEmpty(t, taskID, "task id must be set")

	return refreshFixture{svc: svc, resolver: resolver, tasks: tasks, taskID: taskID}
}

// TestRefreshTaskCandidates covers ProcessEngine.RefreshTaskCandidates over an
// approval process parked at a user task eligible to the "manager" role.
func TestRefreshTaskCandidates(t *testing.T) {
	t.Parallel()

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	viewer := authz.Actor{ID: "bob", Roles: []string{"viewer"}}
	carol := authz.Actor{ID: "carol", Roles: []string{"manager"}}

	type testCase struct {
		name         string
		withResolver bool
		// arrange mutates the default request and/or the fixture before the call.
		arrange func(t *testing.T, ctx context.Context, f refreshFixture, req service.RefreshTaskCandidatesRequest) service.RefreshTaskCandidatesRequest
		// ctx modifies the context handed to the SUT; nil means identity.
		ctx    func(ctx context.Context) context.Context
		assert func(t *testing.T, pi service.ProcessInstance, err error)
	}

	cases := []testCase{
		{
			name:         "refreshed set replaces the creation-time candidates",
			withResolver: true,
			arrange: func(t *testing.T, ctx context.Context, f refreshFixture, req service.RefreshTaskCandidatesRequest) service.RefreshTaskCandidatesRequest {
				created, err := f.tasks.Get(ctx, f.taskID)
				require.NoError(t, err)
				require.Equal(t, []string{"alice"}, actorIDs(created.Candidates),
					"precondition: creation-time candidates")
				f.resolver.set([]authz.Actor{carol})
				return req
			},
			assert: func(t *testing.T, pi service.ProcessInstance, err error) {
				require.NoError(t, err)
				require.NotNil(t, pi)
				active := pi.ActiveTasks()
				require.Len(t, active, 1)
				assert.Equal(t, []string{"carol"}, actorIDs(active[0].Candidates),
					"departed actor must disappear; refresh replaces wholesale")
			},
		},
		{
			name:         "no resolver configured",
			withResolver: false,
			assert: func(t *testing.T, _ service.ProcessInstance, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, task.ErrNoActorResolver)
			},
		},
		{
			name:         "closed task keeps its frozen candidate list",
			withResolver: true,
			arrange: func(t *testing.T, ctx context.Context, f refreshFixture, req service.RefreshTaskCandidatesRequest) service.RefreshTaskCandidatesRequest {
				_, err := f.svc.CompleteTask(ctx, service.CompleteTaskRequest{
					TaskID: f.taskID,
					Actor:  manager,
				})
				require.NoError(t, err)
				return req
			},
			assert: func(t *testing.T, _ service.ProcessInstance, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, task.ErrTaskNotOpen)
			},
		},
		{
			name:         "requester outside the eligibility spec",
			withResolver: true,
			arrange: func(_ *testing.T, _ context.Context, _ refreshFixture, req service.RefreshTaskCandidatesRequest) service.RefreshTaskCandidatesRequest {
				req.By = viewer
				return req
			},
			assert: func(t *testing.T, _ service.ProcessInstance, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
		{
			name:         "unknown task",
			withResolver: true,
			arrange: func(_ *testing.T, _ context.Context, _ refreshFixture, req service.RefreshTaskCandidatesRequest) service.RefreshTaskCandidatesRequest {
				req.TaskID = "no-such-task"
				return req
			},
			assert: func(t *testing.T, _ service.ProcessInstance, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, humantask.ErrTaskNotFound)
			},
		},
		{
			name:         "canceled context aborts the resolver lookup",
			withResolver: true,
			ctx: func(ctx context.Context) context.Context {
				cctx, cancel := context.WithCancel(ctx)
				cancel()
				return cctx
			},
			assert: func(t *testing.T, _ service.ProcessInstance, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, context.Canceled)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			f := newRefreshFixture(t, ctx, tc.withResolver)

			req := service.RefreshTaskCandidatesRequest{TaskID: f.taskID, By: manager}
			if tc.arrange != nil {
				req = tc.arrange(t, ctx, f, req)
			}

			callCtx := ctx
			if tc.ctx != nil {
				callCtx = tc.ctx(ctx)
			}

			pi, err := f.svc.RefreshTaskCandidates(callCtx, req)
			tc.assert(t, pi, err)
		})
	}
}

// actorIDs projects actors to their IDs for order-sensitive comparison.
func actorIDs(actors []authz.Actor) []string {
	ids := make([]string, len(actors))
	for i, a := range actors {
		ids[i] = a.ID
	}
	return ids
}
