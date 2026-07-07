package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/service"
)

// TestEngineLifecycleOwnedDriver verifies that a zero-config engine (which owns
// its default driver, and thus its in-process scheduler) starts and shuts down
// cleanly and idempotently.
func TestEngineLifecycleOwnedDriver(t *testing.T) {
	e, err := service.NewEngine()
	require.NoError(t, err)

	ctx := t.Context()

	// Start is idempotent and returns nil.
	require.NoError(t, e.Start(ctx))
	require.NoError(t, e.Start(ctx))

	// Shutdown is idempotent and returns nil, even after Start.
	require.NoError(t, e.Shutdown(ctx))
	require.NoError(t, e.Shutdown(ctx))
}

// TestEngineLifecycleZeroConfigShutdownOnly verifies that Shutdown on a
// never-started zero-config engine returns nil and stays idempotent.
func TestEngineLifecycleZeroConfigShutdownOnly(t *testing.T) {
	e, err := service.NewEngine()
	require.NoError(t, err)

	ctx := t.Context()
	require.NoError(t, e.Shutdown(ctx))
	require.NoError(t, e.Shutdown(ctx))
}

// TestEngineShutdownLeavesInjectedDriverUntouched verifies that when the driver
// is supplied by the consumer via WithProcessDriver, Engine.Shutdown does NOT
// tear it down: the injected driver remains usable afterwards.
func TestEngineShutdownLeavesInjectedDriverUntouched(t *testing.T) {
	h := newHarness(t)
	e := h.newEngine(t)

	ctx := t.Context()
	require.NoError(t, e.Shutdown(ctx))

	// The injected driver must still be usable: Start must not report
	// scheduling.ErrSchedulerClosed (which it would if it had been shut down).
	err := h.runner.Start(context.Background())
	assert.NoError(t, err)

	// Clean up the consumer-owned driver ourselves so it does not leak.
	t.Cleanup(func() {
		_ = h.runner.Shutdown(context.Background())
	})
}
