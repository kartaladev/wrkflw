// Package humantask_test exercises MemTaskStore and StaticActorResolver.
package humantask_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeTask is a minimal HumanTask fixture. claimedBy and candidates are given as
// bare actor IDs for brevity; they are widened to the audit shape (a *Claim and
// []authz.Actor) here so the individual tests stay readable.
func makeTask(token, instanceID, nodeID string, state humantask.TaskState, claimedBy string, candidates []string, roles []string) humantask.HumanTask {
	var claim *humantask.Claim
	if claimedBy != "" {
		claim = &humantask.Claim{Actor: authz.Actor{ID: claimedBy}}
	}
	return humantask.HumanTask{
		TaskID:     token,
		InstanceID: instanceID,
		NodeID:     nodeID,
		Eligibility: authz.AuthzSpec{
			Roles: roles,
		},
		Candidates: actorsWithIDs(candidates...),
		State:      state,
		Claim:      claim,
		CreatedAt:  time.Now(),
		DueAt:      nil,
	}
}

// actorsWithIDs builds a candidate slice from bare actor IDs.
func actorsWithIDs(ids ...string) []authz.Actor {
	if len(ids) == 0 {
		return nil
	}
	actors := make([]authz.Actor, len(ids))
	for i, id := range ids {
		actors[i] = authz.Actor{ID: id}
	}
	return actors
}

// candidateIDs projects a candidate slice back to bare IDs for assertions.
func candidateIDs(candidates []authz.Actor) []string {
	if len(candidates) == 0 {
		return nil
	}
	ids := make([]string, len(candidates))
	for i, c := range candidates {
		ids[i] = c.ID
	}
	return ids
}

// --- MemTaskStore tests ---

func TestMemTaskStore_UpsertGet_RoundTrip(t *testing.T) {
	ctx := t.Context()
	store := humantask.NewMemTaskStore()

	task := makeTask("tok-1", "inst-1", "node-1", humantask.Unclaimed, "", []string{"actor-a"}, nil)
	require.NoError(t, store.Upsert(ctx, task))

	got, err := store.Get(ctx, "tok-1")
	require.NoError(t, err)
	assert.Equal(t, task.TaskID, got.TaskID)
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
	task.Claim = &humantask.Claim{Actor: authz.Actor{ID: "actor-x"}}
	require.NoError(t, store.Upsert(ctx, task))

	got, err := store.Get(ctx, "tok-upd")
	require.NoError(t, err)
	assert.Equal(t, humantask.Claimed, got.State)
	require.NotNil(t, got.Claim)
	assert.Equal(t, "actor-x", got.Claim.Actor.ID)
}

