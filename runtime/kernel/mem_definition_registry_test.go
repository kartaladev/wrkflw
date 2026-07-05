package kernel_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

func TestMemDefinitionRegistry_RegisterAndLookup(t *testing.T) {
	t.Parallel()

	reg := kernel.NewMemDefinitionRegistry()
	def := &model.ProcessDefinition{ID: "sub", Version: 2}

	require.NoError(t, reg.Register(def))

	got, err := reg.Lookup(t.Context(), "sub")
	require.NoError(t, err)
	assert.Equal(t, def, got, "Lookup by bare ID should return the registered definition")

	got2, err := reg.Lookup(t.Context(), "sub:2")
	require.NoError(t, err)
	assert.Equal(t, def, got2, "Lookup by <ID>:<Version> should return the registered definition")
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
		"duplicate <ID>:<Version> should return ErrDefinitionExists, got: %v", err)
}

func TestMemDefinitionRegistry_BareIDResolvesLatest(t *testing.T) {
	t.Parallel()

	reg := kernel.NewMemDefinitionRegistry()
	defV1 := &model.ProcessDefinition{ID: "sub", Version: 1}
	defV2 := &model.ProcessDefinition{ID: "sub", Version: 2}

	require.NoError(t, reg.Register(defV1))
	require.NoError(t, reg.Register(defV2))

	// Bare ID should resolve to the most-recently-registered version (v2).
	got, err := reg.Lookup(t.Context(), "sub")
	require.NoError(t, err)
	assert.Equal(t, defV2, got, "bare ID Lookup should return the most-recently-registered version")

	// Versioned key sub:1 must still resolve to v1.
	got1, err := reg.Lookup(t.Context(), "sub:1")
	require.NoError(t, err)
	assert.Equal(t, defV1, got1, "Lookup by <ID>:1 should still return v1")
}

func TestMemDefinitionRegistry_LookupMiss(t *testing.T) {
	t.Parallel()

	reg := kernel.NewMemDefinitionRegistry()

	got, err := reg.Lookup(t.Context(), "nonexistent")
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
			_, _ = reg.Lookup(t.Context(), "concurrent")
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
	}, "MustRegister should panic on duplicate <ID>:<Version>")
}
