// Package humantask_test exercises MemTaskStore and StaticActorResolver.
package humantask_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
)

// makeTask is a minimal HumanTask fixture.
func makeTask(token, instanceID, nodeID string, state humantask.TaskState, claimedBy string, candidates []string, roles []string) humantask.HumanTask {
	return humantask.HumanTask{
		TaskToken:  token,
		InstanceID: instanceID,
		NodeID:     nodeID,
		Eligibility: authz.AuthzSpec{
			Roles: roles,
		},
		Candidates: candidates,
		State:      state,
		ClaimedBy:  claimedBy,
		CreatedAt:  time.Now(),
		DueAt:      nil,
	}
}

// --- MemTaskStore tests ---

func TestMemTaskStore_UpsertGet_RoundTrip(t *testing.T) {
	ctx := t.Context()
	store := humantask.NewMemTaskStore()

	task := makeTask("tok-1", "inst-1", "node-1", humantask.Unclaimed, "", []string{"actor-a"}, nil)
	require.NoError(t, store.Upsert(ctx, task))

	got, err := store.Get(ctx, "tok-1")
	require.NoError(t, err)
	assert.Equal(t, task.TaskToken, got.TaskToken)
	assert.Equal(t, task.InstanceID, got.InstanceID)
	assert.Equal(t, task.State, got.State)
}

func TestMemTaskStore_Get_Miss_ReturnsErrTaskNotFound(t *testing.T) {
	ctx := t.Context()
	store := humantask.NewMemTaskStore()

	_, err := store.Get(ctx, "nonexistent")
	require.Error(t, err)
	assert.True(t, errors.Is(err, humantask.ErrTaskNotFound), "expected ErrTaskNotFound, got: %v", err)
}

func TestMemTaskStore_Upsert_UpdatesExisting(t *testing.T) {
	ctx := t.Context()
	store := humantask.NewMemTaskStore()

	task := makeTask("tok-upd", "inst-1", "node-1", humantask.Unclaimed, "", nil, nil)
	require.NoError(t, store.Upsert(ctx, task))

	// Update to Claimed.
	task.State = humantask.Claimed
	task.ClaimedBy = "actor-x"
	require.NoError(t, store.Upsert(ctx, task))

	got, err := store.Get(ctx, "tok-upd")
	require.NoError(t, err)
	assert.Equal(t, humantask.Claimed, got.State)
	assert.Equal(t, "actor-x", got.ClaimedBy)
}

func TestMemTaskStore_AssignedTo_FiltersClaimedBy(t *testing.T) {
	ctx := t.Context()
	store := humantask.NewMemTaskStore()

	// Two tasks claimed by actor-a, one by actor-b, one unclaimed.
	tasks := []humantask.HumanTask{
		makeTask("tok-a1", "inst-1", "node-1", humantask.Claimed, "actor-a", nil, nil),
		makeTask("tok-a2", "inst-2", "node-1", humantask.Claimed, "actor-a", nil, nil),
		makeTask("tok-b1", "inst-3", "node-1", humantask.Claimed, "actor-b", nil, nil),
		makeTask("tok-unc", "inst-4", "node-1", humantask.Unclaimed, "", nil, nil),
	}
	for _, tsk := range tasks {
		require.NoError(t, store.Upsert(ctx, tsk))
	}

	got, err := store.AssignedTo(ctx, "actor-a")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "tok-a1", got[0].TaskToken, "results should be sorted by TaskToken")
	assert.Equal(t, "tok-a2", got[1].TaskToken)
}

