package task_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
	"github.com/kartaladev/wrkflw/runtime/task"
)

// recordingAuthorizer captures the [authz.Request] it was asked about and then
// allows. It RECORDS rather than logs: `go test` swallows output from a passing
// package, so a probe that printed would read as zero and look exactly like a
// clean result.
type recordingAuthorizer struct {
	seen []authz.Request
}

func (a *recordingAuthorizer) Authorize(_ context.Context, r authz.Request) error {
	a.seen = append(a.seen, r)
	return nil
}

var _ authz.Authorizer = (*recordingAuthorizer)(nil)

// TestTaskServiceBuildsTheAuthorizationRequest pins WHAT the service asks, not
// merely whether it asks.
//
// Nothing else in this package can see the difference: every existing test
// drives an authorizer that ignores the Operation and the TaskView entirely, so
// a service that labelled all four methods OpClaim, or that never populated the
// task projection, would pass the whole suite. Both fields exist for deciders
// that scope themselves by operation and read the claim state, and a decider
// handed a wrong or empty one abstains — which the combiner reads as ALLOW.
// That is the same shape as the allow-all defect this port exists to fix, so it
// is asserted here at the boundary that produces it.
func TestTaskServiceBuildsTheAuthorizationRequest(t *testing.T) {
	t.Parallel()

	const (
		taskID    = "tok-req-1"
		claimant  = "u-claimant"
		requester = "u-requester"
	)

	spec := authz.AuthzSpec{Roles: []string{"manager"}}
	vars := map[string]any{"region": "EU"}
	actor := authz.Actor{ID: requester, Roles: []string{"manager"}, Privileges: []string{"p"}}

	type testCase struct {
		name string
		// claim is the task's existing claim, nil for an unclaimed task.
		claim  *humantask.Claim
		call   func(t *testing.T, svc *task.TaskService) error
		assert func(t *testing.T, r authz.Request)
	}

	common := func(t *testing.T, r authz.Request) {
		t.Helper()
		assert.Equal(t, spec, r.Spec, "the task's eligibility spec must reach the authorizer")
		assert.Equal(t, vars, r.Vars, "the task's variable snapshot must reach the authorizer")
		assert.Equal(t, requester, r.Actor.ID)
		assert.Equal(t, []string{"p"}, r.Actor.Privileges,
			"the request-time actor keeps its privileges: stripping them here "+
				"would silently disarm the privilege rule")
	}

	cases := []testCase{
		{
			name:  "Claim asks about OpClaim on an unclaimed task",
			claim: nil,
			call: func(t *testing.T, svc *task.TaskService) error {
				_, err := svc.Claim(t.Context(), taskID, actor)
				return err
			},
			assert: func(t *testing.T, r authz.Request) {
				common(t, r)
				assert.Equal(t, authz.OpClaim, r.Operation)
				assert.Equal(t, authz.TaskView{}, r.Task,
					"an unclaimed task projects to the zero TaskView")
				assert.False(t, r.Task.Claimed)
			},
		},
		{
			name:  "Complete asks about OpComplete and carries the claimant",
			claim: &humantask.Claim{Actor: authz.Actor{ID: claimant}, At: time.Unix(10, 0)},
			call: func(t *testing.T, svc *task.TaskService) error {
				_, err := svc.Complete(t.Context(), taskID, actor, engine.CompletionInput{})
				return err
			},
			assert: func(t *testing.T, r authz.Request) {
				common(t, r)
				assert.Equal(t, authz.OpComplete, r.Operation)
				assert.Equal(t, authz.TaskView{Claimed: true, ClaimantID: claimant}, r.Task,
					"the ownership rule arriving later reads exactly this projection")
			},
		},
		{
			name:  "Reassign asks about OpReassign and carries the claimant",
			claim: &humantask.Claim{Actor: authz.Actor{ID: claimant}, At: time.Unix(10, 0)},
			call: func(t *testing.T, svc *task.TaskService) error {
				_, err := svc.Reassign(t.Context(), taskID, claimant, "u-next", actor)
				return err
			},
			assert: func(t *testing.T, r authz.Request) {
				common(t, r)
				assert.Equal(t, authz.OpReassign, r.Operation)
				assert.Equal(t, authz.TaskView{Claimed: true, ClaimantID: claimant}, r.Task)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := humantask.NewMemTaskStore()
			state := humantask.Unclaimed
			if tc.claim != nil {
				state = humantask.Claimed
			}
			require.NoError(t, store.Upsert(t.Context(), humantask.HumanTask{
				TaskID:      taskID,
				Eligibility: spec,
				Vars:        vars,
				State:       state,
				Claim:       tc.claim,
			}))

			spy := &recordingAuthorizer{}
			svc := runtimetest.MustTaskService(t, store, spy)

			require.NoError(t, tc.call(t, svc))
			require.Len(t, spy.seen, 1, "exactly one authorization question per call")
			tc.assert(t, spy.seen[0])
		})
	}
}

// TestTaskServiceRefreshCandidatesBuildsTheAuthorizationRequest covers the
// fourth call site. It is separate because RefreshCandidates additionally needs
// an ActorResolver, which is a different setup shape from the three above.
func TestTaskServiceRefreshCandidatesBuildsTheAuthorizationRequest(t *testing.T) {
	t.Parallel()

	const taskID = "tok-req-refresh"

	spec := authz.AuthzSpec{Roles: []string{"manager"}}
	actor := authz.Actor{ID: "u-requester", Roles: []string{"manager"}}

	store := humantask.NewMemTaskStore()
	require.NoError(t, store.Upsert(t.Context(), humantask.HumanTask{
		TaskID:      taskID,
		Eligibility: spec,
		Vars:        map[string]any{"region": "EU"},
		State:       humantask.Unclaimed,
	}))

	spy := &recordingAuthorizer{}
	svc := runtimetest.MustTaskService(t, store, spy,
		task.WithActorResolver(humantask.NewStaticActorResolver(map[string][]authz.Actor{
			"manager": {actor},
		})),
	)

	_, err := svc.RefreshCandidates(t.Context(), taskID, actor)
	require.NoError(t, err)
	require.Len(t, spy.seen, 1)
	assert.Equal(t, authz.OpRefreshCandidates, spy.seen[0].Operation,
		"a decider scoped to refresh must be able to tell it from a claim")
	assert.Equal(t, spec, spy.seen[0].Spec)
	assert.Equal(t, map[string]any{"region": "EU"}, spy.seen[0].Vars)
}
