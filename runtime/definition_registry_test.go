package runtime_test

// definition_registry_test.go — black-box tests for the process-global
// default DefinitionRegistry and the ergonomic RegisterDefinition /
// MustRegisterDefinition / DefaultDefinitionRegistry API.
//
// Test categories:
//   (a) DefaultDefinitionRegistry identity + RegisterDefinition delegates to it.
//   (b) Driver default: zero-config NewProcessDriver runs a parent with a
//       KindCallActivity whose DefRef is registered via runtime.RegisterDefinition.
//   (c) WithDefinitions(nil) is ignored; WithDefinitions(custom) overrides.
//   (d) DEBUG construction summary: definitions=default-global vs definitions=custom.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	"log/slog"
)

// ── (a) DefaultDefinitionRegistry identity + RegisterDefinition ───────────

// TestDefaultDefinitionRegistryIdentity verifies that DefaultDefinitionRegistry
// returns the same *MemDefinitionRegistry pointer on every call (process-global
// singleton) and that RegisterDefinition delegates to it.
func TestDefaultDefinitionRegistryIdentity(t *testing.T) {
	t.Parallel()

	reg1 := runtime.DefaultDefinitionRegistry()
	reg2 := runtime.DefaultDefinitionRegistry()

	// Bind to two separate locals to satisfy staticcheck SA4000 (both used below).
	assert.NotNil(t, reg1, "DefaultDefinitionRegistry must not return nil")
	assert.Same(t, reg1, reg2, "DefaultDefinitionRegistry must return the same instance each call")
}

// TestRegisterDefinitionDelegatesToDefault verifies that runtime.RegisterDefinition
// registers into DefaultDefinitionRegistry so Lookup finds it.
func TestRegisterDefinitionDelegatesToDefault(t *testing.T) {
	t.Parallel()

	// Use a unique ID to avoid collisions with other tests sharing the global.
	def := &model.ProcessDefinition{
		ID:      fmt.Sprintf("test-reg-def-identity-%d", uniqueDefSeq.Add(1)),
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("s"), event.NewEnd("e"),
		},
		Flows: []flow.SequenceFlow{{ID: "f1", Source: "s", Target: "e"}},
	}

	err := runtime.RegisterDefinition(def)
	require.NoError(t, err)

	got, lookupErr := runtime.DefaultDefinitionRegistry().Lookup(t.Context(), model.Latest(def.ID))
	require.NoError(t, lookupErr)
	assert.Equal(t, def.ID, got.ID)
}

// TestMustRegisterDefinitionPanicsOnDuplicate verifies that MustRegisterDefinition
// panics when the versioned key is already registered.
func TestMustRegisterDefinitionPanicsOnDuplicate(t *testing.T) {
	t.Parallel()

	def := &model.ProcessDefinition{
		ID:      fmt.Sprintf("test-mustreg-dup-%d", uniqueDefSeq.Add(1)),
		Version: 1,
		Nodes:   []model.Node{event.NewStart("s"), event.NewEnd("e")},
		Flows:   []flow.SequenceFlow{{ID: "f1", Source: "s", Target: "e"}},
	}

	// First registration must succeed.
	require.NotPanics(t, func() { runtime.MustRegisterDefinition(def) })

	// Second registration of the same ID:Version must panic.
	assert.Panics(t, func() { runtime.MustRegisterDefinition(def) })
}

// ── (b) Driver default: zero-config uses default registry ────────────────

// defaultDefsOnce ensures the sub-definition used by the driver-default test is
// registered into the global registry exactly once per process, using a unique
// ID derived from a counter (avoids conflicts with other subtests).
var (
	defaultDefsOnce    sync.Once
	defaultDefSubID    string
	defaultDefSubCalls atomic.Int64
)

