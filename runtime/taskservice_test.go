package runtime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
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
	clk := clock.System()

	r := runtime.NewRunner(
		nil,
		clk,
		runtime.NewMemStateStore(),
		runtime.NewMemJournal(),
		runtime.NewMemOutbox(),
		resolver,
		taskStore,
		az,
	)

	def := approvalDef()
	_, err := r.Run(ctx, def, "inst-2", nil)
	require.NoError(t, err)

	claimable, err := taskStore.ClaimableBy(ctx, manager)
	require.NoError(t, err)
	require.Len(t, claimable, 1)

	svc := runtime.NewTaskService(taskStore, az, clk)
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
	clk := clock.System()

	r := runtime.NewRunner(
		nil,
		clk,
		runtime.NewMemStateStore(),
		runtime.NewMemJournal(),
		runtime.NewMemOutbox(),
		resolver,
		taskStore,
		az,
	)

	def := approvalDef()
	_, err := r.Run(ctx, def, "inst-3", nil)
	require.NoError(t, err)

	claimable, err := taskStore.ClaimableBy(ctx, manager)
	require.NoError(t, err)
	require.Len(t, claimable, 1)
	taskToken := claimable[0].TaskToken

	svc := runtime.NewTaskService(taskStore, az, clk)

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
	clk := clock.System()

	r := runtime.NewRunner(
		nil,
		clk,
		runtime.NewMemStateStore(),
		runtime.NewMemJournal(),
		runtime.NewMemOutbox(),
		resolver,
		taskStore,
		az,
	)

	_, err := r.Run(ctx, approvalDef(), "inst-reassign-reject", nil)
	require.NoError(t, err)

	claimable, err := taskStore.ClaimableBy(ctx, manager)
	require.NoError(t, err)
	require.Len(t, claimable, 1)
	taskToken := claimable[0].TaskToken

	svc := runtime.NewTaskService(taskStore, az, clk)

	// Claim the task first so ClaimedBy == manager.ID; only then does the
	// authorization check become the failing gate (from == ClaimedBy passes,
	// but stranger lacks the required role).
	claimTrg, err := svc.Claim(ctx, taskToken, manager)
	require.NoError(t, err)
	_, err = r.Deliver(ctx, approvalDef(), "inst-reassign-reject", claimTrg)
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
	clk := clock.System()

	r := runtime.NewRunner(
		nil,
		clk,
		runtime.NewMemStateStore(),
		runtime.NewMemJournal(),
		runtime.NewMemOutbox(),
		resolver,
		taskStore,
		az,
	)

	_, err := r.Run(ctx, approvalDef(), "inst-complete-reject", nil)
	require.NoError(t, err)

	claimable, err := taskStore.ClaimableBy(ctx, manager)
	require.NoError(t, err)
	require.Len(t, claimable, 1)
	taskToken := claimable[0].TaskToken

	svc := runtime.NewTaskService(taskStore, az, clk)
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
	svc := runtime.NewTaskService(store, az, clock.System())

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
