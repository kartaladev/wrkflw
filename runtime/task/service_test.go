package task_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/runtime/task"
)

// TestTaskServiceRejectsIneligibleActor verifies that Claim returns ErrNotAuthorized
// when the actor does not have the required role.
func TestTaskServiceRejectsIneligibleActor(t *testing.T) {
	ctx := t.Context()

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	stranger := authz.Actor{ID: "bob", Roles: []string{"viewer"}}

	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"manager": {manager},
	})
	az := authz.RoleAuthorizer{}

	r := runtimetest.MustProcessDriver(t, nil, runtimetest.MustMemStore(t),
		runtime.WithHumanTasks(resolver, taskStore, az),
	)

	def := runtimetest.ApprovalDef()
	_, err := r.Drive(ctx, def, "inst-2", nil)
	require.NoError(t, err)

	claimable, err := taskStore.ClaimableBy(ctx, manager)
	require.NoError(t, err)
	require.Len(t, claimable, 1)

	svc := runtimetest.MustTaskService(t, taskStore, az)
	_, err = svc.Claim(ctx, claimable[0].TaskID, stranger)
	require.Error(t, err)
	assert.ErrorIs(t, err, authz.ErrNotAuthorized)
}

// TestTaskServiceReassign verifies that Reassign returns a HumanReassigned trigger
// when the by actor is authorized, and that the task must already be CLAIMED by
// the from actor before it can be reassigned.
//
// Authorization policy note: Reassign currently uses task eligibility (the same
// check as Claim) — a distinct admin/reassign-privilege model is deferred. The
// by actor holding the task role is therefore intentional, not incidental.
func TestTaskServiceReassign(t *testing.T) {
	ctx := t.Context()

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	admin := authz.Actor{ID: "admin", Roles: []string{"manager"}}

	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"manager": {manager, admin},
	})
	az := authz.RoleAuthorizer{}

	r := runtimetest.MustProcessDriver(t, nil, runtimetest.MustMemStore(t),
		runtime.WithHumanTasks(resolver, taskStore, az),
	)

	def := runtimetest.ApprovalDef()
	_, err := r.Drive(ctx, def, "inst-3", nil)
	require.NoError(t, err)

	claimable, err := taskStore.ClaimableBy(ctx, manager)
	require.NoError(t, err)
	require.Len(t, claimable, 1)
	taskID := claimable[0].TaskID

	svc := runtimetest.MustTaskService(t, taskStore, az)

	// The task must be CLAIMED by the from actor before reassignment is allowed.
	// Claim it first so ClaimedBy == manager.ID, then reassign from manager to admin.
	claimTrg, err := svc.Claim(ctx, taskID, manager)
	require.NoError(t, err)
	_, err = r.ApplyTrigger(ctx, def, "inst-3", claimTrg)
	require.NoError(t, err)

	trg, err := svc.Reassign(ctx, taskID, manager.ID, admin.ID, admin)
	require.NoError(t, err)
	reassigned, ok := trg.(engine.HumanReassigned)
	require.True(t, ok)
	assert.Equal(t, taskID, reassigned.TaskID)
	assert.Equal(t, manager.ID, reassigned.From)
	assert.Equal(t, admin.ID, reassigned.To)
	assert.Equal(t, admin.ID, reassigned.By.ID)

	// Verify: reassigning with a from that does NOT match the current claimant
	// must be rejected before any trigger is issued, preventing a false From in
	// the journal.
	trg, err = svc.Reassign(ctx, taskID, "wrong-claimant", "someone-else", admin)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wrong-claimant")
	assert.Nil(t, trg, "no trigger must be returned when from does not match the current claimant")
}

