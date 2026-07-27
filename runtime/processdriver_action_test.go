package runtime_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/processtest"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
)

// The tests in this file share the "drive a user task to park, then inspect the
// projection" shape but NOT their setup: the candidate-isolation table needs a
// mutable resolver and no scheduler, while the DueAt projection needs a deadline
// timer, a fake clock and a scheduler. They are therefore separate TestXxx
// functions rather than one table (see the project's table-test skill).

// sliceActorResolver is an [humantask.ActorResolver] that hands back the very
// slice the test holds — the aliasing shape a real registry-backed resolver has
// (see [humantask.StaticActorResolver], whose actors reference its own map).
// Mutating actors after a resolve stands in for a consumer updating its registry.
type sliceActorResolver struct {
	actors []authz.Actor
}

func (r *sliceActorResolver) Candidates(context.Context, authz.AuthzSpec, map[string]any) ([]authz.Actor, error) {
	return r.actors, nil
}

// capturingTaskStore records the exact [humantask.HumanTask] value handed to
// Upsert, WITHOUT the defensive clone [humantask.MemTaskStore] applies, so a test
// can assert on what the runtime actually passed to a store implementation.
type capturingTaskStore struct {
	*humantask.MemTaskStore

	mu   sync.Mutex
	last humantask.HumanTask
}

func newCapturingTaskStore() *capturingTaskStore {
	return &capturingTaskStore{MemTaskStore: humantask.NewMemTaskStore()}
}

func (s *capturingTaskStore) Upsert(ctx context.Context, t humantask.HumanTask) error {
	s.mu.Lock()
	s.last = t
	s.mu.Unlock()
	return s.MemTaskStore.Upsert(ctx, t)
}

// lastUpserted returns the most recent task value passed to Upsert verbatim.
func (s *capturingTaskStore) lastUpserted() humantask.HumanTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// userTaskDef returns a one-user-task definition: start → review (role
// "reviewer") → end. opts extends the user task (e.g. with a deadline).
func userTaskDef(id string, opts ...activity.UserTaskOption) *model.ProcessDefinition {
	all := append([]activity.UserTaskOption{activity.WithEligibleRoles("reviewer")}, opts...)
	return &model.ProcessDefinition{
		ID:      id,
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("review", all...),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "review"},
			{ID: "f2", Source: "review", Target: "end"},
		},
	}
}

// TestResolveHumanCandidatesIsolatesResolverState asserts that the actors an
// [humantask.ActorResolver] returns are deep-copied before they are written onto
// the task that the step commits. Without the copy, a consumer mutating its
// resolver registry retroactively rewrites the candidate list of an ALREADY
// COMMITTED instance — that list is audit data (ADR-0147). The engine's own
// ingest path (handleHumanCandidatesResolved) clones for exactly this reason, so
// the two paths must agree.
func TestResolveHumanCandidatesIsolatesResolverState(t *testing.T) {
	t.Parallel()

	// reviewerActors is the resolver's registry content, freshly allocated per case
	// so one case's mutation cannot reach another's.
	reviewerActors := func() []authz.Actor {
		return []authz.Actor{{
			ID:         "alice",
			Roles:      []string{"reviewer"},
			Attributes: map[string]any{"dept": "ops"},
		}}
	}

	type testCase struct {
		name       string
		actors     []authz.Actor              // what the resolver returns
		mutate     func(actors []authz.Actor) // post-commit registry mutation; nil ⇒ none
		assertFunc func(t *testing.T, committed, upserted humantask.HumanTask)
	}

	cases := []testCase{
		{
			name:   "role rewritten in the resolver's registry",
			actors: reviewerActors(),
			mutate: func(actors []authz.Actor) { actors[0].Roles[0] = "hacked" },
			assertFunc: func(t *testing.T, committed, upserted humantask.HumanTask) {
				require.Len(t, committed.Candidates, 1)
				require.Len(t, upserted.Candidates, 1)
				assert.Equal(t, []string{"reviewer"}, committed.Candidates[0].Roles,
					"committed candidate roles must not follow the resolver's registry")
				assert.Equal(t, []string{"reviewer"}, upserted.Candidates[0].Roles,
					"the task handed to the store must not follow the resolver's registry")
			},
		},
		{
			name:   "attribute rewritten in the resolver's registry",
			actors: reviewerActors(),
			mutate: func(actors []authz.Actor) { actors[0].Attributes["dept"] = "hacked" },
			assertFunc: func(t *testing.T, committed, upserted humantask.HumanTask) {
				require.Len(t, committed.Candidates, 1)
				require.Len(t, upserted.Candidates, 1)
				assert.Equal(t, "ops", committed.Candidates[0].Attributes["dept"])
				assert.Equal(t, "ops", upserted.Candidates[0].Attributes["dept"])
			},
		},
		{
			name:   "actor replaced in the resolver's slice",
			actors: reviewerActors(),
			mutate: func(actors []authz.Actor) { actors[0] = authz.Actor{ID: "mallory"} },
			assertFunc: func(t *testing.T, committed, upserted humantask.HumanTask) {
				require.Len(t, committed.Candidates, 1)
				require.Len(t, upserted.Candidates, 1)
				assert.Equal(t, "alice", committed.Candidates[0].ID,
					"the candidate slice must be independently allocated")
				assert.Equal(t, "alice", upserted.Candidates[0].ID)
			},
		},
		{
			name:   "resolver returning no candidates parks the task with none",
			actors: nil,
			assertFunc: func(t *testing.T, committed, upserted humantask.HumanTask) {
				assert.Empty(t, committed.Candidates, "an empty resolution is not an error")
				assert.Empty(t, upserted.Candidates)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			resolver := &sliceActorResolver{actors: tc.actors}
			tasks := newCapturingTaskStore()
			store := runtimetest.MustMemStore(t)
			driver := runtimetest.MustProcessDriver(t, nil, store,
				runtime.WithHumanTasks(resolver, tasks, nil))

			parked, err := driver.Drive(ctx, userTaskDef("candidate-isolation"), "cand-iso-1", nil)
			require.NoError(t, err)
			require.Equal(t, engine.StatusRunning, parked.Status, "instance must park on the user task")
			require.Len(t, parked.Tasks, 1)

			upserted := tasks.lastUpserted()
			require.Equal(t, parked.Tasks[0].TaskID, upserted.TaskID, "the parked task must reach the store")

			// The consumer mutates its registry AFTER the task was committed.
			if tc.mutate != nil {
				tc.mutate(resolver.actors)
			}

			tc.assertFunc(t, parked.Tasks[0], upserted)
		})
	}
}