// ensureDefaultSubDef registers the sub-definition used by the driver-default test
// into action.DefaultCatalog (for its action) and into runtime.DefaultDefinitionRegistry.
// Called inside a sync.Once so repeated -count=N runs are safe.
func ensureDefaultSubDef(t *testing.T) (parentDef *model.ProcessDefinition) {
	t.Helper()

	defaultDefsOnce.Do(func() {
		id := fmt.Sprintf("test-default-driver-sub-%d", uniqueDefSeq.Add(1))
		defaultDefSubID = id

		actionName := id + "-action"

		// Register the action into DefaultCatalog.
		err := action.Register(actionName, action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			defaultDefSubCalls.Add(1)
			return nil, nil
		}))
		if err != nil && !errors.Is(err, action.ErrActionExists) {
			t.Errorf("ensureDefaultSubDef: action register: %v", err)
		}

		// Build and register the sub-definition.
		subDef := &model.ProcessDefinition{
			ID:      id,
			Version: 1,
			Nodes: []model.Node{
				event.NewStart("s"),
				activity.NewServiceTask("svc", activity.WithTaskAction(actionName)),
				event.NewEnd("e"),
			},
			Flows: []flow.SequenceFlow{
				{ID: "f1", Source: "s", Target: "svc"},
				{ID: "f2", Source: "svc", Target: "e"},
			},
		}

		regErr := runtime.RegisterDefinition(subDef)
		if regErr != nil && !errors.Is(regErr, kernel.ErrDefinitionExists) {
			t.Errorf("ensureDefaultSubDef: def register: %v", regErr)
		}
	})

	// Build the parent definition (always fresh — it is passed directly to Run,
	// not registered, so no global-state issue).
	parentID := fmt.Sprintf("test-default-driver-parent-%d", uniqueDefSeq.Add(1))
	parent := &model.ProcessDefinition{
		ID:      parentID,
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("p-start"),
			activity.NewCallActivity("call", model.Latest(defaultDefSubID)),
			event.NewEnd("p-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "pf1", Source: "p-start", Target: "call"},
			{ID: "pf2", Source: "call", Target: "p-end"},
		},
	}

	return parent
}

// TestDriverDefaultUsesDefaultDefinitionRegistry verifies that a zero-config
// NewProcessDriver() automatically uses DefaultDefinitionRegistry so a parent
// definition with a KindCallActivity runs its sub-definition without any explicit
// WithDefinitions call.
func TestDriverDefaultUsesDefaultDefinitionRegistry(t *testing.T) {
	t.Parallel()

	parent := ensureDefaultSubDef(t)
	baseline := defaultDefSubCalls.Load()

	driver, err := runtime.NewProcessDriver()
	require.NoError(t, err)
	t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })

	instanceID := fmt.Sprintf("test-default-driver-inst-%d", uniqueDefSeq.Add(1))
	st, runErr := driver.Drive(t.Context(), parent, instanceID, nil)
	require.NoError(t, runErr)
	assert.Equal(t, engine.StatusCompleted, st.Status, "parent must complete when sub-def is in default registry")
	assert.Greater(t, defaultDefSubCalls.Load(), baseline, "sub-definition action must have been invoked")
}

// ── (c) WithDefinitions(nil) ignored; WithDefinitions(custom) overrides ──

// TestWithDefinitionsNilIgnored verifies that passing nil to WithDefinitions does
// not clobber the default registry.
func TestWithDefinitionsNilIgnored(t *testing.T) {
	t.Parallel()

	parent := ensureDefaultSubDef(t)
	baseline := defaultDefSubCalls.Load()

	driver, err := runtime.NewProcessDriver(runtime.WithDefinitions(nil))
	require.NoError(t, err)
	t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })

	instanceID := fmt.Sprintf("test-withdef-nil-inst-%d", uniqueDefSeq.Add(1))
	st, runErr := driver.Drive(t.Context(), parent, instanceID, nil)
	require.NoError(t, runErr)
	assert.Equal(t, engine.StatusCompleted, st.Status, "nil WithDefinitions must leave default registry in effect")
	assert.Greater(t, defaultDefSubCalls.Load(), baseline, "sub-definition action must have been invoked through default registry")
}

