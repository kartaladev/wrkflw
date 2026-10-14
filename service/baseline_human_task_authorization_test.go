package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/service"
)

// TestBaseline_RefusedTaskOperationChangesNothing characterizes
// openspec/specs/human-task-authorization "A refused operation changes
// nothing": when "bob", whom the task's eligibility (role "manager") does not
// admit, attempts to claim, complete or refresh the candidates of an open task,
// the attempt is refused with authz.ErrNotAuthorized and a re-read of the task
// and of the instance finds both exactly as they were.
//
// The refresh row swaps the resolver's answer first, so a refresh that was NOT
// refused would visibly replace the candidate list.
func TestBaseline_RefusedTaskOperationChangesNothing(t *testing.T) {
	t.Parallel()

	bob := authz.Actor{ID: "bob", Roles: []string{"viewer"}}
	carol := authz.Actor{ID: "carol", Roles: []string{"manager"}}

	type testCase struct {
		name string
		// operate attempts the operation as bob.
		operate func(ctx context.Context, f refreshFixture) error
		assert  func(t *testing.T, err error)
	}

	refused := func(t *testing.T, err error) {
		require.ErrorIs(t, err, authz.ErrNotAuthorized)
	}

	cases := []testCase{
		{
			name: "claim",
			operate: func(ctx context.Context, f refreshFixture) error {
				_, err := f.svc.ClaimTask(ctx, service.ClaimTaskRequest{TaskID: f.taskID, Actor: bob})
				return err
			},
			assert: refused,
		},
		{
			name: "complete",
			operate: func(ctx context.Context, f refreshFixture) error {
				_, err := f.svc.CompleteTask(ctx, service.CompleteTaskRequest{
					TaskID: f.taskID, Actor: bob, Output: map[string]any{"approved": true},
				})
				return err
			},
			assert: refused,
		},
		{
			name: "refresh candidates",
			operate: func(ctx context.Context, f refreshFixture) error {
				f.resolver.set([]authz.Actor{carol})
				_, err := f.svc.RefreshTaskCandidates(ctx, service.RefreshTaskCandidatesRequest{TaskID: f.taskID, By: bob})
				return err
			},
			assert: refused,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			f := newRefreshFixture(t, ctx, true)

			taskBefore, err := f.tasks.Get(ctx, f.taskID)
			require.NoError(t, err)
			instBefore, err := f.svc.GetInstance(ctx, "refresh-inst")
			require.NoError(t, err)

			tc.assert(t, tc.operate(ctx, f))

			taskAfter, err := f.tasks.Get(ctx, f.taskID)
			require.NoError(t, err)
			instAfter, err := f.svc.GetInstance(ctx, "refresh-inst")
			require.NoError(t, err)

			assert.Equal(t, taskBefore.State, taskAfter.State, "task state")
			assert.Equal(t, taskBefore.Claim, taskAfter.Claim, "task claim")
			assert.Equal(t, taskBefore.Candidates, taskAfter.Candidates, "task candidates")
			assert.Equal(t, instBefore.State(), instAfter.State(), "instance state")
		})
	}
}
