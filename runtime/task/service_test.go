package task_test

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
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

	r := runtimetest.MustRunner(t, nil, runtimetest.MustMemStore(t),
		runtime.WithHumanTasks(resolver, taskStore, az),
	)

	def := runtimetest.ApprovalDef()
	_, err := r.Run(ctx, def, "inst-2", nil)
	require.NoError(t, err)

	claimable, err := taskStore.ClaimableBy(ctx, manager)
	require.NoError(t, err)
	require.Len(t, claimable, 1)

	svc := runtimetest.MustTaskService(t, taskStore, az)
	_, err = svc.Claim(ctx, claimable[0].TaskToken, stranger)
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

	r := runtimetest.MustRunner(t, nil, runtimetest.MustMemStore(t),
		runtime.WithHumanTasks(resolver, taskStore, az),
	)

	def := runtimetest.ApprovalDef()
	_, err := r.Run(ctx, def, "inst-3", nil)
	require.NoError(t, err)

	claimable, err := taskStore.ClaimableBy(ctx, manager)
	require.NoError(t, err)
	require.Len(t, claimable, 1)
	taskToken := claimable[0].TaskToken

	svc := runtimetest.MustTaskService(t, taskStore, az)

	// The task must be CLAIMED by the from actor before reassignment is allowed.
	// Claim it first so ClaimedBy == manager.ID, then reassign from manager to admin.
	claimTrg, err := svc.Claim(ctx, taskToken, manager)
	require.NoError(t, err)
	_, err = r.Deliver(ctx, def, "inst-3", claimTrg)
	require.NoError(t, err)

	trg, err := svc.Reassign(ctx, taskToken, manager.ID, admin.ID, admin)
	require.NoError(t, err)
	reassigned, ok := trg.(engine.HumanReassigned)
	require.True(t, ok)
	assert.Equal(t, taskToken, reassigned.TaskToken)
	assert.Equal(t, manager.ID, reassigned.From)
	assert.Equal(t, admin.ID, reassigned.To)
	assert.Equal(t, admin.ID, reassigned.By.ID)

	// Verify: reassigning with a from that does NOT match the current claimant
	// must be rejected before any trigger is issued, preventing a false From in
	// the journal.
	trg, err = svc.Reassign(ctx, taskToken, "wrong-claimant", "someone-else", admin)
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

	r := runtimetest.MustRunner(t, nil, runtimetest.MustMemStore(t),
		runtime.WithHumanTasks(resolver, taskStore, az),
	)

	_, err := r.Run(ctx, runtimetest.ApprovalDef(), "inst-reassign-reject", nil)
	require.NoError(t, err)

	claimable, err := taskStore.ClaimableBy(ctx, manager)
	require.NoError(t, err)
	require.Len(t, claimable, 1)
	taskToken := claimable[0].TaskToken

	svc := runtimetest.MustTaskService(t, taskStore, az)

	// Claim the task first so ClaimedBy == manager.ID; only then does the
	// authorization check become the failing gate (from == ClaimedBy passes,
	// but stranger lacks the required role).
	claimTrg, err := svc.Claim(ctx, taskToken, manager)
	require.NoError(t, err)
	_, err = r.Deliver(ctx, runtimetest.ApprovalDef(), "inst-reassign-reject", claimTrg)
	require.NoError(t, err)

	trg, err := svc.Reassign(ctx, taskToken, manager.ID, stranger.ID, stranger)
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

	r := runtimetest.MustRunner(t, nil, runtimetest.MustMemStore(t),
		runtime.WithHumanTasks(resolver, taskStore, az),
	)

	_, err := r.Run(ctx, runtimetest.ApprovalDef(), "inst-complete-reject", nil)
	require.NoError(t, err)

	claimable, err := taskStore.ClaimableBy(ctx, manager)
	require.NoError(t, err)
	require.Len(t, claimable, 1)
	taskToken := claimable[0].TaskToken

	svc := runtimetest.MustTaskService(t, taskStore, az)
	trg, err := svc.Complete(ctx, taskToken, stranger, map[string]any{"approved": false})
	require.Error(t, err)
	assert.ErrorIs(t, err, authz.ErrNotAuthorized)
	assert.Nil(t, trg, "no trigger must be returned when authorization is rejected")
}

// TestTaskServiceGetNotFound verifies that Claim/Complete return an error when the
// task token does not exist in the store.
func TestTaskServiceGetNotFound(t *testing.T) {
	ctx := t.Context()
	store := humantask.NewMemTaskStore()
	az := authz.AllowAll{}
	svc := runtimetest.MustTaskService(t, store, az)

	actor := authz.Actor{ID: "alice"}
	_, err := svc.Claim(ctx, "no-such-token", actor)
	require.Error(t, err)
	assert.ErrorIs(t, err, humantask.ErrTaskNotFound)

	_, err = svc.Complete(ctx, "no-such-token", actor, nil)
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
				TaskToken:   "tok-attr-1",
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

// TestNewTaskServiceWithClockOption verifies that WithTaskServiceClock injects
// a fake clock whose time flows through to task-lifecycle trigger timestamps.
func TestNewTaskServiceWithClockOption(t *testing.T) {
	ctx := t.Context()

	fakeTime := time.Unix(1000, 0)
	fake := clockwork.NewFakeClockAt(fakeTime)

	store := humantask.NewMemTaskStore()
	require.NoError(t, store.Upsert(ctx, humantask.HumanTask{
		TaskToken:   "tok-clock-1",
		Eligibility: authz.AuthzSpec{},
		State:       humantask.Unclaimed,
	}))

	az := authz.AllowAll{}
	svc := runtimetest.MustTaskService(t, store, az, task.WithTaskServiceClock(fake))
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
