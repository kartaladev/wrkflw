package humantask_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/humantask"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	claim := func(id string) *humantask.Claim {
		return &humantask.Claim{Actor: authz.Actor{ID: id}, At: at}
	}

	type testCase struct {
		name   string
		task   humantask.HumanTask
		assert func(t *testing.T, err error)
	}

	valid := func(t *testing.T, err error) { require.NoError(t, err) }
	invalid := func(id, want string) func(*testing.T, error) {
		return func(t *testing.T, err error) {
			require.ErrorIs(t, err, humantask.ErrInvalidTask)
			require.Contains(t, err.Error(), `task "`+id+`"`, "must name the task")
			require.Contains(t, err.Error(), want, "must name the contradiction")
		}
	}

	cases := []testCase{
		// R1
		{"claimed with a claim is valid",
			humantask.HumanTask{TaskID: "t-1", State: humantask.Claimed, Claim: claim("alice")}, valid},
		{"claimed without a claim is rejected",
			humantask.HumanTask{TaskID: "t-2", State: humantask.Claimed},
			invalid("t-2", "requires a claim")},
		// NOTE: `Claimed` + an EMPTY claimant is deliberately NOT rejected — it is
		// ADR-0148 amendment 1 §4's kiosk shape. Pinned as legal by the row below.
		{"claimed with an empty claimant is ACCEPTED (ADR-0148 kiosk shape)",
			humantask.HumanTask{TaskID: "t-3", State: humantask.Claimed,
				Claim: &humantask.Claim{Actor: authz.Actor{Roles: []string{"kiosk"}}, At: at}}, valid},
		// R2
		{"unclaimed without a claim is valid",
			humantask.HumanTask{TaskID: "t-4", State: humantask.Unclaimed}, valid},
		{"unclaimed carrying a claim is rejected",
			humantask.HumanTask{TaskID: "t-5", State: humantask.Unclaimed, Claim: claim("alice")},
			invalid("t-5", "must not carry a claim")},
		// R3 — closes the bypass: an out-of-range state is neither Claimed nor Unclaimed,
		// so without this rule it sails through and reads back R2-violating.
		{"an out-of-range state is rejected",
			humantask.HumanTask{TaskID: "t-6", State: humantask.TaskState(99), Claim: claim("alice")},
			invalid("t-6", "unknown state")},
		{"a negative state is rejected",
			humantask.HumanTask{TaskID: "t-7", State: humantask.TaskState(-1)},
			invalid("t-7", "unknown state")},
		// DELIBERATE silences — see ADR-0183. ManualImmediate mints Completed with
		// neither claim nor completion; a task cancelled while held keeps its claim.
		{"completed with neither claim nor completion is accepted",
			humantask.HumanTask{TaskID: "t-8", State: humantask.Completed}, valid},
		{"cancelled retaining a claim is accepted",
			humantask.HumanTask{TaskID: "t-9", State: humantask.Cancelled, Claim: claim("alice")}, valid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, humantask.Validate(tc.task))
		})
	}
}
