package runtime_test

import (
	"context"
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

// TestNewProcessDriverDefaults verifies the all-optional constructor: zero-arg
// builds a usable driver with action.DefaultCatalog() and a MemInstanceStore,
// and that WithActionCatalog / WithInstanceStore override them correctly.
// Nil options are silently ignored (defaults stand).
func TestNewProcessDriverDefaults(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		build  func(t *testing.T) (*runtime.ProcessDriver, *kernel.MemInstanceStore)
		assert func(t *testing.T, d *runtime.ProcessDriver, store *kernel.MemInstanceStore, err error)
	}

	// Unique action name to avoid collisions with other parallel tests registering
	// into the global default catalog.
	const uniqueAction = "test-defaults-unique-greet-v1"
	var called atomic.Bool
	require.NoError(t, action.Register(uniqueAction, action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
		called.Store(true)
		return nil, nil
	})))

	cases := []testCase{
		{
			name: "zero-arg uses DefaultCatalog and runs registered action",
			build: func(t *testing.T) (*runtime.ProcessDriver, *kernel.MemInstanceStore) {
				t.Helper()
				d, err := runtime.NewProcessDriver()
				require.NoError(t, err)
				return d, nil
			},
			assert: func(t *testing.T, d *runtime.ProcessDriver, _ *kernel.MemInstanceStore, err error) {
				require.NoError(t, err)
				require.NotNil(t, d)

				called.Store(false)
				st, runErr := d.Run(t.Context(), oneNodeDef(uniqueAction), "inst-zero-arg", nil)
				require.NoError(t, runErr)
				assert.Equal(t, engine.StatusCompleted, st.Status)
				assert.True(t, called.Load(), "default catalog must resolve the registered action")
			},
		},
		{
			name: "WithActionCatalog overrides default catalog",
			build: func(t *testing.T) (*runtime.ProcessDriver, *kernel.MemInstanceStore) {
				t.Helper()
				var customCalled atomic.Bool
				custom := action.NewMapCatalog(map[string]action.Action{
					"custom-action": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
						customCalled.Store(true)
						return nil, nil
					}),
				})
				d, err := runtime.NewProcessDriver(runtime.WithActionCatalog(custom))
				require.NoError(t, err)
				st, runErr := d.Run(t.Context(), oneNodeDef("custom-action"), "inst-custom-cat", nil)
				require.NoError(t, runErr)
				assert.Equal(t, engine.StatusCompleted, st.Status)
				assert.True(t, customCalled.Load(), "custom catalog action must have been called")
				return d, nil
			},
			assert: func(t *testing.T, d *runtime.ProcessDriver, _ *kernel.MemInstanceStore, err error) {
				require.NoError(t, err)
				require.NotNil(t, d)
			},
		},
		{
			name: "WithInstanceStore overrides default store — instance retrievable via Load",
			build: func(t *testing.T) (*runtime.ProcessDriver, *kernel.MemInstanceStore) {
				t.Helper()
				customStore, storeErr := kernel.NewMemInstanceStore()
				require.NoError(t, storeErr)
				d, err := runtime.NewProcessDriver(runtime.WithInstanceStore(customStore))
				require.NoError(t, err)
				return d, customStore
			},
			assert: func(t *testing.T, d *runtime.ProcessDriver, store *kernel.MemInstanceStore, err error) {
				require.NoError(t, err)
				require.NotNil(t, d)

				// Run a process so the instance is persisted in the custom store.
				called.Store(false)
				st, runErr := d.Run(t.Context(), oneNodeDef(uniqueAction), "inst-custom-store", nil)
				require.NoError(t, runErr)
				assert.Equal(t, engine.StatusCompleted, st.Status)

				// The custom store must hold the completed instance.
				loaded, _, loadErr := store.Load(t.Context(), "inst-custom-store")
				require.NoError(t, loadErr)
				assert.Equal(t, engine.StatusCompleted, loaded.Status)
			},
		},
		{
			name: "WithActionCatalog(nil) is ignored — default catalog still in effect",
			build: func(t *testing.T) (*runtime.ProcessDriver, *kernel.MemInstanceStore) {
				t.Helper()
				d, err := runtime.NewProcessDriver(runtime.WithActionCatalog(nil))
				require.NoError(t, err)
				return d, nil
			},
			assert: func(t *testing.T, d *runtime.ProcessDriver, _ *kernel.MemInstanceStore, err error) {
				require.NoError(t, err)
				require.NotNil(t, d)

				called.Store(false)
				st, runErr := d.Run(t.Context(), oneNodeDef(uniqueAction), "inst-nil-cat", nil)
				require.NoError(t, runErr)
				assert.Equal(t, engine.StatusCompleted, st.Status)
				assert.True(t, called.Load(), "nil catalog must be ignored; default catalog resolves the action")
			},
		},
		{
			name: "WithInstanceStore(nil) is ignored — default in-memory store still in effect",
			build: func(t *testing.T) (*runtime.ProcessDriver, *kernel.MemInstanceStore) {
				t.Helper()
				d, err := runtime.NewProcessDriver(runtime.WithInstanceStore(nil))
				require.NoError(t, err)
				return d, nil
			},
			assert: func(t *testing.T, d *runtime.ProcessDriver, _ *kernel.MemInstanceStore, err error) {
				require.NoError(t, err)
				require.NotNil(t, d)

				called.Store(false)
				st, runErr := d.Run(t.Context(), oneNodeDef(uniqueAction), "inst-nil-store", nil)
				require.NoError(t, runErr)
				assert.Equal(t, engine.StatusCompleted, st.Status)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, store := tc.build(t)
			tc.assert(t, d, store, nil)
		})
	}
}