// TestPerformAwaitHumanProjectsDueAt asserts that the AwaitHuman projection
// carries the engine-computed deadline into the task store. The engine stamps
// DueAt on the committed task record for a deadline-bearing user task; if perform
// drops it, every store-driven inbox / SLA view shows a task with no deadline
// until an unrelated UpdateTask happens to overwrite the row.
func TestPerformAwaitHumanProjectsDueAt(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	startAt := time.Date(2026, 6, 26, 9, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	def := &model.ProcessDefinition{
		ID:      "deadline-projection",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("review",
				activity.WithEligibleRoles("reviewer"),
				activity.WithWaitDeadline(schedule.AfterExpr(`"30m"`), "escalate"),
			),
			event.NewEnd("end"),
			event.NewEnd("end-escalated"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "review"},
			{ID: "f2", Source: "review", Target: "end"},
			{ID: "escalate", Source: "review", Target: "end-escalated"},
		},
	}

	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"reviewer": {{ID: "alice", Roles: []string{"reviewer"}}},
	})
	tasks := newCapturingTaskStore()
	driver := runtimetest.MustProcessDriver(t, nil, runtimetest.MustMemStore(t),
		runtime.WithClock(fc),
		runtime.WithScheduler(processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))),
		runtime.WithHumanTasks(resolver, tasks, nil))

	parked, err := driver.Drive(ctx, def, "due-projection-1", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status, "instance must park on the user task")
	require.Len(t, parked.Tasks, 1)
	engineDue := parked.Tasks[0].DueAt
	require.NotNil(t, engineDue, "engine must stamp DueAt for a deadline-bearing user task")
	assert.True(t, engineDue.Equal(startAt.Add(30*time.Minute)), "engine DueAt = %s", engineDue)

	// Resolve the task id from state — never predict an engine-minted id (ADR-0149).
	stored, err := tasks.Get(ctx, parked.Tasks[0].TaskID)
	require.NoError(t, err)
	require.NotNil(t, stored.DueAt, "the stored task must carry the deadline")
	assert.True(t, stored.DueAt.Equal(*engineDue), "stored DueAt = %s, engine DueAt = %s", stored.DueAt, engineDue)

	// The value handed to the store must own its DueAt, not point into engine state.
	upserted := tasks.lastUpserted()
	require.NotNil(t, upserted.DueAt)
	assert.NotSame(t, engineDue, upserted.DueAt, "DueAt must be copied by pointee, not aliased")
	assert.True(t, upserted.DueAt.Equal(*engineDue))
}
