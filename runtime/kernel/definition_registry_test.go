package kernel_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

func TestMapDefinitionRegistryLookup(t *testing.T) {
	def := &definition.ProcessDefinition{ID: "my-def", Version: 1}

	tests := []struct {
		name      string
		defs      map[string]*definition.ProcessDefinition
		lookupKey string
		wantDef   *definition.ProcessDefinition
		wantErr   error
	}{
		{
			name:      "found",
			defs:      map[string]*definition.ProcessDefinition{"my-def": def},
			lookupKey: "my-def",
			wantDef:   def,
		},
		{
			name:      "not found returns ErrDefinitionNotFound",
			defs:      map[string]*definition.ProcessDefinition{"my-def": def},
			lookupKey: "missing",
			wantErr:   kernel.ErrDefinitionNotFound,
		},
		{
			name:      "nil entry skipped — lookup returns not found",
			defs:      map[string]*definition.ProcessDefinition{"nil-def": nil},
			lookupKey: "nil-def",
			wantErr:   kernel.ErrDefinitionNotFound,
		},
		{
			name:      "empty registry returns not found",
			defs:      map[string]*definition.ProcessDefinition{},
			lookupKey: "anything",
			wantErr:   kernel.ErrDefinitionNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := kernel.NewMapDefinitionRegistry(tc.defs)
			got, err := reg.Lookup(t.Context(), tc.lookupKey)
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

func TestMapDefinitionRegistryIsolatedFromMutation(t *testing.T) {
	def := &definition.ProcessDefinition{ID: "isolated", Version: 1}
	input := map[string]*definition.ProcessDefinition{"key": def}
	reg := kernel.NewMapDefinitionRegistry(input)

	// Mutate the original map after construction.
	delete(input, "key")
	input["other"] = &definition.ProcessDefinition{ID: "other"}

	// Registry must be unaffected by post-construction mutation.
	got, err := reg.Lookup(t.Context(), "key")
	require.NoError(t, err)
	assert.Equal(t, def, got)

	_, err = reg.Lookup(t.Context(), "other")
	assert.True(t, errors.Is(err, kernel.ErrDefinitionNotFound))
}