// TestMemTaskStore_AssignedTo pins the AssignedTo contract: it answers "which
// tasks is this actor holding", so it matches on the claimant recorded in
// [humantask.HumanTask.Claim] and nothing else.
//
// The empty-actor-ID rows are the load-bearing ones. An unclaimed task has no
// claimant at all, and an empty actor ID identifies nobody — so AssignedTo("")
// must return nothing rather than degenerating into a wildcard that dumps every
// task nobody is holding. That would be a data-disclosure footgun for any caller
// that reaches this method with an unauthenticated or unresolved actor ID.
func TestMemTaskStore_AssignedTo(t *testing.T) {
	t.Parallel()

	// claimedByNobody is the pathological row: a claim record whose actor carries
	// no ID. It must not be reachable through the empty actor ID either.
	claimedByNobody := makeTask("tok-nobody", "inst-9", "node-1", humantask.Claimed, "", nil, nil)
	claimedByNobody.Claim = &humantask.Claim{Actor: authz.Actor{ID: ""}}

	type testCase struct {
		name    string
		seed    []humantask.HumanTask
		actorID string
		ctx     func(ctx context.Context) context.Context // nil means identity
		assert  func(t *testing.T, got []humantask.HumanTask, err error)
	}

	cases := []testCase{
		{
			name: "a claimed task matches its claimant, sorted by TaskID",
			seed: []humantask.HumanTask{
				makeTask("tok-a2", "inst-2", "node-1", humantask.Claimed, "actor-a", nil, nil),
				makeTask("tok-a1", "inst-1", "node-1", humantask.Claimed, "actor-a", nil, nil),
				makeTask("tok-b1", "inst-3", "node-1", humantask.Claimed, "actor-b", nil, nil),
				makeTask("tok-unc", "inst-4", "node-1", humantask.Unclaimed, "", nil, nil),
			},
			actorID: "actor-a",
			assert: func(t *testing.T, got []humantask.HumanTask, err error) {
				require.NoError(t, err)
				require.Len(t, got, 2)
				assert.Equal(t, "tok-a1", got[0].TaskID, "results should be sorted by TaskID")
				assert.Equal(t, "tok-a2", got[1].TaskID)
			},
		},
		{
			name: "a different actor does not match",
			seed: []humantask.HumanTask{
				makeTask("tok-1", "inst-1", "node-1", humantask.Claimed, "actor-a", nil, nil),
			},
			actorID: "no-such-actor",
			assert: func(t *testing.T, got []humantask.HumanTask, err error) {
				require.NoError(t, err)
				assert.Empty(t, got)
			},
		},
		{
			name: "an unclaimed task never matches, even for a candidate",
			seed: []humantask.HumanTask{
				makeTask("tok-unc", "inst-1", "node-1", humantask.Unclaimed, "", []string{"actor-a"}, nil),
			},
			actorID: "actor-a",
			assert: func(t *testing.T, got []humantask.HumanTask, err error) {
				require.NoError(t, err)
				assert.Empty(t, got, "a candidate has not claimed the task; it is not assigned to them")
			},
		},
		{
			name: "an empty actor ID is not a wildcard over unclaimed tasks",
			seed: []humantask.HumanTask{
				makeTask("tok-unc1", "inst-1", "node-1", humantask.Unclaimed, "", nil, nil),
				makeTask("tok-unc2", "inst-2", "node-1", humantask.Unclaimed, "", nil, nil),
				makeTask("tok-cl", "inst-3", "node-1", humantask.Claimed, "actor-a", nil, nil),
			},
			actorID: "",
			assert: func(t *testing.T, got []humantask.HumanTask, err error) {
				require.NoError(t, err)
				assert.Empty(t, got, "an empty actor ID identifies nobody and must disclose nothing")
			},
		},
		{
			name:    "an empty actor ID does not match a claim recording an empty actor ID",
			seed:    []humantask.HumanTask{claimedByNobody},
			actorID: "",
			assert: func(t *testing.T, got []humantask.HumanTask, err error) {
				require.NoError(t, err)
				assert.Empty(t, got, "an empty actor ID must never match, however the claim was recorded")
			},
		},
		{
			name: "a cancelled context is immaterial to the in-memory store",
			seed: []humantask.HumanTask{
				makeTask("tok-1", "inst-1", "node-1", humantask.Claimed, "actor-a", nil, nil),
			},
			actorID: "actor-a",
			ctx: func(ctx context.Context) context.Context {
				cctx, cancel := context.WithCancel(ctx)
				cancel()
				return cctx
			},
			assert: func(t *testing.T, got []humantask.HumanTask, err error) {
				// MemTaskStore performs no I/O, so it has nothing to abandon on
				// cancellation and deliberately ignores the context.
				require.NoError(t, err)
				require.Len(t, got, 1)
				assert.Equal(t, "tok-1", got[0].TaskID)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			store := humantask.NewMemTaskStore()
			for _, tsk := range tc.seed {
				require.NoError(t, store.Upsert(ctx, tsk))
			}
			if tc.ctx != nil {
				ctx = tc.ctx(ctx)
			}

			got, err := store.AssignedTo(ctx, tc.actorID)
			tc.assert(t, got, err)
		})
	}
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
	assert.Equal(t, "tok-e", got[0].TaskID)
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
	assert.Equal(t, "tok-role", got[0].TaskID)
}

func TestMemTaskStore_ClaimableBy_DeterministicOrder(t *testing.T) {
	ctx := t.Context()
	store := humantask.NewMemTaskStore()

	actor := authz.Actor{ID: "actor-x", Roles: []string{"worker"}}

	// Insert in non-alphabetical TaskID order.
	tokens := []string{"tok-z", "tok-a", "tok-m", "tok-b"}
	for _, tok := range tokens {
		tsk := makeTask(tok, "inst-1", "node-1", humantask.Unclaimed, "", []string{"actor-x"}, nil)
		require.NoError(t, store.Upsert(ctx, tsk))
	}

	got, err := store.ClaimableBy(ctx, actor)
	require.NoError(t, err)
	require.Len(t, got, len(tokens))
	assert.Equal(t, "tok-a", got[0].TaskID)
	assert.Equal(t, "tok-b", got[1].TaskID)
	assert.Equal(t, "tok-m", got[2].TaskID)
	assert.Equal(t, "tok-z", got[3].TaskID)
}

