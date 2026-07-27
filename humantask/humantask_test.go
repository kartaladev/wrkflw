// Package humantask_test verifies the exported types and errors of the humantask package.
package humantask_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskState_Values(t *testing.T) {
	tests := []struct {
		name  string
		state humantask.TaskState
		want  int
	}{
		{"Unclaimed is 0", humantask.Unclaimed, 0},
		{"Claimed is 1", humantask.Claimed, 1},
		{"Completed is 2", humantask.Completed, 2},
		{"Cancelled is 3", humantask.Cancelled, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, humantask.TaskState(tc.want), tc.state)
		})
	}
}

func TestTaskState_String(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		state  humantask.TaskState
		assert func(t *testing.T, got string)
	}

	cases := []testCase{
		{
			name:   "unclaimed",
			state:  humantask.Unclaimed,
			assert: func(t *testing.T, got string) { assert.Equal(t, "unclaimed", got) },
		},
		{
			name:   "claimed",
			state:  humantask.Claimed,
			assert: func(t *testing.T, got string) { assert.Equal(t, "claimed", got) },
		},
		{
			name:   "completed",
			state:  humantask.Completed,
			assert: func(t *testing.T, got string) { assert.Equal(t, "completed", got) },
		},
		{
			name:   "cancelled",
			state:  humantask.Cancelled,
			assert: func(t *testing.T, got string) { assert.Equal(t, "cancelled", got) },
		},
		{
			name:   "out-of-range maps to unknown",
			state:  humantask.TaskState(99),
			assert: func(t *testing.T, got string) { assert.Equal(t, "unknown", got) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, tc.state.String())
		})
	}
}

func TestErrTaskNotFound_IsError(t *testing.T) {
	assert.Error(t, humantask.ErrTaskNotFound)
	assert.Contains(t, humantask.ErrTaskNotFound.Error(), "not found")
}

// TestHumanTaskAuditModel verifies the ADR-0147 audit shape: Candidates carries
// full authz.Actor records (not IDs), Claim captures who claimed and when, and
// Completion captures who completed, when, with which outcome and note. Claim and
// Completion are nil until the corresponding lifecycle event occurs.
func TestHumanTaskAuditModel(t *testing.T) {
	t.Parallel()

	claimedAt := time.Date(2026, 7, 27, 10, 5, 0, 0, time.UTC)
	completedAt := time.Date(2026, 7, 27, 10, 15, 0, 0, time.UTC)
	jane := authz.Actor{
		ID:         "u-jane",
		Roles:      []string{"manager"},
		Attributes: map[string]any{"email": "jane.doe@acme.com"},
	}

	type testCase struct {
		name   string
		task   humantask.HumanTask
		assert func(t *testing.T, task humantask.HumanTask)
	}

	cases := []testCase{
		{
			name: "unclaimed task has no claim and no completion",
			task: humantask.HumanTask{
				TaskID:     "task-1",
				State:      humantask.Unclaimed,
				Candidates: []authz.Actor{jane},
			},
			assert: func(t *testing.T, task humantask.HumanTask) {
				assert.Nil(t, task.Claim)
				assert.Nil(t, task.Completion)
				require.Len(t, task.Candidates, 1)
				assert.Equal(t, "u-jane", task.Candidates[0].ID)
				assert.Equal(t, []string{"manager"}, task.Candidates[0].Roles)
				assert.Equal(t, "jane.doe@acme.com", task.Candidates[0].Attributes["email"])
			},
		},
		{
			name: "claimed task records the claiming actor and time",
			task: humantask.HumanTask{
				TaskID: "task-1",
				State:  humantask.Claimed,
				Claim:  &humantask.Claim{Actor: jane, At: claimedAt},
			},
			assert: func(t *testing.T, task humantask.HumanTask) {
				require.NotNil(t, task.Claim)
				assert.Equal(t, jane, task.Claim.Actor)
				assert.Equal(t, claimedAt, task.Claim.At)
				assert.Nil(t, task.Completion)
			},
		},
		{
			name: "completed task records actor, time, outcome and note",
			task: humantask.HumanTask{
				TaskID: "task-1",
				State:  humantask.Completed,
				Claim:  &humantask.Claim{Actor: jane, At: claimedAt},
				Completion: &humantask.Completion{
					Actor:   jane,
					At:      completedAt,
					Outcome: "approve",
					Note:    "Budget confirmed.",
				},
			},
			assert: func(t *testing.T, task humantask.HumanTask) {
				require.NotNil(t, task.Completion)
				assert.Equal(t, jane, task.Completion.Actor)
				assert.Equal(t, completedAt, task.Completion.At)
				assert.Equal(t, "approve", task.Completion.Outcome)
				assert.Equal(t, "Budget confirmed.", task.Completion.Note)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, tc.task)
		})
	}
}

