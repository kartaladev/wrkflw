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
//   (e) forceTerminationWarnings + registration-time WARN on redundant
//       single-end force-termination (ADR-0119).

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
	"github.com/zakyalvan/krtlwrkflw/definition"
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

// ── (f) message-start name uniqueness at registration (ADR-0121) ──────────

// messageStartDef builds a single-message-start definition registered under a
// unique id (idPrefix + a process-global counter suffix), with its lone start
// event's message-start name set to msgName. It does not register the
// definition — callers pass it to runtime.RegisterDefinition themselves.
func messageStartDef(t *testing.T, idPrefix, msgName string) *model.ProcessDefinition {
	t.Helper()

	id := fmt.Sprintf("%s-%d", idPrefix, uniqueDefSeq.Add(1))
	def, err := definition.NewBuilder(id, 1).
		AddStartEvent("s", event.WithMessageCorrelator(msgName, "")).
		AddEndEvent("e").
		Connect("s", "e").
		Build()
	require.NoError(t, err)
	return def
}

// multiStartMessageDef builds a definition with one message-start event per
// entry in msgNames (same id-uniqueness scheme as messageStartDef), all
// flowing into a single shared end event. Used to exercise the
// intra-definition duplicate-message-start-name case, which structural
// validation does not catch (ADR-0121 permits any number of event-triggered
// starts; only registration-time uniqueness closes this gap).
func multiStartMessageDef(t *testing.T, idPrefix string, msgNames ...string) *model.ProcessDefinition {
	t.Helper()

	id := fmt.Sprintf("%s-%d", idPrefix, uniqueDefSeq.Add(1))
	b := definition.NewBuilder(id, 1)
	for i, name := range msgNames {
		b = b.AddStartEvent(fmt.Sprintf("s%d", i), event.WithMessageCorrelator(name, ""))
	}
	b = b.AddEndEvent("e")
	for i := range msgNames {
		b = b.Connect(fmt.Sprintf("s%d", i), "e")
	}
	def, err := b.Build()
	require.NoError(t, err)
	return def
}

// messageStartDefVersioned builds a message-start definition with an explicit id
// and version, so a test can register two versions of the SAME def id. Its lone
// start event carries msgName as its message-start.
func messageStartDefVersioned(t *testing.T, id, msgName string, version int) *model.ProcessDefinition {
	t.Helper()

	def, err := definition.NewBuilder(id, version).
		AddStartEvent("s", event.WithMessageCorrelator(msgName, "")).
		AddEndEvent("e").
		Connect("s", "e").
		Build()
	require.NoError(t, err)
	return def
}

// TestRegisterDefinitionRejectsDuplicateMessageStart verifies that
// RegisterDefinition rejects a message-start name collision — both across two
// distinct definitions, and within a single definition that declares the same
// message-start name on two different start nodes — while distinct
// message-start names register cleanly. Each case generates its own unique
// message names (via uniqueDefSeq) so it is safe against the shared
// process-global registry and against other tests' message-start names.
func TestRegisterDefinitionRejectsDuplicateMessageStart(t *testing.T) {
	type testCase struct {
		name   string
		defs   func(t *testing.T) []*model.ProcessDefinition
		assert func(t *testing.T, errs []error)
	}

	cases := []testCase{
		{
			name: "cross-definition duplicate message-start name is rejected",
			defs: func(t *testing.T) []*model.ProcessDefinition {
				msg := fmt.Sprintf("order.created.%d", uniqueDefSeq.Add(1))
				return []*model.ProcessDefinition{
					messageStartDef(t, "cross-a", msg),
					messageStartDef(t, "cross-b", msg),
				}
			},
			assert: func(t *testing.T, errs []error) {
				require.Len(t, errs, 2)
				require.NoError(t, errs[0])
				assert.ErrorIs(t, errs[1], runtime.ErrDuplicateMessageStart)
			},
		},
		{
			name: "intra-definition duplicate message-start name is rejected",
			defs: func(t *testing.T) []*model.ProcessDefinition {
				msg := fmt.Sprintf("order.dup.%d", uniqueDefSeq.Add(1))
				return []*model.ProcessDefinition{
					multiStartMessageDef(t, "intra", msg, msg),
				}
			},
			assert: func(t *testing.T, errs []error) {
				require.Len(t, errs, 1)
				assert.ErrorIs(t, errs[0], runtime.ErrDuplicateMessageStart)
			},
		},
		{
			name: "superseded version's message name does not block a new def reusing it",
			defs: func(t *testing.T) []*model.ProcessDefinition {
				seq := uniqueDefSeq.Add(1)
				oldName := fmt.Sprintf("order.super.old.%d", seq)
				newName := fmt.Sprintf("order.super.new.%d", seq)
				sharedID := fmt.Sprintf("super-a-%d", seq)
				return []*model.ProcessDefinition{
					// A v1 claims oldName, then A v2 renames its start to newName —
					// v2 supersedes v1's start subscription (ADR-0121 Camunda semantics).
					messageStartDefVersioned(t, sharedID, oldName, 1),
					messageStartDefVersioned(t, sharedID, newName, 2),
					// A distinct def B reuses oldName: since only the LATEST version of
					// A (v2, newName) holds an active start subscription, oldName is free.
					messageStartDefVersioned(t, fmt.Sprintf("super-b-%d", seq), oldName, 1),
				}
			},
			assert: func(t *testing.T, errs []error) {
				require.Len(t, errs, 3)
				assert.NoError(t, errs[0], "A v1 registers")
				assert.NoError(t, errs[1], "A v2 registers (renamed start)")
				assert.NoError(t, errs[2], "B may reuse the superseded v1 name")
			},
		},
		{
			name: "distinct message-start names register fine",
			defs: func(t *testing.T) []*model.ProcessDefinition {
				seq := uniqueDefSeq.Add(1)
				return []*model.ProcessDefinition{
					messageStartDef(t, "distinct-a", fmt.Sprintf("order.a.%d", seq)),
					messageStartDef(t, "distinct-b", fmt.Sprintf("order.b.%d", seq)),
				}
			},
			assert: func(t *testing.T, errs []error) {
				require.Len(t, errs, 2)
				assert.NoError(t, errs[0])
				assert.NoError(t, errs[1])
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			defs := tc.defs(t)
			errs := make([]error, len(defs))
			for i, def := range defs {
				errs[i] = runtime.RegisterDefinition(def)
			}
			tc.assert(t, errs)
		})
	}
}

