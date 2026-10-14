package humantask_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/humantask"
)

// TestBaseline_MemTaskStoreUpsertAcceptsClosedTasks characterizes two memory
// rows of openspec/specs/human-task-lifecycle "Claim invariant on write"
// through the store itself (TestValidate covers only the validator it
// applies): a completed task without a claim and a cancelled task carrying a
// claim are both stored.
func TestBaseline_MemTaskStoreUpsertAcceptsClosedTasks(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name   string
		task   humantask.HumanTask
		assert func(t *testing.T, stored humantask.HumanTask, getErr error)
	}

	cases := []testCase{
		{
			name: "completed without a claim is stored",
			task: humantask.HumanTask{TaskID: "b-completed", State: humantask.Completed},
			assert: func(t *testing.T, stored humantask.HumanTask, getErr error) {
				require.NoError(t, getErr, "the task must have been stored")
				assert.Equal(t, humantask.Completed, stored.State)
				assert.Nil(t, stored.Claim)
			},
		},
		{
			name: "cancelled with a claim is stored",
			task: humantask.HumanTask{
				TaskID: "b-cancelled", State: humantask.Cancelled,
				Claim: &humantask.Claim{Actor: authz.Actor{ID: "alice"}, At: at},
			},
			assert: func(t *testing.T, stored humantask.HumanTask, getErr error) {
				require.NoError(t, getErr, "the task must have been stored")
				assert.Equal(t, humantask.Cancelled, stored.State)
				require.NotNil(t, stored.Claim)
				assert.Equal(t, "alice", stored.Claim.Actor.ID)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := humantask.NewMemTaskStore()
			require.NoError(t, store.Upsert(t.Context(), tc.task))

			stored, getErr := store.Get(t.Context(), tc.task.TaskID)
			tc.assert(t, stored, getErr)
		})
	}
}
