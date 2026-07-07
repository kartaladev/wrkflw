package runtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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
	"github.com/zakyalvan/krtlwrkflw/processtest"
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
	zeroArgCalls       atomic.Int64
	nilCatCalls        atomic.Int64
	nilStoreCalls      atomic.Int64
	instanceStoreCalls atomic.Int64
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
			{"test-defaults-instancestore-v1", &instanceStoreCalls},
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
		t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

		st, runErr := d.Drive(t.Context(), oneNodeDef("test-defaults-zeroarg-v1"), "inst-zero-arg", nil)
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
		t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

		st, runErr := d.Drive(t.Context(), oneNodeDef("custom-action-v1"), "inst-custom-cat", nil)
		require.NoError(t, runErr)
		assert.Equal(t, engine.StatusCompleted, st.Status)
		assert.True(t, customCalled.Load(), "custom catalog action must have been called")
	})

	t.Run("WithInstanceStore overrides default store — instance retrievable via Load", func(t *testing.T) {
		t.Parallel()

		baseline := instanceStoreCalls.Load()

		customStore, storeErr := kernel.NewMemInstanceStore()
		require.NoError(t, storeErr)

		d, err := runtime.NewProcessDriver(runtime.WithInstanceStore(customStore))
		require.NoError(t, err)
		require.NotNil(t, d)
		t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

		// Run a process so the instance is persisted in the custom store.
		st, runErr := d.Drive(t.Context(), oneNodeDef("test-defaults-instancestore-v1"), "inst-custom-store", nil)
		require.NoError(t, runErr)
		assert.Equal(t, engine.StatusCompleted, st.Status)
		assert.Greater(t, instanceStoreCalls.Load(), baseline, "action must have been invoked")

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
		t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

		st, runErr := d.Drive(t.Context(), oneNodeDef("test-defaults-nilcat-v1"), "inst-nil-cat", nil)
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
		t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

		st, runErr := d.Drive(t.Context(), oneNodeDef("test-defaults-nilstore-v1"), "inst-nil-store", nil)
		require.NoError(t, runErr)
		assert.Equal(t, engine.StatusCompleted, st.Status)
		assert.Greater(t, nilStoreCalls.Load(), baseline, "action must have been invoked through the default store path")
	})
}

// debugLogEntry is the shape of a single JSON line written by slog.NewJSONHandler
// at LevelDebug. We only assert on the fields our construction-summary emits.
type debugLogEntry struct {
	Level   string `json:"level"`
	Msg     string `json:"msg"`
	Store   string `json:"store"`
	Catalog string `json:"catalog"`
	Sched   string `json:"scheduler"`
}

// TestNewProcessDriverConstructionSummary verifies that NewProcessDriver emits a
// single DEBUG log record with message "ProcessDriver constructed" that carries the
// expected attribute values for store, catalog, and feature flags.
func TestNewProcessDriverConstructionSummary(t *testing.T) {
	t.Parallel()

	t.Run("zero-config emits in-memory store and default-global catalog", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer
		handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
		logger := slog.New(handler)

		d, err := runtime.NewProcessDriver(runtime.WithLogger(logger))
		require.NoError(t, err)
		t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

		// Expect exactly one JSON line from the construction summary.
		lines := splitNonEmpty(buf.Bytes())
		require.Len(t, lines, 1, "expected exactly one log record from the construction summary")

		var entry debugLogEntry
		require.NoError(t, json.Unmarshal(lines[0], &entry))
		assert.Equal(t, "DEBUG", entry.Level)
		assert.Equal(t, "ProcessDriver constructed", entry.Msg)
		assert.Equal(t, "in-memory(non-durable)", entry.Store, "zero-config must report in-memory store")
		assert.Equal(t, "default-global", entry.Catalog, "zero-config must report default-global catalog")
		assert.Equal(t, "default-inprocess", entry.Sched, "zero-config must report the in-process default scheduler")

		// Also confirm the hint attribute is present by scanning the raw JSON.
		assert.Contains(t, string(lines[0]), "in-memory store is not durable", "hint attribute must mention durability")
	})

	t.Run("WithScheduler and custom store flip scheduler=on and store=custom", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer
		handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
		logger := slog.New(handler)

		customStore, err := kernel.NewMemInstanceStore()
		require.NoError(t, err)

		sched := processtest.NewMemScheduler()

		_, buildErr := runtime.NewProcessDriver(
			runtime.WithLogger(logger),
			runtime.WithInstanceStore(customStore),
			runtime.WithScheduler(sched),
		)
		require.NoError(t, buildErr)

		lines := splitNonEmpty(buf.Bytes())
		require.Len(t, lines, 1, "expected exactly one log record from the construction summary")

		var entry debugLogEntry
		require.NoError(t, json.Unmarshal(lines[0], &entry))
		assert.Equal(t, "DEBUG", entry.Level)
		assert.Equal(t, "ProcessDriver constructed", entry.Msg)
		assert.Equal(t, "custom", entry.Store, "custom store must be reported as custom")
		assert.Equal(t, "custom", entry.Sched, "an injected scheduler must be reported as custom")
	})
}

// splitNonEmpty splits buf on newlines and returns non-empty lines.
func splitNonEmpty(buf []byte) [][]byte {
	var out [][]byte
	for _, line := range bytes.Split(buf, []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			out = append(out, line)
		}
	}
	return out
}

// Ensure context import is used (t.Context() is used above, but context is also
// pulled in as a package reference in the JSON handler options).
var _ = context.Background
