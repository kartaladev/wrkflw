package runtime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func runningChild(id string) engine.InstanceState {
	return engine.InstanceState{InstanceID: id, DefID: "child", DefVersion: 1, Status: engine.StatusRunning}
}

func TestMemStoreRecordsCallLinkOnCreate(t *testing.T) {
	cl := runtime.NewMemCallLinkStore()
	store := runtime.NewMemStoreWithCallLinks(cl)

	link := &runtime.CallLink{
		ChildInstanceID:  "p-sub-c1",
		ParentInstanceID: "p",
		ParentCommandID:  "p-c1",
		ParentDefID:      "parent",
		ParentDefVersion: 1,
		Depth:            1,
	}
	_, err := store.Create(t.Context(), runtime.AppliedStep{
		State:       runningChild("p-sub-c1"),
		Trigger:     startTrg(), // existing helper: engine.NewStartInstance(...)
		NewCallLink: link,
	})
	require.NoError(t, err)

	got, ok, err := cl.LookupChild(t.Context(), "p-sub-c1")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "p", got.ParentInstanceID)
	assert.Equal(t, "p-c1", got.ParentCommandID)
}

func TestMemStoreFlipsCallLinkOnTerminalCommit(t *testing.T) {
	cl := runtime.NewMemCallLinkStore()
	store := runtime.NewMemStoreWithCallLinks(cl)

	tok, err := store.Create(t.Context(), runtime.AppliedStep{
		State:       runningChild("p-sub-c1"),
		Trigger:     startTrg(),
		NewCallLink: &runtime.CallLink{ChildInstanceID: "p-sub-c1", ParentInstanceID: "p", ParentCommandID: "p-c1", ParentDefID: "parent", ParentDefVersion: 1, Depth: 1},
	})
	require.NoError(t, err)

	done := runningChild("p-sub-c1")
	done.Status = engine.StatusCompleted
	done.Variables = map[string]any{"result": 42}
	_, err = store.Commit(t.Context(), tok, runtime.AppliedStep{
		State:       done,
		Trigger:     startTrg(),
		CallOutcome: &runtime.CallOutcome{Completed: true, Output: map[string]any{"result": 42}},
	})
	require.NoError(t, err)

	pending, err := cl.ClaimPending(t.Context(), 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "p-sub-c1", pending[0].Link.ChildInstanceID)
	assert.True(t, pending[0].Outcome.Completed)
	assert.Equal(t, 42, pending[0].Outcome.Output["result"])
}
