package runtime_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// oneNodeDef returns a minimal process: start → serviceTask(actionName) → end.
func oneNodeDef(actionName string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "one-node-" + actionName,
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("task", activity.WithActionName(actionName)),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
}

// defaultCatalogActions holds per-action singletons registered once into the
// global DefaultCatalog. Registering once and using an atomic call counter
// avoids re-registration errors across -count=N runs in the same process.
var (
	defaultCatalogOnce sync.Once

	// Per-name call counters for the three subtests that exercise the default catalog.
	zeroArgCalls   atomic.Int64
	nilCatCalls    atomic.Int64
	nilStoreCalls  atomic.Int64
	instanceStoreC atomic.Int64
)

// ensureDefaultCatalogActions registers the four default-catalog action names
// exactly once per process, capturing each subtest's counter by pointer.
func ensureDefaultCatalogActions(t *testing.T) {
	t.Helper()
	defaultCatalogOnce.Do(func() {
		names := []struct {
			name    string
			counter *atomic.Int64
		}{
			{"test-defaults-zeroarg-v1", &zeroArgCalls},
			{"test-defaults-nilcat-v1", &nilCatCalls},
			{"test-defaults-nilstore-v1", &nilStoreCalls},
			{"test-defaults-instancestore-v1", &instanceStoreC},
		}
		for _, n := range names {
			counter := n.counter // capture
			err := action.Register(n.name, action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
				counter.Add(1)
				return nil, nil
			}))
			// ErrActionExists can occur in pathological cases; treat it as a
			// no-op because the singleton is already wired.
			if err != nil && !errors.Is(err, action.ErrActionExists) {
				t.Errorf("ensureDefaultCatalogActions: unexpected registration error: %v", err)
			}
		}
	})
}

// TestNewProcessDriverDefaults verifies the all-optional constructor: zero-arg
// builds a usable driver with action.DefaultCatalog() and a MemInstanceStore,
// and that WithActionCatalog / WithInstanceStore override them correctly.
// Nil options are silently ignored (defaults stand).
//
// Each subtest that exercises the global default catalog uses a dedicated
// per-action call counter (registered once via sync.Once) so that parallel
// subtests and repeated -count=N runs in the same process never share mutable
// state across independent observations.
func TestNewProcessDriverDefaults(t *testing.T) {
	t.Parallel()

	// Ensure the default-catalog singletons are wired before any subtest runs.
	ensureDefaultCatalogActions(t)

	t.Run("zero-arg uses DefaultCatalog and runs registered action", func(t *testing.T) {
		t.Parallel()

		baseline := zeroArgCalls.Load()

		d, err := runtime.NewProcessDriver()
		require.NoError(t, err)
		require.NotNil(t, d)

		st, runErr := d.Run(t.Context(), oneNodeDef("test-defaults-zeroarg-v1"), "inst-zero-arg", nil)
		require.NoError(t, runErr)
		assert.Equal(t, engine.StatusCompleted, st.Status)
		assert.Greater(t, zeroArgCalls.Load(), baseline, "default catalog must resolve and invoke the registered action")
	})

	t.Run("WithActionCatalog overrides default catalog", func(t *testing.T) {
		t.Parallel()

		// Self-contained: own local flag + local MapCatalog; does not touch the global catalog.
		var customCalled atomic.Bool
		custom := action.NewMapCatalog(map[string]action.Action{
			"custom-action-v1": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
				customCalled.Store(true)
				return nil, nil
			}),
		})
		d, err := runtime.NewProcessDriver(runtime.WithActionCatalog(custom))
		require.NoError(t, err)
		require.NotNil(t, d)

		st, runErr := d.Run(t.Context(), oneNodeDef("custom-action-v1"), "inst-custom-cat", nil)
		require.NoError(t, runErr)
		assert.Equal(t, engine.StatusCompleted, st.Status)
		assert.True(t, customCalled.Load(), "custom catalog action must have been called")
	})

	t.Run("WithInstanceStore overrides default store — instance retrievable via Load", func(t *testing.T) {
		t.Parallel()

		baseline := instanceStoreC.Load()

		customStore, storeErr := kernel.NewMemInstanceStore()
		require.NoError(t, storeErr)

		d, err := runtime.NewProcessDriver(runtime.WithInstanceStore(customStore))
		require.NoError(t, err)
		require.NotNil(t, d)

		// Run a process so the instance is persisted in the custom store.
		st, runErr := d.Run(t.Context(), oneNodeDef("test-defaults-instancestore-v1"), "inst-custom-store", nil)
		require.NoError(t, runErr)
		assert.Equal(t, engine.StatusCompleted, st.Status)
		assert.Greater(t, instanceStoreC.Load(), baseline, "action must have been invoked")

		// The custom store must hold the completed instance.
		loaded, _, loadErr := customStore.Load(t.Context(), "inst-custom-store")
		require.NoError(t, loadErr)
		assert.Equal(t, engine.StatusCompleted, loaded.Status)
	})

	t.Run("WithActionCatalog(nil) is ignored — default catalog still in effect", func(t *testing.T) {
		t.Parallel()

		baseline := nilCatCalls.Load()

		d, err := runtime.NewProcessDriver(runtime.WithActionCatalog(nil))
		require.NoError(t, err)
		require.NotNil(t, d)

		st, runErr := d.Run(t.Context(), oneNodeDef("test-defaults-nilcat-v1"), "inst-nil-cat", nil)
		require.NoError(t, runErr)
		assert.Equal(t, engine.StatusCompleted, st.Status)
		assert.Greater(t, nilCatCalls.Load(), baseline, "nil catalog must be ignored; default catalog resolves the action")
	})

	t.Run("WithInstanceStore(nil) is ignored — default in-memory store still in effect", func(t *testing.T) {
		t.Parallel()

		baseline := nilStoreCalls.Load()

		d, err := runtime.NewProcessDriver(runtime.WithInstanceStore(nil))
		require.NoError(t, err)
		require.NotNil(t, d)

		st, runErr := d.Run(t.Context(), oneNodeDef("test-defaults-nilstore-v1"), "inst-nil-store", nil)
		require.NoError(t, runErr)
		assert.Equal(t, engine.StatusCompleted, st.Status)
		assert.Greater(t, nilStoreCalls.Load(), baseline, "action must have been invoked through the default store path")
	})
}