// ── (e) forceTerminationWarnings + registration-time WARN (ADR-0119) ───────

// TestForceTerminationWarnings verifies the pure forceTerminationWarnings
// helper: a force-termination end event only warrants a WARN when it is the
// *only* end event in the definition (redundant — there is no other branch to
// cancel). Multi-end definitions and definitions with no force-termination end
// produce no warnings.
func TestForceTerminationWarnings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		build  func() *model.ProcessDefinition
		assert func(t *testing.T, warns []string)
	}{
		{
			name: "single-end force-termination is redundant",
			build: func() *model.ProcessDefinition {
				def, err := definition.NewBuilder("single", 1).
					AddStartEvent("s").
					AddEndEvent("e", event.WithForceTermination("x", event.OutcomeAbort)).
					Connect("s", "e").
					Build()
				require.NoError(t, err)
				return def
			},
			assert: func(t *testing.T, warns []string) {
				require.Len(t, warns, 1)
			},
		},
		{
			name: "multi-end force-termination is meaningful",
			build: func() *model.ProcessDefinition {
				def, err := definition.NewBuilder("multi", 1).
					AddStartEvent("s").
					AddParallelGateway("fork").
					AddUserTask("a").
					AddEndEvent("ea").
					AddEndEvent("halt", event.WithForceTermination("x", event.OutcomeAbort)).
					Connect("s", "fork").Connect("fork", "a").Connect("a", "ea").Connect("fork", "halt").
					Build()
				require.NoError(t, err)
				return def
			},
			assert: func(t *testing.T, warns []string) {
				require.Empty(t, warns)
			},
		},
		{
			name: "no force-termination, no warnings",
			build: func() *model.ProcessDefinition {
				def, err := definition.NewBuilder("plain", 1).
					AddStartEvent("s").AddEndEvent("e").Connect("s", "e").Build()
				require.NoError(t, err)
				return def
			},
			assert: func(t *testing.T, warns []string) {
				require.Empty(t, warns)
			},
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.assert(t, runtime.ExportForceTerminationWarnings(c.build()))
		})
	}
}

// TestRegisterDefinitionWarnsOnRedundantForceTermination verifies that
// RegisterDefinition logs a WARN via slog.Default() after a successful
// registration of a single-end force-termination definition. This test
// installs a capturing slog default logger, so it must not run in parallel
// with other tests that also mutate the process-wide default logger.
func TestRegisterDefinitionWarnsOnRedundantForceTermination(t *testing.T) {
	prevLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	id := fmt.Sprintf("force-term-warn-%d", uniqueDefSeq.Add(1))
	def, err := definition.NewBuilder(id, 1).
		AddStartEvent("s").
		AddEndEvent("e", event.WithForceTermination("x", event.OutcomeAbort)).
		Connect("s", "e").
		Build()
	require.NoError(t, err)

	require.NoError(t, runtime.RegisterDefinition(def))

	lines := splitNonEmpty(buf.Bytes())
	var sawWarn bool
	for _, line := range lines {
		var entry struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
		}
		require.NoError(t, json.Unmarshal(line, &entry))
		if entry.Level == "WARN" {
			sawWarn = true
			assert.Contains(t, entry.Msg, id)
			assert.Contains(t, entry.Msg, "forces termination but is the only end event")
		}
	}
	assert.True(t, sawWarn, "expected a WARN log record from RegisterDefinition")
}