// TestHumanTaskIsOpen verifies that IsOpen returns true only for Unclaimed and
// Claimed states, and false for Completed and Cancelled.
func TestHumanTaskIsOpen(t *testing.T) {
	tests := []struct {
		name     string
		state    humantask.TaskState
		wantOpen bool
	}{
		{"Unclaimed is open", humantask.Unclaimed, true},
		{"Claimed is open", humantask.Claimed, true},
		{"Completed is not open", humantask.Completed, false},
		{"Cancelled is not open", humantask.Cancelled, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ht := humantask.HumanTask{State: tc.state}
			assert.Equal(t, tc.wantOpen, ht.IsOpen())
		})
	}
}

// TestHumanTaskClone verifies that Clone isolates every mutable field, including
// the actors nested inside Candidates and the Claim/Completion pointees. The
// engine's cloneState and the caching task store both rely on this to hand out
// values a caller cannot use to mutate stored audit data.
func TestHumanTaskClone(t *testing.T) {
	t.Parallel()

	base := func() humantask.HumanTask {
		due := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
		jane := authz.Actor{
			ID:         "u-jane",
			Roles:      []string{"manager"},
			Attributes: map[string]any{"email": "jane.doe@acme.com"},
		}
		return humantask.HumanTask{
			TaskID:      "task-1",
			Eligibility: authz.AuthzSpec{Roles: []string{"manager"}, Privileges: []string{"approve"}},
			Candidates:  []authz.Actor{jane},
			Claim:       &humantask.Claim{Actor: jane},
			Completion:  &humantask.Completion{Actor: jane, Outcome: "approve"},
			DueAt:       &due,
			Vars:        map[string]any{"region": "EU"},
		}
	}

	type testCase struct {
		name   string
		assert func(t *testing.T, orig, clone humantask.HumanTask)
	}

	cases := []testCase{
		{
			name: "candidate actors are deep-copied",
			assert: func(t *testing.T, orig, clone humantask.HumanTask) {
				clone.Candidates[0].ID = "mutated"
				clone.Candidates[0].Roles[0] = "mutated"
				clone.Candidates[0].Attributes["email"] = "mutated"
				assert.Equal(t, "u-jane", orig.Candidates[0].ID)
				assert.Equal(t, "manager", orig.Candidates[0].Roles[0])
				assert.Equal(t, "jane.doe@acme.com", orig.Candidates[0].Attributes["email"])
			},
		},
		{
			name: "claim pointee is deep-copied",
			assert: func(t *testing.T, orig, clone humantask.HumanTask) {
				require.NotSame(t, orig.Claim, clone.Claim)
				clone.Claim.Actor.Roles[0] = "mutated"
				assert.Equal(t, "manager", orig.Claim.Actor.Roles[0])
			},
		},
		{
			name: "completion pointee is deep-copied",
			assert: func(t *testing.T, orig, clone humantask.HumanTask) {
				require.NotSame(t, orig.Completion, clone.Completion)
				clone.Completion.Outcome = "reject"
				assert.Equal(t, "approve", orig.Completion.Outcome)
			},
		},
		{
			name: "eligibility, due date and vars are independently allocated",
			assert: func(t *testing.T, orig, clone humantask.HumanTask) {
				clone.Eligibility.Roles[0] = "mutated"
				clone.Eligibility.Privileges[0] = "mutated"
				clone.Vars["region"] = "US"
				require.NotSame(t, orig.DueAt, clone.DueAt)
				assert.Equal(t, "manager", orig.Eligibility.Roles[0])
				assert.Equal(t, "approve", orig.Eligibility.Privileges[0])
				assert.Equal(t, "EU", orig.Vars["region"])
			},
		},
		{
			name: "nil optional fields stay nil",
			assert: func(t *testing.T, _, _ humantask.HumanTask) {
				clone := humantask.HumanTask{TaskID: "task-2"}.Clone()
				assert.Nil(t, clone.Candidates)
				assert.Nil(t, clone.Claim)
				assert.Nil(t, clone.Completion)
				assert.Nil(t, clone.DueAt)
				assert.Nil(t, clone.Vars)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			orig := base()
			tc.assert(t, orig, orig.Clone())
		})
	}
}

// TestClaimCompletionJSONWireShape pins the audit records' wire form. The
// instance view embeds them directly (ADR-0147 passthrough), so the keys live on
// the types rather than in each renderer: a claim is {actor, timestamp} and a
// completion adds {outcome, note}, both omitted when empty.
func TestClaimCompletionJSONWireShape(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 27, 10, 5, 0, 0, time.UTC)
	jane := authz.Actor{ID: "u-jane"}

	type testCase struct {
		name   string
		value  any
		assert func(t *testing.T, got string)
	}

	cases := []testCase{
		{
			name:  "claim renders actor and timestamp",
			value: humantask.Claim{Actor: jane, At: at},
			assert: func(t *testing.T, got string) {
				require.JSONEq(t, `{"actor":{"id":"u-jane"},"timestamp":"2026-07-27T10:05:00Z"}`, got)
			},
		},
		{
			name:  "completion renders outcome and note",
			value: humantask.Completion{Actor: jane, At: at, Outcome: "approve", Note: "ok"},
			assert: func(t *testing.T, got string) {
				require.JSONEq(t,
					`{"actor":{"id":"u-jane"},"timestamp":"2026-07-27T10:05:00Z","outcome":"approve","note":"ok"}`, got)
			},
		},
		{
			name:  "completion without an outcome omits outcome and note",
			value: humantask.Completion{Actor: jane, At: at},
			assert: func(t *testing.T, got string) {
				require.JSONEq(t, `{"actor":{"id":"u-jane"},"timestamp":"2026-07-27T10:05:00Z"}`, got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, err := json.Marshal(tc.value)
			require.NoError(t, err)
			tc.assert(t, string(b))
		})
	}
}

