package kernel_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

func runningChild(id string) engine.InstanceState {
	return engine.InstanceState{InstanceID: id, DefID: "child", DefVersion: 1, Status: engine.StatusRunning}
}

func TestMemStoreRecordsCallLinkOnCreate(t *testing.T) {
	cl := kernel.NewMemCallLinkStore()
	store := runtimetest.MustMemStore(t, kernel.WithCallLinks(cl))

	link := &kernel.CallLink{
		ChildInstanceID:  "p-sub-c1",
		ParentInstanceID: "p",
		ParentCommandID:  "p-c1",
		ParentDefID:      "parent",
		ParentDefVersion: 1,
		Depth:            1,
	}
	_, err := store.Create(t.Context(), kernel.AppliedStep{
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

// TestWithMemCallLinkClockNilFallsBackToSystem asserts that passing a nil clock
// to WithMemCallLinkClock does NOT overwrite the default clock.System(). The
// guard is verified via ClaimPending in lease mode — that path calls clk.Now()
// to evaluate the lease cutoff. A nil clock would panic there.
func TestWithMemCallLinkClockNilFallsBackToSystem(t *testing.T) {
	cl := kernel.NewMemCallLinkStore(
		kernel.WithMemCallLinkLease("replica-X", 30*time.Second),
		kernel.WithMemCallLinkClock(nil), // must be ignored; clock.System() must survive
	)

	// Insert a terminal-but-unnotified link to exercise the clock path.
	store := runtimetest.MustMemStore(t, kernel.WithCallLinks(cl))
	link := &kernel.CallLink{
		ChildInstanceID:  "nil-clk-child",
		ParentInstanceID: "nil-clk-parent",
		ParentCommandID:  "cmd-1",
		ParentDefID:      "def-1",
		ParentDefVersion: 1,
		Depth:            1,
	}
	tok, err := store.Create(t.Context(), kernel.AppliedStep{
		State:       runningChild("nil-clk-child"),
		Trigger:     startTrg(),
		NewCallLink: link,
	})
	require.NoError(t, err)

	done := runningChild("nil-clk-child")
	done.Status = engine.StatusCompleted
	_, err = store.Commit(t.Context(), tok, kernel.AppliedStep{
		State:       done,
		Trigger:     startTrg(),
		CallOutcome: &kernel.CallOutcome{Completed: true},
	})
	require.NoError(t, err)

	// ClaimPending in lease mode calls clk.Now(); a nil clock would panic.
	assert.NotPanics(t, func() {
		_, _ = cl.ClaimPending(t.Context(), 10)
	}, "WithMemCallLinkClock(nil) must be ignored; clk.Now() must not panic in lease mode")
}

func TestMemStoreFlipsCallLinkOnTerminalCommit(t *testing.T) {
	cl := kernel.NewMemCallLinkStore()
	store := runtimetest.MustMemStore(t, kernel.WithCallLinks(cl))

	tok, err := store.Create(t.Context(), kernel.AppliedStep{
		State:       runningChild("p-sub-c1"),
		Trigger:     startTrg(),
		NewCallLink: &kernel.CallLink{ChildInstanceID: "p-sub-c1", ParentInstanceID: "p", ParentCommandID: "p-c1", ParentDefID: "parent", ParentDefVersion: 1, Depth: 1},
	})
	require.NoError(t, err)

	done := runningChild("p-sub-c1")
	done.Status = engine.StatusCompleted
	done.Variables = map[string]any{"result": 42}
	_, err = store.Commit(t.Context(), tok, kernel.AppliedStep{
		State:       done,
		Trigger:     startTrg(),
		CallOutcome: &kernel.CallOutcome{Completed: true, Output: map[string]any{"result": 42}},
	})
	require.NoError(t, err)

	pending, err := cl.ClaimPending(t.Context(), 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "p-sub-c1", pending[0].Link.ChildInstanceID)
	assert.True(t, pending[0].Outcome.Completed)
	assert.Equal(t, 42, pending[0].Outcome.Output["result"])
}
