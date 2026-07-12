package kernel_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

func TestMemDefinitionRegistryListsDistinct(t *testing.T) {
	t.Parallel()

	reg := kernel.NewMemDefinitionRegistry()
	require.NoError(t, reg.Register(&model.ProcessDefinition{ID: "A", Version: 1}))
	require.NoError(t, reg.Register(&model.ProcessDefinition{ID: "B", Version: 1}))

	var lister kernel.DefinitionLister = reg
	got := lister.ListDefinitions(t.Context())

	ids := make([]string, 0, len(got))
	for _, d := range got {
		ids = append(ids, d.ID)
	}
	assert.ElementsMatch(t, []string{"A", "B"}, ids, "each concrete def must be listed once, not once per index key")
}

func TestMapDefinitionRegistryListsDistinct(t *testing.T) {
	t.Parallel()

	reg := kernel.NewMapDefinitionRegistry(
		&model.ProcessDefinition{ID: "A", Version: 1},
		&model.ProcessDefinition{ID: "B", Version: 1},
	)

	var lister kernel.DefinitionLister = reg
	got := lister.ListDefinitions(t.Context())

	ids := make([]string, 0, len(got))
	for _, d := range got {
		ids = append(ids, d.ID)
	}
	assert.ElementsMatch(t, []string{"A", "B"}, ids, "each concrete def must be listed once, not once per index key")
}

// TestCachingDefinitionRegistryListDefinitions covers the pass-through capability
// check: when the backing registry implements DefinitionLister, ListDefinitions
// delegates and returns the backing's deduplicated list; when it does not, the
// caching registry returns nil rather than trying to enumerate its partial,
// per-qualifier TTL cache.
func TestCachingDefinitionRegistryListDefinitions(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		backing kernel.DefinitionRegistry
		assert  func(t *testing.T, got []*model.ProcessDefinition)
	}

	cases := []testCase{
		{
			name: "backing implements DefinitionLister: pass-through, deduped",
			backing: kernel.NewMapDefinitionRegistry(
				&model.ProcessDefinition{ID: "A", Version: 1},
				&model.ProcessDefinition{ID: "B", Version: 1},
			),
			assert: func(t *testing.T, got []*model.ProcessDefinition) {
				ids := make([]string, 0, len(got))
				for _, d := range got {
					ids = append(ids, d.ID)
				}
				assert.ElementsMatch(t, []string{"A", "B"}, ids)
			},
		},
		{
			name:    "backing does not implement DefinitionLister: nil",
			backing: &countingRegistry{def: &model.ProcessDefinition{ID: "d", Version: 1}},
			assert: func(t *testing.T, got []*model.ProcessDefinition) {
				assert.Nil(t, got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := mustCachingDefinitionRegistry(t, tc.backing, time.Minute)

			var lister kernel.DefinitionLister = c
			got := lister.ListDefinitions(t.Context())
			tc.assert(t, got)
		})
	}
}
