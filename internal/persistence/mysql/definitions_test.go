package mysql_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	mypkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// makeTestDefinition returns a minimal ProcessDefinition for round-trip testing.
func makeTestDefinition(id string, version int) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      id,
		Version: version,
	}
}

// TestDefinitionStore_PutLookupGet verifies the full Put → Lookup (exact) →
// Lookup (latest) → GetDefinition round-trip against a real MySQL database.
func TestDefinitionStore_PutLookupGet(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)
	store := mypkg.NewDefinitionStore(db)
	ctx := t.Context()

	def := makeTestDefinition("proc-a", 1)
	require.NoError(t, store.PutDefinition(ctx, def))

	// Lookup exact "defID:version".
	got, err := store.Lookup(ctx, "proc-a:1")
	require.NoError(t, err)
	require.Equal(t, def.ID, got.ID)
	require.Equal(t, def.Version, got.Version)

	// Lookup latest by "defID" (no colon).
	got2, err := store.Lookup(ctx, "proc-a")
	require.NoError(t, err)
	require.Equal(t, def.ID, got2.ID)
	require.Equal(t, def.Version, got2.Version)

	// GetDefinition by (defID, version).
	got3, err := store.GetDefinition(ctx, "proc-a", 1)
	require.NoError(t, err)
	require.Equal(t, def.ID, got3.ID)
	require.Equal(t, def.Version, got3.Version)
}

// TestDefinitionStore_LatestVersionResolution verifies that Lookup("defID") returns
// the definition with the highest version when multiple versions exist.
func TestDefinitionStore_LatestVersionResolution(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)
	store := mypkg.NewDefinitionStore(db)
	ctx := t.Context()

	def1 := makeTestDefinition("proc-b", 1)
	def2 := makeTestDefinition("proc-b", 2)
	require.NoError(t, store.PutDefinition(ctx, def1))
	require.NoError(t, store.PutDefinition(ctx, def2))

	got, err := store.Lookup(ctx, "proc-b")
	require.NoError(t, err)
	require.Equal(t, 2, got.Version, "Lookup should return the highest version")
}

// TestDefinitionStore_UpsertOverwrite verifies that putting the same (def_id, version)
// twice with different content results in the second content being stored.
// We distinguish the two puts by different CancelActions slices.
func TestDefinitionStore_UpsertOverwrite(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)
	store := mypkg.NewDefinitionStore(db)
	ctx := t.Context()

	first := &model.ProcessDefinition{ID: "proc-c", Version: 1, CancelActions: []string{"action-first"}}
	second := &model.ProcessDefinition{ID: "proc-c", Version: 1, CancelActions: []string{"action-second"}}

	require.NoError(t, store.PutDefinition(ctx, first))
	require.NoError(t, store.PutDefinition(ctx, second))

	got, err := store.GetDefinition(ctx, "proc-c", 1)
	require.NoError(t, err)
	require.Equal(t, []string{"action-second"}, got.CancelActions,
		"upsert must overwrite with the second value")
}

// TestDefinitionStore_LookupNotFound verifies that Lookup returns ErrDefinitionNotFound
// when no matching definition exists (both exact and latest forms).
func TestDefinitionStore_LookupNotFound(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)
	store := mypkg.NewDefinitionStore(db)
	ctx := t.Context()

	// Exact ref not found.
	_, err := store.Lookup(ctx, "no-such-def:1")
	require.Error(t, err)
	require.True(t, errors.Is(err, runtime.ErrDefinitionNotFound),
		"Lookup by exact ref must wrap ErrDefinitionNotFound; got %v", err)

	// Latest ref not found.
	_, err = store.Lookup(ctx, "no-such-def")
	require.Error(t, err)
	require.True(t, errors.Is(err, runtime.ErrDefinitionNotFound),
		"Lookup by latest ref must wrap ErrDefinitionNotFound; got %v", err)
}

// TestDefinitionStore_GetDefinitionNotFound verifies that GetDefinition returns
// ErrDefinitionNotFound when no row matches (defID, version).
func TestDefinitionStore_GetDefinitionNotFound(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)
	store := mypkg.NewDefinitionStore(db)
	ctx := t.Context()

	_, err := store.GetDefinition(ctx, "no-such-def", 99)
	require.Error(t, err)
	require.True(t, errors.Is(err, runtime.ErrDefinitionNotFound),
		"GetDefinition must wrap ErrDefinitionNotFound; got %v", err)
}

// TestDefinitionStore_LookupBadVersion verifies that Lookup returns an error
// when the version segment of a "defID:version" ref cannot be parsed as an int.
func TestDefinitionStore_LookupBadVersion(t *testing.T) {
	t.Parallel()
	db := database.RunTestMySQL(t)
	store := mypkg.NewDefinitionStore(db)
	ctx := t.Context()

	_, err := store.Lookup(ctx, "proc-x:notanumber")
	require.Error(t, err, "Lookup with bad version segment must return an error")
}