// TestTaskServiceReassignRejectsUnauthorized verifies that Reassign returns
// ErrNotAuthorized when the acting actor lacks the required role, and that no
// trigger (side effect) is returned.
//
// The task must first be claimed by the from actor (ClaimedBy must match) so
// that the authorization check — not the claimant check — is the failing gate.
func TestTaskServiceReassignRejectsUnauthorized(t *testing.T) {
	ctx := t.Context()

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	stranger := authz.Actor{ID: "bob", Roles: []string{"viewer"}}

	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"manager": {manager},
	})
	az := authz.RoleAuthorizer{}

	r := runtimetest.MustProcessDriver(t, nil, runtimetest.MustMemStore(t),
		runtime.WithHumanTasks(resolver, taskStore, az),
	)

	_, err := r.Drive(ctx, runtimetest.ApprovalDef(), "inst-reassign-reject", nil)
	require.NoError(t, err)

	claimable, err := taskStore.ClaimableBy(ctx, manager)
	require.NoError(t, err)
	require.Len(t, claimable, 1)
	taskID := claimable[0].TaskID

	svc := runtimetest.MustTaskService(t, taskStore, az)

	// Claim the task first so ClaimedBy == manager.ID; only then does the
	// authorization check become the failing gate (from == ClaimedBy passes,
	// but stranger lacks the required role).
	claimTrg, err := svc.Claim(ctx, taskID, manager)
	require.NoError(t, err)
	_, err = r.ApplyTrigger(ctx, runtimetest.ApprovalDef(), "inst-reassign-reject", claimTrg)
	require.NoError(t, err)

	trg, err := svc.Reassign(ctx, taskID, manager.ID, stranger.ID, stranger)
	require.Error(t, err)
	assert.ErrorIs(t, err, authz.ErrNotAuthorized)
	assert.Nil(t, trg, "no trigger must be returned when authorization is rejected")
}

// TestTaskServiceCompleteRejectsUnauthorized verifies that Complete returns
// ErrNotAuthorized when the acting actor lacks the required role, and that no
// trigger (side effect) is returned.
func TestTaskServiceCompleteRejectsUnauthorized(t *testing.T) {
	ctx := t.Context()

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	stranger := authz.Actor{ID: "bob", Roles: []string{"viewer"}}

	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"manager": {manager},
	})
	az := authz.RoleAuthorizer{}

	r := runtimetest.MustProcessDriver(t, nil, runtimetest.MustMemStore(t),
		runtime.WithHumanTasks(resolver, taskStore, az),
	)

	_, err := r.Drive(ctx, runtimetest.ApprovalDef(), "inst-complete-reject", nil)
	require.NoError(t, err)

	claimable, err := taskStore.ClaimableBy(ctx, manager)
	require.NoError(t, err)
	require.Len(t, claimable, 1)
	taskID := claimable[0].TaskID

	svc := runtimetest.MustTaskService(t, taskStore, az)
	trg, err := svc.Complete(ctx, taskID, stranger, engine.CompletionInput{Output: map[string]any{"approved": false}})
	require.Error(t, err)
	assert.ErrorIs(t, err, authz.ErrNotAuthorized)
	assert.Nil(t, trg, "no trigger must be returned when authorization is rejected")
}

// TestTaskServiceGetNotFound verifies that Claim/Complete return an error when the
// task id does not exist in the store.
func TestTaskServiceGetNotFound(t *testing.T) {
	ctx := t.Context()
	store := humantask.NewMemTaskStore()
	az := authz.AllowAll{}
	svc := runtimetest.MustTaskService(t, store, az)

	actor := authz.Actor{ID: "alice"}
	_, err := svc.Claim(ctx, "no-such-token", actor)
	require.Error(t, err)
	assert.ErrorIs(t, err, humantask.ErrTaskNotFound)

	_, err = svc.Complete(ctx, "no-such-token", actor, engine.CompletionInput{})
	require.Error(t, err)
	assert.ErrorIs(t, err, humantask.ErrTaskNotFound)

	_, err = svc.Reassign(ctx, "no-such-token", "a", "b", actor)
	require.Error(t, err)
	assert.ErrorIs(t, err, humantask.ErrTaskNotFound)
}

