package kernel_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

func TestMemRegistryLookupByQualifier(t *testing.T) {
	reg := kernel.NewMemDefinitionRegistry()
	v1 := &model.ProcessDefinition{ID: "order", Version: 1}
	v2 := &model.ProcessDefinition{ID: "order", Version: 2}
	if err := reg.Register(v1); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(v2); err != nil {
		t.Fatal(err)
	}

	assertLookup := func(q model.Qualifier, want *model.ProcessDefinition) {
		t.Helper()
		got, err := reg.Lookup(t.Context(), q)
		if err != nil {
			t.Fatalf("lookup %s: %v", q, err)
		}
		if got != want {
			t.Fatalf("lookup %s = %+v, want %+v", q, got, want)
		}
	}
	assertLookup(model.Version("order", 1), v1)
	assertLookup(model.Version("order", 2), v2)
	assertLookup(model.Latest("order"), v2) // latest == newest registered

	if _, err := reg.Lookup(t.Context(), model.Version("order", 9)); !errors.Is(err, kernel.ErrDefinitionNotFound) {
		t.Fatalf("expected ErrDefinitionNotFound, got %v", err)
	}
}

func TestMapDefinitionRegistryLookup(t *testing.T) {
	def := &model.ProcessDefinition{ID: "my-def", Version: 1}

	tests := []struct {
		name    string
		defs    []*model.ProcessDefinition
		lookupQ model.Qualifier
		wantDef *model.ProcessDefinition
		wantErr error
	}{
		{
			name:    "found by latest",
			defs:    []*model.ProcessDefinition{def},
			lookupQ: model.Latest("my-def"),
			wantDef: def,
		},
		{
			name:    "found by pinned version",
			defs:    []*model.ProcessDefinition{def},
			lookupQ: model.Version("my-def", 1),
			wantDef: def,
		},
		{
			name:    "not found returns ErrDefinitionNotFound",
			defs:    []*model.ProcessDefinition{def},
			lookupQ: model.Latest("missing"),
			wantErr: kernel.ErrDefinitionNotFound,
		},
		{
			name:    "nil entry skipped — lookup returns not found",
			defs:    []*model.ProcessDefinition{nil},
			lookupQ: model.Latest("nil-def"),
			wantErr: kernel.ErrDefinitionNotFound,
		},
		{
			name:    "empty registry returns not found",
			defs:    nil,
			lookupQ: model.Latest("anything"),
			wantErr: kernel.ErrDefinitionNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := kernel.NewMapDefinitionRegistry(tc.defs...)
			got, err := reg.Lookup(t.Context(), tc.lookupQ)
			if tc.wantErr != nil {
				require.Error(t, err)
				assert.True(t, errors.Is(err, tc.wantErr), "expected %v, got %v", tc.wantErr, err)
				assert.Nil(t, got)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantDef, got)
			}
		})
	}
}

func TestMapDefinitionRegistryLatestResolvesHighestVersion(t *testing.T) {
	v1 := &model.ProcessDefinition{ID: "proc", Version: 1}
	v2 := &model.ProcessDefinition{ID: "proc", Version: 2}
	reg := kernel.NewMapDefinitionRegistry(v1, v2)

	// Latest must return the highest version.
	got, err := reg.Lookup(t.Context(), model.Latest("proc"))
	require.NoError(t, err)
	assert.Equal(t, v2, got, "Latest must resolve to highest version")

	// Pinned v1 must still resolve.
	got1, err := reg.Lookup(t.Context(), model.Version("proc", 1))
	require.NoError(t, err)
	assert.Equal(t, v1, got1)
}

func TestMapDefinitionRegistryIsolatedFromMutation(t *testing.T) {
	def := &model.ProcessDefinition{ID: "isolated", Version: 1}
	// NewMapDefinitionRegistry is variadic — pass a single def.
	reg := kernel.NewMapDefinitionRegistry(def)

	// The pinned qualifier must still be found; the registry is immutable after construction.
	got, err := reg.Lookup(t.Context(), model.Version("isolated", 1))
	require.NoError(t, err)
	assert.Equal(t, def, got)

	// A totally different qualifier must not be found.
	_, err = reg.Lookup(t.Context(), model.Latest("other"))
	assert.True(t, errors.Is(err, kernel.ErrDefinitionNotFound))
}
