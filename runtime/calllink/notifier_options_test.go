package calllink_test

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/calllink"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// minimalDef is a trivial single-node definition; the notifier only needs a
// non-nil *model.ProcessDefinition to pass to the deliver callback.
func minimalDef(id string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      id,
		Version: 1,
		Nodes:   []model.Node{event.NewStart("start")},
	}
}

// seedTerminalLink inserts a terminal call link (parent def "parent:1") into the
// reference store via the shared runtimetest helper, so a drain can claim and
// deliver it.
func seedTerminalLink(t *testing.T, cl *kernel.MemCallLinkStore, child, parent string, completed bool) {
	t.Helper()
	link := kernel.CallLink{
		ChildInstanceID:  child,
		ParentInstanceID: parent,
		ParentCommandID:  parent + "-c1",
		ParentDefID:      "parent",
		ParentDefVersion: 1,
		Depth:            1,
	}
	out := kernel.CallOutcome{Completed: true, Output: map[string]any{"result": 42}}
	if !completed {
		out = kernel.CallOutcome{Completed: false, Err: "child failed"}
	}
	runtimetest.SeedTerminalCallLink(t, cl, link, out)
}

// TestCallNotifierOptionsAndDrainBranches exercises every functional option and
// both DrainOnce outcome branches (completed + failed) plus the unknown-def skip
// branch, then asserts the second drain does not redeliver already-notified links.
func TestCallNotifierOptionsAndDrainBranches(t *testing.T) {
	cl := kernel.NewMemCallLinkStore()
	reg := kernel.NewMapDefinitionRegistry(minimalDef("parent"))

	seedTerminalLink(t, cl, "c-ok", "p-ok", true)
	seedTerminalLink(t, cl, "c-fail", "p-fail", false)
	// Unknown parent def → DrainOnce resolves nothing and skips (continue branch).
	runtimetest.SeedTerminalCallLink(t, cl, kernel.CallLink{ChildInstanceID: "c-skip", ParentInstanceID: "p-skip", ParentCommandID: "p-skip-c1", ParentDefID: "missing", ParentDefVersion: 1, Depth: 1}, kernel.CallOutcome{Completed: true})

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
		calllink.WithClock(clock.System()),
		calllink.WithCallNotifierLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	require.NoError(t, err)

	got, err := n.DrainOnce(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 2, got, "both resolvable terminal links drain; the unknown-def link is skipped")
	assert.Equal(t, 1, completed)
	assert.Equal(t, 1, failed)

	// Second drain: the two delivered links are now marked notified, so the
	// deliver callback must NOT fire again (counters unchanged). The unknown-def
	// link is re-scanned and re-skipped, contributing nothing.
	got, err = n.DrainOnce(t.Context())
	require.NoError(t, err)
	assert.Zero(t, got)
	assert.Equal(t, 1, completed, "an already-notified completed link must not be redelivered")
	assert.Equal(t, 1, failed, "an already-notified failed link must not be redelivered")
}

// TestCallNotifierRunRedrainsOnTick proves Run's ticker branch (case <-ticker.C)
// actually redrains: a link seeded AFTER Run has finished its immediate drain can
// only be delivered by a subsequent tick. Using the "early then late" ordering
// makes this deterministic — observing the early delivery guarantees the immediate
// drain has already returned, so the late delivery must come from the ticker.
func TestCallNotifierRunRedrainsOnTick(t *testing.T) {
	cl := kernel.NewMemCallLinkStore()
	reg := kernel.NewMapDefinitionRegistry(minimalDef("parent"))

	var delivered atomic.Int64
	deliver := func(context.Context, *model.ProcessDefinition, string, engine.Trigger) error {
		delivered.Add(1)
		return nil
	}
	n, err := calllink.NewCallNotifier(cl, deliver, reg, calllink.WithCallNotifierPollInterval(time.Millisecond))
	require.NoError(t, err)

	seedTerminalLink(t, cl, "c-early", "p-early", true) // present before Run's immediate drain

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- n.Run(ctx) }()

	require.Eventually(t, func() bool { return delivered.Load() == 1 }, 2*time.Second, time.Millisecond,
		"the early link must be delivered (immediate drain), confirming Run reached its select loop")

	// Seeded strictly after the immediate drain returned — only a ticker tick can pick it up.
	seedTerminalLink(t, cl, "c-late", "p-late", true)
	require.Eventually(t, func() bool { return delivered.Load() == 2 }, 2*time.Second, time.Millisecond,
		"the late link must be redelivered by the ticker branch")

	cancel()
	select {
	case rerr := <-done:
		assert.ErrorIs(t, rerr, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after cancel")
	}
}

// TestCallNotifierRunHonorsCancelledContext covers Run's immediate-drain
// cancellation path: an already-cancelled context returns ctx.Err() before any tick.
func TestCallNotifierRunHonorsCancelledContext(t *testing.T) {
	cl := kernel.NewMemCallLinkStore()
	reg := kernel.NewMapDefinitionRegistry(nil)
	deliver := func(context.Context, *model.ProcessDefinition, string, engine.Trigger) error { return nil }
	n, err := calllink.NewCallNotifier(cl, deliver, reg)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assert.ErrorIs(t, n.Run(ctx), context.Canceled)
}