// TestTaskService_Claim_AttributeOverVars verifies that attribute predicates
// referencing process variables (vars["region"]) are correctly enforced at Claim
// time. This test exercises the full vars-plumbing path: task.Vars must be
// populated and passed to the Authorizer — otherwise the expr evaluates against
// a nil map and the EU predicate errors/denies even for eligible actors.
func TestTaskService_Claim_AttributeOverVars(t *testing.T) {
	cases := map[string]struct {
		vars   map[string]any
		assert func(t *testing.T, err error)
	}{
		"matching region claims": {
			vars:   map[string]any{"region": "EU"},
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		"non-matching region denied": {
			vars: map[string]any{"region": "US"},
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := humantask.NewMemTaskStore()
			require.NoError(t, store.Upsert(t.Context(), humantask.HumanTask{
				TaskID:      "tok-attr-1",
				Eligibility: authz.AuthzSpec{Attribute: `vars["region"] == "EU"`},
				Vars:        tc.vars,
				State:       humantask.Unclaimed,
			}))
			svc := runtimetest.MustTaskService(t, store, authz.RoleAuthorizer{})
			_, err := svc.Claim(t.Context(), "tok-attr-1", authz.Actor{ID: "alice"})
			tc.assert(t, err)
		})
	}
}

// TestNewTaskServiceDefaultClockNoPanic verifies that NewTaskService without
// any clock option does not panic and returns a non-nil TaskService.
func TestNewTaskServiceDefaultClockNoPanic(t *testing.T) {
	store := humantask.NewMemTaskStore()
	az := authz.AllowAll{}
	svc := runtimetest.MustTaskService(t, store, az)
	assert.NotNil(t, svc)
}

// TestNewTaskServiceWithClockOption verifies that WithClock injects
// a fake clock whose time flows through to task-lifecycle trigger timestamps.
func TestNewTaskServiceWithClockOption(t *testing.T) {
	ctx := t.Context()

	fakeTime := time.Unix(1000, 0)
	fake := clockwork.NewFakeClockAt(fakeTime)

	store := humantask.NewMemTaskStore()
	require.NoError(t, store.Upsert(ctx, humantask.HumanTask{
		TaskID:      "tok-clock-1",
		Eligibility: authz.AuthzSpec{},
		State:       humantask.Unclaimed,
	}))

	az := authz.AllowAll{}
	svc := runtimetest.MustTaskService(t, store, az, task.WithClock(fake))
	assert.NotNil(t, svc)

	// Claim stamps the trigger's At field from the clock; verify fake time flows through.
	trg, err := svc.Claim(ctx, "tok-clock-1", authz.Actor{ID: "alice"})
	require.NoError(t, err)
	claimed, ok := trg.(engine.HumanClaimed)
	require.True(t, ok)
	assert.Equal(t, fakeTime, claimed.OccurredAt())
}

func TestNewTaskServiceFailsFast(t *testing.T) {
	t.Parallel()
	store := humantask.NewMemTaskStore()
	az := authz.RoleAuthorizer{}
	cases := []struct {
		name   string
		store  humantask.TaskStore
		az     authz.Authorizer
		assert func(t *testing.T, svc *task.TaskService, err error)
	}{
		{
			name:  "nil store",
			store: nil,
			az:    az,
			assert: func(t *testing.T, svc *task.TaskService, err error) {
				require.ErrorIs(t, err, kernel.ErrNilDependency)
				require.Nil(t, svc)
			},
		},
		{
			name:  "nil authorizer",
			store: store,
			az:    nil,
			assert: func(t *testing.T, svc *task.TaskService, err error) {
				require.ErrorIs(t, err, kernel.ErrNilDependency)
				require.Nil(t, svc)
			},
		},
		{
			name:  "valid args",
			store: store,
			az:    az,
			assert: func(t *testing.T, svc *task.TaskService, err error) {
				require.NoError(t, err)
				require.NotNil(t, svc)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, err := task.NewTaskService(tc.store, tc.az)
			tc.assert(t, svc, err)
		})
	}
}

