package kernel_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

func TestMemDefinitionRegistry_RegisterAndLookup(t *testing.T) {
	t.Parallel()

	reg := kernel.NewMemDefinitionRegistry()
	def := &model.ProcessDefinition{ID: "sub", Version: 2}

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
	def := &model.ProcessDefinition{ID: "sub", Version: 2}

	require.NoError(t, reg.Register(def))

	err := reg.Register(&model.ProcessDefinition{ID: "sub", Version: 2})
	require.Error(t, err)
	assert.True(t, errors.Is(err, kernel.ErrDefinitionExists),
		"duplicate Qualifier should return ErrDefinitionExists, got: %v", err)
}

func TestMemDefinitionRegistry_BareIDResolvesLatest(t *testing.T) {
	t.Parallel()

	reg := kernel.NewMemDefinitionRegistry()
	defV1 := &model.ProcessDefinition{ID: "sub", Version: 1}
	defV2 := &model.ProcessDefinition{ID: "sub", Version: 2}

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
			def := &model.ProcessDefinition{ID: "concurrent", Version: i + 1}
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
	def := &model.ProcessDefinition{ID: "panic-test", Version: 1}
	reg.MustRegister(def)

	// Duplicate registration should panic.
	assert.Panics(t, func() {
		reg.MustRegister(&model.ProcessDefinition{ID: "panic-test", Version: 1})
	}, "MustRegister should panic on duplicate Qualifier")
}

func TestMemDefinitionRegistryLatestIsLastRegistered(t *testing.T) {
	t.Parallel()

	reg := kernel.NewMemDefinitionRegistry()
	v2 := &model.ProcessDefinition{ID: "order", Version: 2}
	v1 := &model.ProcessDefinition{ID: "order", Version: 1}

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
