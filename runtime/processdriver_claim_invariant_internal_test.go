package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
)

// TestValidateTaskCommands covers the pure pre-commit hook that refuses a step
// whose task projections contradict themselves.
//
// The rows exercise the three shapes the hook can meet: an invalid UpdateTask,
// a valid one, and command slices carrying no UpdateTask at all — including an
// AwaitHuman on its own, which cannot be claim-invalid because performAwaitHuman
// builds its record with State: Unclaimed and no Claim.
func TestValidateTaskCommands(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name   string
		cmds   []engine.Command
		assert func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name: "an UpdateTask projecting a claimed task with no claim is rejected",
			cmds: []engine.Command{
				engine.UpdateTask{Task: humantask.HumanTask{TaskID: "tk-1", State: humantask.Claimed}},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, humantask.ErrInvalidTask)
				require.Contains(t, err.Error(), "reject step", "must say the step was refused")
				require.Contains(t, err.Error(), `task "tk-1"`, "must name the offending task")
			},
		},
		{
			name: "a valid UpdateTask is accepted",
			cmds: []engine.Command{
				engine.UpdateTask{Task: humantask.HumanTask{
					TaskID: "tk-2", State: humantask.Claimed,
					Claim: &humantask.Claim{Actor: authz.Actor{ID: "alice"}, At: at},
				}},
			},
			assert: func(t *testing.T, err error) { require.NoError(t, err) },
		},
		{
			// The N>1 case: the WHOLE step is refused, so no later UpdateTask is
			// silently dropped the way a post-commit rejection dropped tasks 2..N out
			// of the queue.
			name: "the first of two UpdateTasks being invalid refuses the step",
			cmds: []engine.Command{
				engine.UpdateTask{Task: humantask.HumanTask{TaskID: "tk-first", State: humantask.Claimed}},
				engine.UpdateTask{Task: humantask.HumanTask{
					TaskID: "tk-second", State: humantask.Cancelled,
					Claim: &humantask.Claim{Actor: authz.Actor{ID: "alice"}, At: at},
				}},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, humantask.ErrInvalidTask)
				require.Contains(t, err.Error(), `task "tk-first"`)
			},
		},
		{
			// Discriminates a hook that inspects only cmds[0] from one that scans the
			// whole slice.
			name: "the second of two UpdateTasks being invalid refuses the step",
			cmds: []engine.Command{
				engine.UpdateTask{Task: humantask.HumanTask{
					TaskID: "tk-first", State: humantask.Claimed,
					Claim: &humantask.Claim{Actor: authz.Actor{ID: "alice"}, At: at},
				}},
				engine.UpdateTask{Task: humantask.HumanTask{TaskID: "tk-second", State: humantask.Claimed}},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, humantask.ErrInvalidTask)
				require.Contains(t, err.Error(), `task "tk-second"`)
			},
		},
		{
			name: "a command slice carrying no UpdateTask is accepted",
			cmds: []engine.Command{
				engine.CancelTimer{TimerID: "tm-1"},
				engine.ThrowSignal{Name: "sig"},
			},
			assert: func(t *testing.T, err error) { require.NoError(t, err) },
		},
		{
			name: "an AwaitHuman alone is accepted",
			cmds: []engine.Command{
				engine.AwaitHuman{TaskID: "tk-3", Eligibility: authz.AuthzSpec{Roles: []string{"mgr"}}},
			},
			assert: func(t *testing.T, err error) { require.NoError(t, err) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, validateTaskCommands(tc.cmds))
		})
	}
}
