// Package humantask_test verifies the exported types and errors of the humantask package.
package humantask_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/humantask"
	"github.com/stretchr/testify/assert"
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
