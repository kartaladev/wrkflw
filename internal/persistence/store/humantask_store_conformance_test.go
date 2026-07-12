package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
)

// compile-time guard: the neutral store satisfies the public interface.
var _ humantask.TaskStore = (*store.HumanTaskStore)(nil)

func TestHumanTaskStoreConformance(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ts, err := store.NewHumanTaskStore(b.conn, b.dialect)
		require.NoError(t, err)

		t.Run("upsert_get_round_trip", func(t *testing.T) {
			due := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
			seed := humantask.HumanTask{
				TaskToken:   "tok-rt-" + b.name,
				InstanceID:  "inst-1",
				NodeID:      "approve",
				State:       humantask.Unclaimed,
				Eligibility: authz.AuthzSpec{Roles: []string{"manager"}},
				Candidates:  []string{"alice"},
				Vars:        map[string]any{"amount": float64(100)},
				CreatedAt:   time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC),
				DueAt:       &due,
			}
			require.NoError(t, ts.Upsert(t.Context(), seed), "%s: Upsert", b.name)

			got, err := ts.Get(t.Context(), "tok-rt-"+b.name)
			require.NoError(t, err, "%s: Get", b.name)
			assert.Equal(t, "inst-1", got.InstanceID, "%s: InstanceID", b.name)
			assert.Equal(t, humantask.Unclaimed, got.State, "%s: State", b.name)
			assert.Equal(t, []string{"manager"}, got.Eligibility.Roles, "%s: Eligibility.Roles", b.name)
			assert.Equal(t, []string{"alice"}, got.Candidates, "%s: Candidates", b.name)
			require.NotNil(t, got.DueAt, "%s: DueAt", b.name)
			assert.True(t, got.DueAt.Equal(due), "%s: DueAt value", b.name)
			assert.True(t, got.CreatedAt.Equal(seed.CreatedAt), "%s: CreatedAt", b.name)
			assert.Equal(t, map[string]any{"amount": float64(100)}, got.Vars, "%s: Vars", b.name)
		})

		t.Run("get_miss_returns_err_task_not_found", func(t *testing.T) {
			_, err := ts.Get(t.Context(), "tok-no-such-"+b.name)
			require.Error(t, err, "%s: Get missing must error", b.name)
			require.ErrorIs(t, err, humantask.ErrTaskNotFound,
				"%s: must wrap ErrTaskNotFound; got %v", b.name, err)
		})

		t.Run("upsert_no_due_at", func(t *testing.T) {
			seed := humantask.HumanTask{
				TaskToken:  "tok-no-due-" + b.name,
				InstanceID: "inst-nd",
				NodeID:     "review",
				State:      humantask.Unclaimed,
				CreatedAt:  time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC),
			}
			require.NoError(t, ts.Upsert(t.Context(), seed), "%s: Upsert no-due", b.name)

			got, err := ts.Get(t.Context(), "tok-no-due-"+b.name)
			require.NoError(t, err, "%s: Get no-due", b.name)
			assert.Nil(t, got.DueAt, "%s: DueAt must be nil", b.name)
		})

		t.Run("assigned_to_filters_by_claimed_by_and_sorts", func(t *testing.T) {
			// Seed tasks: two claimed by "bob", one by "carol", one unclaimed.
			tasks := []humantask.HumanTask{
				{
					TaskToken: "tok-bob-2-" + b.name, InstanceID: "i1", NodeID: "n1",
					State: humantask.Claimed, ClaimedBy: "bob",
					CreatedAt: time.Now().UTC(),
				},
				{
					TaskToken: "tok-bob-1-" + b.name, InstanceID: "i2", NodeID: "n1",
					State: humantask.Claimed, ClaimedBy: "bob",
					CreatedAt: time.Now().UTC(),
				},
				{
					TaskToken: "tok-carol-1-" + b.name, InstanceID: "i3", NodeID: "n1",
					State: humantask.Claimed, ClaimedBy: "carol",
					CreatedAt: time.Now().UTC(),
				},
				{
					TaskToken: "tok-uncl-1-" + b.name, InstanceID: "i4", NodeID: "n1",
					State:     humantask.Unclaimed,
					CreatedAt: time.Now().UTC(),
				},
			}
			for _, task := range tasks {
				require.NoError(t, ts.Upsert(t.Context(), task), "%s: seed Upsert %s", b.name, task.TaskToken)
			}

			result, err := ts.AssignedTo(t.Context(), "bob")
			require.NoError(t, err, "%s: AssignedTo", b.name)
			require.Len(t, result, 2, "%s: AssignedTo must return 2 tasks for bob", b.name)
			// Must be sorted by task_token ascending.
			assert.Equal(t, "tok-bob-1-"+b.name, result[0].TaskToken, "%s: first token", b.name)
			assert.Equal(t, "tok-bob-2-"+b.name, result[1].TaskToken, "%s: second token", b.name)

			// carol gets only her task
			carolResult, err := ts.AssignedTo(t.Context(), "carol")
			require.NoError(t, err, "%s: AssignedTo carol", b.name)
			require.Len(t, carolResult, 1, "%s: carol must have 1 task", b.name)
		})

		t.Run("claimable_by_candidate", func(t *testing.T) {
			due := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
			seed := humantask.HumanTask{
				TaskToken:   "tok-cand-" + b.name,
				InstanceID:  "ic1",
				NodeID:      "approve",
				State:       humantask.Unclaimed,
				Candidates:  []string{"dave"},
				Eligibility: authz.AuthzSpec{Roles: []string{"auditor"}},
				CreatedAt:   time.Now().UTC(),
				DueAt:       &due,
			}
			require.NoError(t, ts.Upsert(t.Context(), seed), "%s: seed claimable-by-candidate", b.name)

			actor := authz.Actor{ID: "dave", Roles: []string{"developer"}}
			result, err := ts.ClaimableBy(t.Context(), actor)
			require.NoError(t, err, "%s: ClaimableBy by candidate", b.name)

			found := false
			for _, r := range result {
				if r.TaskToken == "tok-cand-"+b.name {
					found = true
					break
				}
			}
			assert.True(t, found, "%s: dave must see tok-cand as claimable (by candidate)", b.name)
		})

		t.Run("claimable_by_role", func(t *testing.T) {
			seed := humantask.HumanTask{
				TaskToken:   "tok-role-" + b.name,
				InstanceID:  "ir1",
				NodeID:      "review",
				State:       humantask.Unclaimed,
				Candidates:  []string{"other-user"},
				Eligibility: authz.AuthzSpec{Roles: []string{"manager", "supervisor"}},
				CreatedAt:   time.Now().UTC(),
			}
			require.NoError(t, ts.Upsert(t.Context(), seed), "%s: seed claimable-by-role", b.name)

			actor := authz.Actor{ID: "eve", Roles: []string{"supervisor"}}
			result, err := ts.ClaimableBy(t.Context(), actor)
			require.NoError(t, err, "%s: ClaimableBy by role", b.name)

			found := false
			for _, r := range result {
				if r.TaskToken == "tok-role-"+b.name {
					found = true
					break
				}
			}
			assert.True(t, found, "%s: eve must see tok-role as claimable (by role)", b.name)
		})

		t.Run("claimable_by_excludes_non_unclaimed", func(t *testing.T) {
			// Seed a claimed task that eve would otherwise be eligible for.
			seed := humantask.HumanTask{
				TaskToken:   "tok-claimed-" + b.name,
				InstanceID:  "icl1",
				NodeID:      "review",
				State:       humantask.Claimed,
				ClaimedBy:   "frank",
				Candidates:  []string{"eve"},
				Eligibility: authz.AuthzSpec{Roles: []string{"supervisor"}},
				CreatedAt:   time.Now().UTC(),
			}
			require.NoError(t, ts.Upsert(t.Context(), seed), "%s: seed claimed-not-claimable", b.name)

			actor := authz.Actor{ID: "eve", Roles: []string{"supervisor"}}
			result, err := ts.ClaimableBy(t.Context(), actor)
			require.NoError(t, err, "%s: ClaimableBy excludes claimed", b.name)

			for _, r := range result {
				assert.NotEqual(t, "tok-claimed-"+b.name, r.TaskToken,
					"%s: claimed task must NOT appear in ClaimableBy", b.name)
			}
		})
	})
}