func TestMemTaskStore_AssignedTo_EmptyWhenNone(t *testing.T) {
	ctx := t.Context()
	store := humantask.NewMemTaskStore()

	require.NoError(t, store.Upsert(ctx, makeTask("tok-1", "inst-1", "node-1", humantask.Claimed, "actor-a", nil, nil)))

	got, err := store.AssignedTo(ctx, "no-such-actor")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestMemTaskStore_ClaimableBy_CandidatesMembership(t *testing.T) {
	ctx := t.Context()
	store := humantask.NewMemTaskStore()

	actor := authz.Actor{ID: "actor-c", Roles: []string{"reviewer"}}

	// actor-c is in Candidates.
	eligible := makeTask("tok-e", "inst-1", "node-1", humantask.Unclaimed, "", []string{"actor-c", "actor-d"}, nil)
	// actor-c is NOT in Candidates and wrong role.
	ineligible := makeTask("tok-i", "inst-2", "node-1", humantask.Unclaimed, "", []string{"actor-d"}, []string{"approver"})
	// Claimed task (actor-c in Candidates but already claimed — not claimable).
	claimed := makeTask("tok-cl", "inst-3", "node-1", humantask.Claimed, "actor-d", []string{"actor-c"}, nil)

	for _, tsk := range []humantask.HumanTask{eligible, ineligible, claimed} {
		require.NoError(t, store.Upsert(ctx, tsk))
	}

	got, err := store.ClaimableBy(ctx, actor)
	require.NoError(t, err)
	require.Len(t, got, 1, "only the unclaimed task where actor-c is in Candidates")
	assert.Equal(t, "tok-e", got[0].TaskToken)
}

func TestMemTaskStore_ClaimableBy_SharedEligibilityRole(t *testing.T) {
	ctx := t.Context()
	store := humantask.NewMemTaskStore()

	actor := authz.Actor{ID: "actor-r", Roles: []string{"reviewer", "editor"}}

	// Unclaimed; actor has "reviewer" role which is in Eligibility.Roles; actor NOT in Candidates.
	roleMatch := makeTask("tok-role", "inst-1", "node-1", humantask.Unclaimed, "", []string{"other-actor"}, []string{"reviewer"})
	// Unclaimed; Eligibility.Roles requires "admin" — actor does not have it, not in Candidates.
	noMatch := makeTask("tok-no", "inst-2", "node-1", humantask.Unclaimed, "", nil, []string{"admin"})

	for _, tsk := range []humantask.HumanTask{roleMatch, noMatch} {
		require.NoError(t, store.Upsert(ctx, tsk))
	}

	got, err := store.ClaimableBy(ctx, actor)
	require.NoError(t, err)
	require.Len(t, got, 1, "only the task whose Eligibility.Roles intersects actor.Roles")
	assert.Equal(t, "tok-role", got[0].TaskToken)
}

func TestMemTaskStore_ClaimableBy_DeterministicOrder(t *testing.T) {
	ctx := t.Context()
	store := humantask.NewMemTaskStore()

	actor := authz.Actor{ID: "actor-x", Roles: []string{"worker"}}

	// Insert in non-alphabetical TaskToken order.
	tokens := []string{"tok-z", "tok-a", "tok-m", "tok-b"}
	for _, tok := range tokens {
		tsk := makeTask(tok, "inst-1", "node-1", humantask.Unclaimed, "", []string{"actor-x"}, nil)
		require.NoError(t, store.Upsert(ctx, tsk))
	}

	got, err := store.ClaimableBy(ctx, actor)
	require.NoError(t, err)
	require.Len(t, got, len(tokens))
	assert.Equal(t, "tok-a", got[0].TaskToken)
	assert.Equal(t, "tok-b", got[1].TaskToken)
	assert.Equal(t, "tok-m", got[2].TaskToken)
	assert.Equal(t, "tok-z", got[3].TaskToken)
}

func TestMemTaskStore_ReturnedTaskIsDefensivelyCopied(t *testing.T) {
	ctx := t.Context()
	store := humantask.NewMemTaskStore()

	// Create a task with non-empty Candidates and Eligibility.Roles.
	originalCandidates := []string{"actor-a", "actor-b"}
	originalRoles := []string{"reviewer", "approver"}
	task := makeTask("tok-def", "inst-1", "node-1", humantask.Unclaimed, "", originalCandidates, originalRoles)

	// Upsert the task.
	require.NoError(t, store.Upsert(ctx, task))

	// Test egress (returned) defensive copy: mutate the returned task.
	got, err := store.Get(ctx, "tok-def")
	require.NoError(t, err)

	// Mutate the returned Candidates slice.
	got.Candidates[0] = "tampered"
	got.Candidates = append(got.Candidates, "injected")

	// Mutate the returned Eligibility.Roles slice.
	got.Eligibility.Roles[0] = "tampered-role"
	got.Eligibility.Roles = append(got.Eligibility.Roles, "injected-role")

	// Re-fetch and assert the store's copy is unchanged.
	got2, err := store.Get(ctx, "tok-def")
	require.NoError(t, err)
	assert.Equal(t, []string{"actor-a", "actor-b"}, got2.Candidates, "Candidates should be unchanged after mutation of returned copy")
	assert.Equal(t, []string{"reviewer", "approver"}, got2.Eligibility.Roles, "Roles should be unchanged after mutation of returned copy")

	// Test ingress defensive copy: mutate the original input slice after Upsert.
	inputCandidates := []string{"actor-c", "actor-d"}
	inputRoles := []string{"editor"}
	task2 := makeTask("tok-def2", "inst-2", "node-2", humantask.Unclaimed, "", inputCandidates, inputRoles)
	require.NoError(t, store.Upsert(ctx, task2))

	// Mutate the input slices after Upsert.
	inputCandidates[0] = "mutated"
	inputRoles[0] = "mutated-role"

	// Fetch and assert the store's copy is unchanged.
	got3, err := store.Get(ctx, "tok-def2")
	require.NoError(t, err)
	assert.Equal(t, []string{"actor-c", "actor-d"}, got3.Candidates, "Candidates should be unchanged after mutation of input slice")
	assert.Equal(t, []string{"editor"}, got3.Eligibility.Roles, "Roles should be unchanged after mutation of input slice")
}

// --- StaticActorResolver tests ---

func TestStaticActorResolver_Candidates_ReturnsUnionDedupedSorted(t *testing.T) {
	ctx := t.Context()

	actorA := authz.Actor{ID: "actor-a", Roles: []string{"approver"}}
	actorB := authz.Actor{ID: "actor-b", Roles: []string{"approver"}}
	actorC := authz.Actor{ID: "actor-c", Roles: []string{"reviewer"}}

	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"approver": {actorB, actorA}, // intentionally non-sorted to test dedup+sort
		"reviewer": {actorC, actorA}, // actorA appears in both roles → dedup by ID
	})

	spec := authz.AuthzSpec{Roles: []string{"approver", "reviewer"}}
	got, err := resolver.Candidates(ctx, spec, nil)
	require.NoError(t, err)

	// Expect [actor-a, actor-b, actor-c] — deduped, sorted by ID.
	require.Len(t, got, 3)
	assert.Equal(t, "actor-a", got[0].ID)
	assert.Equal(t, "actor-b", got[1].ID)
	assert.Equal(t, "actor-c", got[2].ID)
}

func TestStaticActorResolver_Candidates_EmptySpecReturnsEmpty(t *testing.T) {
	ctx := t.Context()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"approver": {{ID: "actor-a"}},
	})

	got, err := resolver.Candidates(ctx, authz.AuthzSpec{}, nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestStaticActorResolver_Candidates_UnknownRoleReturnsEmpty(t *testing.T) {
	ctx := t.Context()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})

	got, err := resolver.Candidates(ctx, authz.AuthzSpec{Roles: []string{"nonexistent"}}, nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestStaticActorResolver_Candidates_SingleRole(t *testing.T) {
	ctx := t.Context()
	actorA := authz.Actor{ID: "actor-a"}
	actorB := authz.Actor{ID: "actor-b"}
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"worker": {actorB, actorA},
	})

	got, err := resolver.Candidates(ctx, authz.AuthzSpec{Roles: []string{"worker"}}, nil)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "actor-a", got[0].ID)
	assert.Equal(t, "actor-b", got[1].ID)
}
