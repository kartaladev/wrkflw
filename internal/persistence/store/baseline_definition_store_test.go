package store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/persistence/store"
)

// TestBaselineScopedRepublishReadsBackUnchanged characterizes the
// definition-store scenario "Republishing identical content is a no-op on every
// backend", rows "a built definition carrying scoped actions"
// (openspec/specs/definition-store/spec.md): after an identical republish,
// reading the version back returns the published definition unchanged.
//
// "Unchanged" is compared over the definition's persisted content — ID,
// Version, Nodes, Flows, CancelActions. The definition-scoped action catalog is
// never serialized (model.ProcessDefinition.ScopedCatalog), so a definition read
// from any store carries none; that is the documented shape, not a change.
func TestBaselineScopedRepublishReadsBackUnchanged(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ds, err := store.NewDefinitionStore(b.conn, b.dialect)
		require.NoError(t, err)

		const id = "baseline-scoped-readback"
		def := scopedActionDefinition(t, id, 1)
		require.NoError(t, ds.PublishDefinition(t.Context(), def), "%s: first publish", b.name)
		require.NoError(t, ds.PublishDefinition(t.Context(), scopedActionDefinition(t, id, 1)),
			"%s: identical republish must return nil", b.name)
		require.Equal(t, 1, countDefinitionRows(t, b, id), "%s: exactly one row", b.name)

		got, err := ds.GetDefinition(t.Context(), id, 1)
		require.NoError(t, err, "%s: read back", b.name)
		assert.Equal(t, def.ID, got.ID, "%s: ID", b.name)
		assert.Equal(t, def.Version, got.Version, "%s: Version", b.name)
		assert.Equal(t, def.Nodes, got.Nodes, "%s: Nodes", b.name)
		assert.Equal(t, def.Flows, got.Flows, "%s: Flows", b.name)
		assert.Equal(t, def.CancelActions, got.CancelActions, "%s: CancelActions", b.name)
	})
}
