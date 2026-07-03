package calllink_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime/calllink"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// minimalDef is a trivial single-node definition; the notifier only needs a
// non-nil *model.ProcessDefinition to pass to the deliver callback.
func minimalDef(id string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      id,
		Version: 1,
		Nodes:   []model.Node{model.NewStartEvent("start")},
	}
}

// TestCallNotifierOptionsDrainAndRun exercises the functional options, both
// DrainOnce outcome branches (completed + failed), and Run's cancellation
// contract without standing up a full parent/child process. It seeds terminal
// links directly via MemCallLinkStore.Seed/SeedTerminal.
func TestCallNotifierOptionsDrainAndRun(t *testing.T) {
	cl := kernel.NewMemCallLinkStore()
	reg := kernel.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{
		"parent:1": minimalDef("parent"),
	})

	// Seed one completed and one failed terminal link, plus one whose parent
	// definition is not registered (exercises the lookup-failure skip branch).
	cl.Seed(kernel.CallLink{ChildInstanceID: "c-ok", ParentInstanceID: "p-ok", ParentCommandID: "p-ok-c1", ParentDefID: "parent", ParentDefVersion: 1, Depth: 1})
	cl.SeedTerminal("c-ok", kernel.CallOutcome{Completed: true, Output: map[string]any{"result": 42}})
	cl.Seed(kernel.CallLink{ChildInstanceID: "c-fail", ParentInstanceID: "p-fail", ParentCommandID: "p-fail-c1", ParentDefID: "parent", ParentDefVersion: 1, Depth: 1})
	cl.SeedTerminal("c-fail", kernel.CallOutcome{Completed: false, Err: "child failed"})
	cl.Seed(kernel.CallLink{ChildInstanceID: "c-skip", ParentInstanceID: "p-skip", ParentCommandID: "p-skip-c1", ParentDefID: "missing", ParentDefVersion: 1, Depth: 1})
	cl.SeedTerminal("c-skip", kernel.CallOutcome{Completed: true})

	var completed, failed int
	deliver := func(_ context.Context, def *model.ProcessDefinition, _ string, trg engine.Trigger) error {
		require.NotNil(t, def)
		switch trg.(type) {
		case engine.SubInstanceCompleted:
			completed++
		case engine.SubInstanceFailed:
			failed++
		}
		return nil
	}

	n, err := calllink.NewCallNotifier(cl, deliver, reg,
		calllink.WithCallNotifierBatchSize(10),
		calllink.WithCallNotifierPollInterval(time.Millisecond),
		calllink.WithCallNotifierClock(clock.System()),
		calllink.WithCallNotifierLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	require.NoError(t, err)

	got, err := n.DrainOnce(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 2, got, "both resolvable terminal links must be drained; the unknown-def link is skipped")
	assert.Equal(t, 1, completed)
	assert.Equal(t, 1, failed)

	// A second drain is a no-op: the delivered links are now marked notified,
	// and the unknown-def link is skipped again.
	got, err = n.DrainOnce(t.Context())
	require.NoError(t, err)
	assert.Zero(t, got)

	// Run returns ctx.Err() when its context is already cancelled (the immediate
	// drain observes the cancellation).
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assert.ErrorIs(t, n.Run(ctx), context.Canceled)

	// Run's polling loop: start it live, let it tick at least once, then cancel
	// and confirm it returns context.Canceled.
	runCtx, runCancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- n.Run(runCtx) }()
	time.Sleep(10 * time.Millisecond) // let the 1ms-poll ticker fire several times
	runCancel()
	select {
	case rerr := <-done:
		assert.ErrorIs(t, rerr, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after cancel")
	}
}