// TestHumanTaskCloneEmptyButAllocatedSlices guards the aliasing case a length
// guard reintroduces: a zero-length slice with spare capacity is shared, not
// copied, so two clones that each append write the same backing array. Skipping
// the copy when len == 0 is not equivalent to copying — only nil is.
func TestHumanTaskCloneEmptyButAllocatedSlices(t *testing.T) {
	t.Parallel()

	base := humantask.HumanTask{
		Candidates:  make([]authz.Actor, 0, 4),
		Eligibility: authz.AuthzSpec{Roles: make([]string, 0, 4), Privileges: make([]string, 0, 4)},
	}

	a, b := base.Clone(), base.Clone()
	a.Candidates = append(a.Candidates, authz.Actor{ID: "from-a"})
	b.Candidates = append(b.Candidates, authz.Actor{ID: "from-b"})
	a.Eligibility.Roles = append(a.Eligibility.Roles, "role-a")
	b.Eligibility.Roles = append(b.Eligibility.Roles, "role-b")
	a.Eligibility.Privileges = append(a.Eligibility.Privileges, "priv-a")
	b.Eligibility.Privileges = append(b.Eligibility.Privileges, "priv-b")

	assert.Equal(t, "from-a", a.Candidates[0].ID, "clones must not share a candidate backing array")
	assert.Equal(t, "role-a", a.Eligibility.Roles[0], "clones must not share a roles backing array")
	assert.Equal(t, "priv-a", a.Eligibility.Privileges[0], "clones must not share a privileges backing array")
}

// TestAuditTypesSurviveJSONRoundTrip is a CI guard against version skew: Claim
// and Completion are persisted — inside the instance snapshot blob, and by the
// SQL task store — so a struct change that breaks their own round-trip means the
// engine can no longer read what it wrote. Postgres and MySQL validate JSON
// syntax at the column, so malformed data never reaches the decoder there; the
// realistic failure is a shape change we introduce ourselves.
//
// It is deliberately paired with, not a substitute for,
// [TestClaimCompletionJSONWireShape]. The two catch different breaks:
//
//   - the wire-shape test catches a KEY change (renaming a tag) — which this test
//     cannot see, because a rename is symmetric and still round-trips;
//   - this test catches a FIDELITY change — a value that encodes but does not
//     read back identically (a lossy attribute type, an asymmetric custom
//     marshaller, a field added without a usable tag) — which the wire-shape
//     test cannot see, because it asserts a fixed document rather than equality
//     with the source value.
//
// Both were verified to fail when their respective invariant is violated.
func TestAuditTypesSurviveJSONRoundTrip(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 27, 10, 5, 0, 123456000, time.UTC)
	actor := authz.Actor{
		ID:         "u-jane",
		Roles:      []string{"manager", "auditor"},
		Attributes: map[string]any{"email": "jane@acme.com", "level": float64(3)},
	}

	type testCase struct {
		name  string
		value any
		// decode returns the value re-read from its own encoding.
		decode func(t *testing.T, raw []byte) any
	}

	cases := []testCase{
		{
			name:  "claim",
			value: humantask.Claim{Actor: actor, At: at},
			decode: func(t *testing.T, raw []byte) any {
				var got humantask.Claim
				require.NoError(t, json.Unmarshal(raw, &got))
				return got
			},
		},
		{
			name:  "completion",
			value: humantask.Completion{Actor: actor, At: at, Outcome: "approve", Note: "budget confirmed"},
			decode: func(t *testing.T, raw []byte) any {
				var got humantask.Completion
				require.NoError(t, json.Unmarshal(raw, &got))
				return got
			},
		},
		{
			name:  "completion without outcome or note",
			value: humantask.Completion{Actor: actor, At: at},
			decode: func(t *testing.T, raw []byte) any {
				var got humantask.Completion
				require.NoError(t, json.Unmarshal(raw, &got))
				return got
			},
		},
		{
			name:  "actor with no roles or attributes",
			value: authz.Actor{ID: "u-bare"},
			decode: func(t *testing.T, raw []byte) any {
				var got authz.Actor
				require.NoError(t, json.Unmarshal(raw, &got))
				return got
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			raw, err := json.Marshal(tc.value)
			require.NoError(t, err)

			got := tc.decode(t, raw)
			require.Equal(t, tc.value, got, "the type must read back exactly what it wrote")

			// Encoding the decoded value must reproduce the same bytes, so an
			// asymmetric tag (marshal-only or decode-only) cannot slip through.
			again, err := json.Marshal(got)
			require.NoError(t, err)
			require.JSONEq(t, string(raw), string(again), "re-encoding must be stable")
		})
	}
}
