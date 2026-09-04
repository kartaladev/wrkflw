package kernel_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

func TestMemDefinitionRegistry_RegisterAndLookup(t *testing.T) {
	t.Parallel()

	reg := kernel.NewMemDefinitionRegistry()
	def := minimalValidDef("sub", 2)

	require.NoError(t, reg.Register(def))

	got, err := reg.Lookup(t.Context(), model.Latest("sub"))
	require.NoError(t, err)
	assert.Equal(t, def, got, "Lookup by Latest should return the registered definition")

	got2, err := reg.Lookup(t.Context(), model.Version("sub", 2))
	require.NoError(t, err)
	assert.Equal(t, def, got2, "Lookup by Version should return the registered definition")
}

func TestMemDefinitionRegistry_NilDef(t *testing.T) {
	t.Parallel()

	reg := kernel.NewMemDefinitionRegistry()
	err := reg.Register(nil)

	require.Error(t, err)
	assert.True(t, errors.Is(err, kernel.ErrNilDefinition), "nil def should return ErrNilDefinition, got: %v", err)
}

func TestMemDefinitionRegistry_EmptyID(t *testing.T) {
	t.Parallel()

	reg := kernel.NewMemDefinitionRegistry()
	err := reg.Register(&model.ProcessDefinition{ID: "", Version: 1})

	require.Error(t, err)
	assert.True(t, errors.Is(err, kernel.ErrEmptyDefinitionID), "empty ID should return ErrEmptyDefinitionID, got: %v", err)
}

func TestMemDefinitionRegistry_DuplicateVersionedKey(t *testing.T) {
	t.Parallel()

	reg := kernel.NewMemDefinitionRegistry()
	def := minimalValidDef("sub", 2)

	require.NoError(t, reg.Register(def))

	err := reg.Register(minimalValidDef("sub", 2))
	require.Error(t, err)
	assert.True(t, errors.Is(err, kernel.ErrDefinitionExists),
		"duplicate Qualifier should return ErrDefinitionExists, got: %v", err)
}

func TestMemDefinitionRegistry_BareIDResolvesLatest(t *testing.T) {
	t.Parallel()

	reg := kernel.NewMemDefinitionRegistry()
	defV1 := minimalValidDef("sub", 1)
	defV2 := minimalValidDef("sub", 2)

	require.NoError(t, reg.Register(defV1))
	require.NoError(t, reg.Register(defV2))

	// Latest Qualifier should resolve to the most-recently-registered version (v2).
	got, err := reg.Lookup(t.Context(), model.Latest("sub"))
	require.NoError(t, err)
	assert.Equal(t, defV2, got, "Latest Qualifier should return the most-recently-registered version")

	// Pinned Version(sub,1) must still resolve to v1.
	got1, err := reg.Lookup(t.Context(), model.Version("sub", 1))
	require.NoError(t, err)
	assert.Equal(t, defV1, got1, "Lookup by Version(sub,1) should still return v1")
}

func TestMemDefinitionRegistry_LookupMiss(t *testing.T) {
	t.Parallel()

	reg := kernel.NewMemDefinitionRegistry()

	got, err := reg.Lookup(t.Context(), model.Latest("nonexistent"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, kernel.ErrDefinitionNotFound),
		"Lookup miss should return ErrDefinitionNotFound, got: %v", err)
	assert.Nil(t, got)
}

func TestMemDefinitionRegistry_Concurrent(t *testing.T) {
	t.Parallel()

	reg := kernel.NewMemDefinitionRegistry()
	const numWorkers = 20

	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for i := range numWorkers {
		go func(i int) {
			defer wg.Done()
			// Each goroutine registers a unique definition by version.
			def := minimalValidDef("concurrent", i+1)
			_ = reg.Register(def)
		}(i)
	}

	// Concurrent lookups alongside registrations.
	wg.Add(numWorkers)
	for range numWorkers {
		go func() {
			defer wg.Done()
			_, _ = reg.Lookup(t.Context(), model.Latest("concurrent"))
		}()
	}

	wg.Wait()
}

func TestMemDefinitionRegistry_MustRegisterPanicsOnError(t *testing.T) {
	t.Parallel()

	reg := kernel.NewMemDefinitionRegistry()
	def := minimalValidDef("panic-test", 1)
	reg.MustRegister(def)

	// Duplicate registration should panic.
	assert.Panics(t, func() {
		reg.MustRegister(minimalValidDef("panic-test", 1))
	}, "MustRegister should panic on duplicate Qualifier")
}

func TestMemDefinitionRegistryLatestIsLastRegistered(t *testing.T) {
	t.Parallel()

	reg := kernel.NewMemDefinitionRegistry()
	v2 := minimalValidDef("order", 2)
	v1 := minimalValidDef("order", 1)

	// Register the HIGHER version first, then the lower one.
	require.NoError(t, reg.Register(v2))
	require.NoError(t, reg.Register(v1))

	// Latest resolves to the LAST-registered def (v1), not the highest version.
	// This is intentional and differs from MapDefinitionRegistry behavior.
	got, err := reg.Lookup(t.Context(), model.Latest("order"))
	require.NoError(t, err)
	assert.Equal(t, v1, got, "Latest should resolve to the last-registered definition (v1), not the highest version (v2)")

	// Pinned lookups still resolve each exact version.
	p2, err := reg.Lookup(t.Context(), model.Version("order", 2))
	require.NoError(t, err)
	assert.Equal(t, v2, p2, "Pinned Version(order,2) should still resolve to v2")

	p1, err := reg.Lookup(t.Context(), model.Version("order", 1))
	require.NoError(t, err)
	assert.Equal(t, v1, p1, "Pinned Version(order,1) should still resolve to v1")
}