// TestTaskServiceRefreshCandidates verifies the candidate-refresh operation
// (ADR-0147): candidates are a projection resolved at task-creation time, and the
// underlying actor registry is not static, so a caller must be able to re-resolve
// an open task's eligible actors. Refresh re-runs the ActorResolver against the
// task's stored eligibility and variables and returns the trigger that replaces
// the list; it never mutates the store directly.
func TestTaskServiceRefreshCandidates(t *testing.T) {
	t.Parallel()

	fakeTime := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	jane := authz.Actor{ID: "u-jane", Roles: []string{"manager"}, Attributes: map[string]any{"email": "jane@acme.com"}}
	mike := authz.Actor{ID: "u-mike", Roles: []string{"manager"}}

	openTask := humantask.HumanTask{
		TaskID:      "tok-refresh",
		InstanceID:  "i1",
		NodeID:      "approve",
		Eligibility: authz.AuthzSpec{Roles: []string{"manager"}},
		Candidates:  []authz.Actor{jane},
		State:       humantask.Unclaimed,
	}

	type testCase struct {
		name     string
		task     humantask.HumanTask
		resolver humantask.ActorResolver
		az       authz.Authorizer
		taskID   string
		// by defaults to an actor holding the task's "manager" role when zero.
		by     authz.Actor
		assert func(t *testing.T, trg engine.Trigger, err error)
	}

	cases := []testCase{
		{
			name: "re-resolves the eligible actors and returns the replacing trigger",
			task: openTask,
			// The registry has grown since the task was created: mike now holds the role.
			resolver: humantask.NewStaticActorResolver(map[string][]authz.Actor{
				"manager": {jane, mike},
			}),
			az:     authz.AllowAll{},
			taskID: "tok-refresh",
			assert: func(t *testing.T, trg engine.Trigger, err error) {
				require.NoError(t, err)
				resolved, ok := trg.(engine.HumanCandidatesResolved)
				require.True(t, ok, "expected HumanCandidatesResolved, got %T", trg)
				require.Equal(t, "tok-refresh", resolved.TaskID)
				require.Equal(t, []authz.Actor{jane, mike}, resolved.Candidates)
				require.Equal(t, fakeTime, resolved.OccurredAt())
			},
		},
		{
			name: "a shrinking registry is reflected",
			task: openTask,
			resolver: humantask.NewStaticActorResolver(map[string][]authz.Actor{
				"manager": {mike},
			}),
			az:     authz.AllowAll{},
			taskID: "tok-refresh",
			assert: func(t *testing.T, trg engine.Trigger, err error) {
				require.NoError(t, err)
				require.Equal(t, []authz.Actor{mike}, trg.(engine.HumanCandidatesResolved).Candidates)
			},
		},
		{
			name:     "an unknown task is rejected",
			task:     openTask,
			resolver: humantask.NewStaticActorResolver(nil),
			az:       authz.AllowAll{},
			taskID:   "no-such-task",
			assert: func(t *testing.T, _ engine.Trigger, err error) {
				require.ErrorIs(t, err, humantask.ErrTaskNotFound)
			},
		},
		{
			name: "a closed task is rejected so a completed audit is not rewritten",
			task: func() humantask.HumanTask {
				closed := openTask
				closed.State = humantask.Completed
				return closed
			}(),
			resolver: humantask.NewStaticActorResolver(map[string][]authz.Actor{"manager": {mike}}),
			az:       authz.AllowAll{},
			taskID:   "tok-refresh",
			assert: func(t *testing.T, _ engine.Trigger, err error) {
				require.ErrorIs(t, err, task.ErrTaskNotOpen)
				// The runtime sentinel is an alias of the engine's, and it wraps
				// ErrInvalidTransition — which is what carries it to HTTP 422
				// instead of falling through httpcore.ClassifyError to a 500 with
				// an empty body. Pinned here because nothing else in this package
				// would notice the alias being replaced by a fresh errors.New.
				// See ADR-0165.
				assert.ErrorIs(t, err, engine.ErrTaskNotOpen)
				assert.ErrorIs(t, err, engine.ErrInvalidTransition)
			},
		},
		{
			name:     "an unauthorized caller is rejected",
			task:     openTask,
			resolver: humantask.NewStaticActorResolver(map[string][]authz.Actor{"manager": {mike}}),
			az:       authz.RoleAuthorizer{},
			taskID:   "tok-refresh",
			by:       authz.Actor{ID: "outsider"},
			assert: func(t *testing.T, _ engine.Trigger, err error) {
				require.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
		{
			name:     "no resolver configured is rejected",
			task:     openTask,
			resolver: nil,
			az:       authz.AllowAll{},
			taskID:   "tok-refresh",
			assert: func(t *testing.T, _ engine.Trigger, err error) {
				require.ErrorIs(t, err, task.ErrNoActorResolver)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			store := humantask.NewMemTaskStore()
			require.NoError(t, store.Upsert(ctx, tc.task))

			opts := []task.TaskServiceOption{task.WithClock(clockwork.NewFakeClockAt(fakeTime))}
			if tc.resolver != nil {
				opts = append(opts, task.WithActorResolver(tc.resolver))
			}
			svc, err := task.NewTaskService(store, tc.az, opts...)
			require.NoError(t, err)

			by := tc.by
			if by.ID == "" {
				by = authz.Actor{ID: "admin", Roles: []string{"manager"}}
			}
			trg, err := svc.RefreshCandidates(ctx, tc.taskID, by)
			tc.assert(t, trg, err)
		})
	}
}

// TestTaskServiceCompleteCarriesCompletion verifies that the completion payload —
// outcome, note, and output — reaches the engine trigger intact (ADR-0146). The
// outcome and note are audit-bearing, so a layer that silently dropped them would
// leave the task's completion record incomplete with no visible failure.
func TestTaskServiceCompleteCarriesCompletion(t *testing.T) {
	t.Parallel()

	fakeTime := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	actor := authz.Actor{ID: "u-jane", Roles: []string{"manager"}}

	type testCase struct {
		name       string
		completion engine.CompletionInput
		assert     func(t *testing.T, got engine.HumanCompleted)
	}

	cases := []testCase{
		{
			name:       "outcome, note and output all reach the trigger",
			completion: engine.CompletionInput{Outcome: "approve", Note: "budget confirmed", Output: map[string]any{"amount": 100}},
			assert: func(t *testing.T, got engine.HumanCompleted) {
				assert.Equal(t, "approve", got.Outcome)
				assert.Equal(t, "budget confirmed", got.Note)
				assert.Equal(t, map[string]any{"amount": 100}, got.Output)
				assert.Equal(t, actor, got.Actor)
			},
		},
		{
			name:       "an empty completion produces an empty outcome and note",
			completion: engine.CompletionInput{},
			assert: func(t *testing.T, got engine.HumanCompleted) {
				assert.Empty(t, got.Outcome)
				assert.Empty(t, got.Note)
				assert.Nil(t, got.Output)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			store := humantask.NewMemTaskStore()
			require.NoError(t, store.Upsert(ctx, humantask.HumanTask{
				TaskID: "tok-complete", InstanceID: "i1", NodeID: "approve",
				Eligibility: authz.AuthzSpec{}, State: humantask.Unclaimed,
			}))
			svc, err := task.NewTaskService(store, authz.AllowAll{},
				task.WithClock(clockwork.NewFakeClockAt(fakeTime)))
			require.NoError(t, err)

			trg, err := svc.Complete(ctx, "tok-complete", actor, tc.completion)
			require.NoError(t, err)
			completed, ok := trg.(engine.HumanCompleted)
			require.True(t, ok, "expected HumanCompleted, got %T", trg)
			tc.assert(t, completed)
		})
	}
}

// recordingActorResolver is an [humantask.ActorResolver] test double that captures
// the deadline of the context it is invoked with. When block is true it never
// returns until that context is done and then reports the context's error,
// standing in for an unresponsive directory service.
//
// Its fields are written inside Candidates and read only after the call that drove
// it has returned, on the same goroutine, so no synchronisation is needed.
type recordingActorResolver struct {
	block       bool
	actors      []authz.Actor
	deadline    time.Time
	hadDeadline bool
}

// Candidates records the context's deadline and then either blocks until the
// context is done or returns the configured actors immediately.
func (r *recordingActorResolver) Candidates(ctx context.Context, _ authz.AuthzSpec, _ map[string]any) ([]authz.Actor, error) {
	r.deadline, r.hadDeadline = ctx.Deadline()
	if r.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return r.actors, nil
}

// TestTaskServiceRefreshCandidatesBoundsResolver verifies that the single
// ActorResolver lookup performed by RefreshCandidates runs under the service's
// candidate-resolve timeout, matching the bound the ProcessDriver applies to the
// identical lookup (runtime.WithCandidateResolveTimeout).
//
// Without the bound an unresponsive directory service holds the calling goroutine
// for as long as the caller's context allows — forever for a caller that passes a
// background context — so the two derivations of the same candidate list would
// fail on entirely different schedules.
func TestTaskServiceRefreshCandidatesBoundsResolver(t *testing.T) {
	t.Parallel()

	fakeTime := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	jane := authz.Actor{ID: "u-jane", Roles: []string{"manager"}}

	openTask := humantask.HumanTask{
		TaskID:      "tok-bound",
		InstanceID:  "i1",
		NodeID:      "approve",
		Eligibility: authz.AuthzSpec{Roles: []string{"manager"}},
		State:       humantask.Unclaimed,
	}

	type testCase struct {
		name     string
		resolver *recordingActorResolver
		opts     []task.TaskServiceOption
		ctx      func(ctx context.Context) context.Context // nil means identity
		assert   func(t *testing.T, res *recordingActorResolver, trg engine.Trigger, err error)
	}

	cases := []testCase{
		{
			name:     "a prompt resolver is unaffected and runs under the default bound",
			resolver: &recordingActorResolver{actors: []authz.Actor{jane}},
			assert: func(t *testing.T, res *recordingActorResolver, trg engine.Trigger, err error) {
				require.NoError(t, err)
				resolved, ok := trg.(engine.HumanCandidatesResolved)
				require.True(t, ok, "expected HumanCandidatesResolved, got %T", trg)
				assert.Equal(t, []authz.Actor{jane}, resolved.Candidates)
				require.True(t, res.hadDeadline, "the resolver must run under the default bound")
				remaining := time.Until(res.deadline)
				assert.Positive(t, remaining, "the default bound must not already have expired")
				assert.LessOrEqual(t, remaining, 10*time.Second, "the default bound must match the ProcessDriver's 10s")
			},
		},
		{
			name:     "a resolver that outlives the bound fails with the deadline error",
			resolver: &recordingActorResolver{block: true},
			opts:     []task.TaskServiceOption{task.WithCandidateResolveTimeout(50 * time.Millisecond)},
			assert: func(t *testing.T, res *recordingActorResolver, trg engine.Trigger, err error) {
				require.ErrorIs(t, err, context.DeadlineExceeded)
				assert.Nil(t, trg, "no trigger may be returned when resolution times out")
				assert.True(t, res.hadDeadline, "the resolver must have been given the bounded context")
			},
		},
		{
			name:     "a non-positive timeout disables the bound",
			resolver: &recordingActorResolver{block: true},
			opts:     []task.TaskServiceOption{task.WithCandidateResolveTimeout(0)},
			ctx: func(ctx context.Context) context.Context {
				// Nothing but the caller's own cancellation can end the lookup once
				// the bound is disabled, so drive it from the caller side.
				cctx, cancel := context.WithCancel(ctx)
				time.AfterFunc(50*time.Millisecond, cancel)
				return cctx
			},
			assert: func(t *testing.T, res *recordingActorResolver, _ engine.Trigger, err error) {
				require.ErrorIs(t, err, context.Canceled)
				assert.NotErrorIs(t, err, context.DeadlineExceeded, "a non-positive timeout must not apply a deadline")
				assert.False(t, res.hadDeadline, "no deadline may be attached when the bound is disabled")
			},
		},
		{
			name:     "an already-cancelled caller context is not masked by the bound",
			resolver: &recordingActorResolver{block: true},
			ctx: func(ctx context.Context) context.Context {
				cctx, cancel := context.WithCancel(ctx)
				cancel()
				return cctx
			},
			assert: func(t *testing.T, _ *recordingActorResolver, _ engine.Trigger, err error) {
				require.ErrorIs(t, err, context.Canceled)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			store := humantask.NewMemTaskStore()
			require.NoError(t, store.Upsert(ctx, openTask))

			opts := append([]task.TaskServiceOption{
				task.WithClock(clockwork.NewFakeClockAt(fakeTime)),
				task.WithActorResolver(tc.resolver),
			}, tc.opts...)
			svc, err := task.NewTaskService(store, authz.AllowAll{}, opts...)
			require.NoError(t, err)

			if tc.ctx != nil {
				ctx = tc.ctx(ctx)
			}

			trg, err := svc.RefreshCandidates(ctx, openTask.TaskID, authz.Actor{ID: "admin", Roles: []string{"manager"}})
			tc.assert(t, tc.resolver, trg, err)
		})
	}
}

// TestTaskServiceReassignRejectsAnEmptyTarget verifies that Reassign refuses a
// reassignment naming no actor BEFORE it reads the task store, authorizes, or
// records the "reassigned" metric. engine.Step refuses the same shape
// (ADR-0183), but only after the service has already counted a reassignment that
// never happened and paid for a store read plus an authz round-trip.
//
// The unknown-task row is the discriminator: it can only pass if the guard runs
// ahead of the store lookup, which otherwise answers ErrTaskNotFound. The
// non-empty control row keeps the guard from passing by refusing everything.
func TestTaskServiceReassignRejectsAnEmptyTarget(t *testing.T) {
	t.Parallel()

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	openTask := humantask.HumanTask{
		TaskID:      "tok-reassign-empty",
		InstanceID:  "inst-reassign-empty",
		NodeID:      "approve",
		State:       humantask.Claimed,
		Claim:       &humantask.Claim{Actor: manager, At: time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)},
		Eligibility: authz.AuthzSpec{Roles: []string{"manager"}},
	}

	type testCase struct {
		name   string
		taskID string
		to     string
		assert func(t *testing.T, trg engine.Trigger, err error)
	}

	cases := []testCase{
		{
			name:   "an empty target is refused",
			taskID: openTask.TaskID,
			to:     "",
			assert: func(t *testing.T, trg engine.Trigger, err error) {
				require.ErrorIs(t, err, engine.ErrEmptyReassignTarget)
				assert.Nil(t, trg, "no trigger may be issued for a reassignment naming nobody")
			},
		},
		{
			name:   "an empty target is refused before the task is even looked up",
			taskID: "no-such-task",
			to:     "",
			assert: func(t *testing.T, trg engine.Trigger, err error) {
				require.ErrorIs(t, err, engine.ErrEmptyReassignTarget)
				assert.NotErrorIs(t, err, humantask.ErrTaskNotFound,
					"the guard must run ahead of the store read")
				assert.Nil(t, trg)
			},
		},
		{
			name:   "a named target still yields a trigger",
			taskID: openTask.TaskID,
			to:     "bob",
			assert: func(t *testing.T, trg engine.Trigger, err error) {
				require.NoError(t, err)
				reassigned, ok := trg.(engine.HumanReassigned)
				require.True(t, ok, "expected HumanReassigned, got %T", trg)
				assert.Equal(t, "bob", reassigned.To)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			store := humantask.NewMemTaskStore()
			require.NoError(t, store.Upsert(ctx, openTask))
			svc := runtimetest.MustTaskService(t, store, authz.RoleAuthorizer{})

			trg, err := svc.Reassign(ctx, tc.taskID, manager.ID, tc.to, manager)
			tc.assert(t, trg, err)
		})
	}
}