func TestMemTaskStore_ReturnedTaskIsDefensivelyCopied(t *testing.T) {
	ctx := t.Context()
	store := humantask.NewMemTaskStore()

	// Create a task with non-empty Candidates and Eligibility.Roles.
	originalCandidates := []authz.Actor{
		{ID: "actor-a", Roles: []string{"reviewer"}, Attributes: map[string]any{"email": "a@acme.com"}},
		{ID: "actor-b"},
	}
	originalRoles := []string{"reviewer", "approver"}
	task := makeTask("tok-def", "inst-1", "node-1", humantask.Unclaimed, "", nil, originalRoles)
	task.Candidates = originalCandidates

	// Upsert the task.
	require.NoError(t, store.Upsert(ctx, task))

	// Test egress (returned) defensive copy: mutate the returned task.
	got, err := store.Get(ctx, "tok-def")
	require.NoError(t, err)

	// Mutate the returned Candidates slice, including inside the first actor.
	got.Candidates[0].ID = "tampered"
	got.Candidates[0].Roles[0] = "tampered-role"
	got.Candidates[0].Attributes["email"] = "tampered@acme.com"
	got.Candidates = append(got.Candidates, authz.Actor{ID: "injected"})

	// Mutate the returned Eligibility.Roles slice.
	got.Eligibility.Roles[0] = "tampered-role"
	got.Eligibility.Roles = append(got.Eligibility.Roles, "injected-role")

	// Re-fetch and assert the store's copy is unchanged.
	got2, err := store.Get(ctx, "tok-def")
	require.NoError(t, err)
	assert.Equal(t, []string{"actor-a", "actor-b"}, candidateIDs(got2.Candidates), "Candidates should be unchanged after mutation of returned copy")
	assert.Equal(t, []string{"reviewer"}, got2.Candidates[0].Roles, "candidate roles should be unchanged after mutation of returned copy")
	assert.Equal(t, "a@acme.com", got2.Candidates[0].Attributes["email"], "candidate attributes should be unchanged after mutation of returned copy")
	assert.Equal(t, []string{"reviewer", "approver"}, got2.Eligibility.Roles, "Roles should be unchanged after mutation of returned copy")

	// Test ingress defensive copy: mutate the original input slices after Upsert.
	inputCandidates := []authz.Actor{{ID: "actor-c", Roles: []string{"editor"}}, {ID: "actor-d"}}
	inputRoles := []string{"editor"}
	task2 := makeTask("tok-def2", "inst-2", "node-2", humantask.Unclaimed, "", nil, inputRoles)
	task2.Candidates = inputCandidates
	require.NoError(t, store.Upsert(ctx, task2))

	// Mutate the input slices after Upsert.
	inputCandidates[0].ID = "mutated"
	inputCandidates[0].Roles[0] = "mutated-role"
	inputRoles[0] = "mutated-role"

	// Fetch and assert the store's copy is unchanged.
	got3, err := store.Get(ctx, "tok-def2")
	require.NoError(t, err)
	assert.Equal(t, []string{"actor-c", "actor-d"}, candidateIDs(got3.Candidates), "Candidates should be unchanged after mutation of input slice")
	assert.Equal(t, []string{"editor"}, got3.Candidates[0].Roles, "candidate roles should be unchanged after mutation of input slice")
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

func TestMemTaskStoreUpsertRejectsAnInvalidTask(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name string
		task humantask.HumanTask
	}

	cases := []testCase{
		{"claimed without a claim", humantask.HumanTask{TaskID: "m-1", State: humantask.Claimed}},
		{"unclaimed carrying a claim", humantask.HumanTask{
			TaskID: "m-3", State: humantask.Unclaimed,
			Claim: &humantask.Claim{Actor: authz.Actor{ID: "alice"}, At: at}}},
		{"out-of-range state", humantask.HumanTask{TaskID: "m-4", State: humantask.TaskState(99)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := humantask.NewMemTaskStore()
			require.ErrorIs(t, store.Upsert(t.Context(), tc.task), humantask.ErrInvalidTask)

			_, getErr := store.Get(t.Context(), tc.task.TaskID)
			require.ErrorIs(t, getErr, humantask.ErrTaskNotFound,
				"a rejected Upsert must not store the task")
		})
	}
}