// ── Authoring gate ───────────────────────────────────────────────────────

// minimalValidDef returns the smallest definition that passes model.Validate:
// one manual start wired straight to one end event.
func minimalValidDef(id string, version int) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      id,
		Version: version,
		Nodes:   []model.Node{event.NewStart("s"), event.NewEnd("e")},
		Flows:   []flow.SequenceFlow{{ID: "f1", Source: "s", Target: "e"}},
	}
}

// TestMemDefinitionRegistry_ValidatesOnRegister pins the authoring gate to the
// registration surface. engine.Step documents that it assumes the definition has
// passed model.Validate; the builder and the YAML loader both end in that call,
// but Register is the one door a hand-built *model.ProcessDefinition literal can
// walk through, and it used to be unguarded. A definition that fails
// model.Validate must be rejected, and must be indexed under neither the pinned
// nor the latest key.
func TestMemDefinitionRegistry_ValidatesOnRegister(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, reg *kernel.MemDefinitionRegistry, err error)
	}

	// rejected asserts the shape shared by every invalid case: the error names
	// both the registry-level sentinel and the specific model rule that was
	// broken, and neither index key resolves afterwards.
	rejected := func(rule error) func(*testing.T, *kernel.MemDefinitionRegistry, error) {
		return func(t *testing.T, reg *kernel.MemDefinitionRegistry, err error) {
			require.Error(t, err)
			assert.ErrorIs(t, err, kernel.ErrInvalidDefinition, "must be identifiable as a validation rejection")
			assert.ErrorIs(t, err, rule, "must carry the model.Validate rule it broke")

			_, latestErr := reg.Lookup(t.Context(), model.Latest("gated"))
			assert.ErrorIs(t, latestErr, kernel.ErrDefinitionNotFound, "a rejected definition must not claim the latest key")
			_, pinnedErr := reg.Lookup(t.Context(), model.Version("gated", 1))
			assert.ErrorIs(t, pinnedErr, kernel.ErrDefinitionNotFound, "a rejected definition must not claim the pinned key")
		}
	}

	catchWithoutTrigger := minimalValidDef("gated", 1)
	catchWithoutTrigger.Nodes = []model.Node{
		event.NewStart("s"), event.NewIntermediateCatch("c"), event.NewEnd("e"),
	}
	catchWithoutTrigger.Flows = []flow.SequenceFlow{
		{ID: "f1", Source: "s", Target: "c"},
		{ID: "f2", Source: "c", Target: "e"},
	}

	danglingFlow := minimalValidDef("gated", 1)
	danglingFlow.Flows = append(danglingFlow.Flows, flow.SequenceFlow{ID: "f2", Source: "e", Target: "ghost"})

	cases := []testCase{
		{
			name: "valid definition is registered under both keys",
			def:  minimalValidDef("gated", 1),
			assert: func(t *testing.T, reg *kernel.MemDefinitionRegistry, err error) {
				require.NoError(t, err)

				_, latestErr := reg.Lookup(t.Context(), model.Latest("gated"))
				assert.NoError(t, latestErr, "a valid definition must claim the latest key")
				_, pinnedErr := reg.Lookup(t.Context(), model.Version("gated", 1))
				assert.NoError(t, pinnedErr, "a valid definition must claim the pinned key")
			},
		},
		{
			name:   "no start event",
			def:    &model.ProcessDefinition{ID: "gated", Version: 1},
			assert: rejected(model.ErrNoStartEvent),
		},
		{
			name:   "version 0 collides with the latest sentinel",
			def:    minimalValidDef("gated", 0),
			assert: rejected(model.ErrInvalidVersion),
		},
		{
			name:   "flow references unknown node",
			def:    danglingFlow,
			assert: rejected(model.ErrDanglingFlow),
		},
		{
			// The defect #38 had to guard at runtime precisely because this
			// literal could reach the engine unvalidated.
			name:   "intermediate catch event declares no trigger",
			def:    catchWithoutTrigger,
			assert: rejected(model.ErrCatchEventMissingTrigger),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reg := kernel.NewMemDefinitionRegistry()

			err := reg.Register(tc.def)
			tc.assert(t, reg, err)
		})
	}
}

// TestMemDefinitionRegistry_MustRegisterPanicsOnInvalidDefinition asserts the
// panicking variant inherits the gate — init-time wiring must not smuggle an
// unvalidated literal into a registry.
func TestMemDefinitionRegistry_MustRegisterPanicsOnInvalidDefinition(t *testing.T) {
	t.Parallel()

	reg := kernel.NewMemDefinitionRegistry()

	assert.Panics(t, func() { reg.MustRegister(&model.ProcessDefinition{ID: "gated", Version: 1}) })
}