// TestWithDefinitionsCustomOverridesDefault verifies that a non-nil registry
// passed to WithDefinitions overrides the default and resolves definitions from it.
func TestWithDefinitionsCustomOverridesDefault(t *testing.T) {
	t.Parallel()

	// Create a fresh isolated sub-definition that is NOT registered in the global default.
	var customCalls atomic.Int64
	subActionName := fmt.Sprintf("test-custom-def-action-%d", uniqueDefSeq.Add(1))
	subID := fmt.Sprintf("test-custom-sub-%d", uniqueDefSeq.Add(1))

	// Register the action into the global catalog so the sub-process can execute.
	err := action.Register(subActionName, action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
		customCalls.Add(1)
		return nil, nil
	}))
	if err != nil && !errors.Is(err, action.ErrActionExists) {
		require.NoError(t, err)
	}

	subDef := &model.ProcessDefinition{
		ID:      subID,
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("s"),
			activity.NewServiceTask("svc", activity.WithTaskAction(subActionName)),
			event.NewEnd("e"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "s", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "e"},
		},
	}

	// Register ONLY into a custom registry — not into the global default.
	custom := kernel.NewMemDefinitionRegistry()
	require.NoError(t, custom.Register(subDef))

	parentID := fmt.Sprintf("test-custom-def-parent-%d", uniqueDefSeq.Add(1))
	parent := &model.ProcessDefinition{
		ID:      parentID,
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("p-start"),
			activity.NewCallActivity("call", model.Latest(subID)),
			event.NewEnd("p-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "pf1", Source: "p-start", Target: "call"},
			{ID: "pf2", Source: "call", Target: "p-end"},
		},
	}

	driver, err := runtime.NewProcessDriver(runtime.WithDefinitions(custom))
	require.NoError(t, err)
	t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })

	instanceID := fmt.Sprintf("test-custom-def-inst-%d", uniqueDefSeq.Add(1))
	st, runErr := driver.Drive(t.Context(), parent, instanceID, nil)
	require.NoError(t, runErr)
	assert.Equal(t, engine.StatusCompleted, st.Status, "parent must complete when sub-def is in custom registry")
	assert.Greater(t, customCalls.Load(), int64(0), "sub-definition action must have been invoked from custom registry")
}

// ── (d) DEBUG summary: definitions=default-global vs definitions=custom ───

// definitionsSummaryEntry is the shape we need from the JSON summary record.
type definitionsSummaryEntry struct {
	Level       string `json:"level"`
	Msg         string `json:"msg"`
	Definitions string `json:"definitions"`
}

// TestConstructionSummaryDefinitionsField verifies that the DEBUG construction
// summary reports definitions=default-global for a zero-config driver and
// definitions=custom when WithDefinitions(custom) is used.
func TestConstructionSummaryDefinitionsField(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		opts    []runtime.Option
		wantDef string
	}{
		{
			name:    "zero-config shows default-global",
			opts:    nil,
			wantDef: "default-global",
		},
		{
			name:    "WithDefinitions(custom) shows custom",
			opts:    []runtime.Option{runtime.WithDefinitions(kernel.NewMemDefinitionRegistry())},
			wantDef: "custom",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
			logger := slog.New(handler)

			allOpts := append([]runtime.Option{runtime.WithLogger(logger)}, tc.opts...)
			driver, err := runtime.NewProcessDriver(allOpts...)
			require.NoError(t, err)
			t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })

			lines := splitNonEmpty(buf.Bytes())
			require.Len(t, lines, 1, "expected exactly one log record from construction summary")

			var entry definitionsSummaryEntry
			require.NoError(t, json.Unmarshal(lines[0], &entry))
			assert.Equal(t, "DEBUG", entry.Level)
			assert.Equal(t, "ProcessDriver constructed", entry.Msg)
			assert.Equal(t, tc.wantDef, entry.Definitions, "definitions field mismatch")
		})
	}
}

// uniqueDefSeq is a process-global counter used to generate unique definition IDs
// so parallel tests never collide on the shared global registry.
var uniqueDefSeq atomic.Int64
