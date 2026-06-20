// Package humantask_test verifies the exported types and errors of the humantask package.
package humantask_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zakyalvan/krtlwrkflw/humantask"
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
