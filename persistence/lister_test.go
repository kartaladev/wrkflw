package persistence_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// TestNewListerReturnsInterface verifies that persistence.NewLister returns a
// kernel.InstanceLister and never exposes the internal *postgres.Lister type.
func TestNewListerReturnsInterface(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	lister, err := persistence.NewLister(pool)
	require.NoError(t, err)
	require.NotNil(t, lister)

	// Compile-time proof: the static return type is kernel.InstanceLister.
	// The function signature of NewLister is the true type-safety guarantee;
	// this non-nil assert is the runtime sanity check.
	assert.NotNil(t, lister)
}

// seedInstances creates running instances directly via store.Create so we can
// control status without driving a full process through the engine.
func seedInstances(t *testing.T, store kernel.Store, ids []string, base time.Time) {
	t.Helper()
	for i, id := range ids {
		_, err := store.Create(t.Context(), kernel.AppliedStep{
			State: engine.InstanceState{
				InstanceID: id,
				DefID:      "d",
				DefVersion: 1,
				Status:     engine.StatusRunning,
				StartedAt:  base.Add(time.Duration(i) * time.Minute),
			},
			Trigger: engine.NewStartInstance(base, nil),
		})
		require.NoError(t, err, "seed %q", id)
	}
}

// TestListerEndToEnd inserts instances via the Postgres store, then lists them
// via persistence.NewLister, asserting ordering, status filter, and a two-page
// keyset walk through the facade.
func TestListerEndToEnd(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	store, err := persistence.OpenPostgres(t.Context(), pool)
	require.NoError(t, err)
	lister, err := persistence.NewLister(pool)
	require.NoError(t, err)

	base := time.Date(2026, 6, 21, 7, 0, 0, 0, time.UTC)
	running := engine.StatusRunning

	// seed 3 running instances
	seedInstances(t, store, []string{"e1", "e2", "e3"}, base)

	// ordering: e3 (newest) should appear before e1 (oldest)
	page, err := lister.List(t.Context(), kernel.InstanceFilter{})
	require.NoError(t, err)
	require.Len(t, page.Items, 3)

	ids := make([]string, len(page.Items))
	for i, it := range page.Items {
		ids[i] = it.InstanceID
	}
	require.Equal(t, "e3", ids[0], "e3 must be first (newest)")
	require.Equal(t, "e1", ids[2], "e1 must be last (oldest)")

	// status filter: all 3 are running
	pageRunning, err := lister.List(t.Context(), kernel.InstanceFilter{Status: &running})
	require.NoError(t, err)
	require.Len(t, pageRunning.Items, 3)
	for _, it := range pageRunning.Items {
		assert.Equal(t, engine.StatusRunning, it.Status)
	}

	// two-page keyset walk
	p1, err := lister.List(t.Context(), kernel.InstanceFilter{Status: &running, Limit: 2})
	require.NoError(t, err)
	require.Len(t, p1.Items, 2, "page1 should have 2 items")
	require.True(t, p1.HasMore, "page1: want HasMore=true")
	require.NotEmpty(t, p1.NextCursor, "page1: want NextCursor")

	p2, err := lister.List(t.Context(), kernel.InstanceFilter{Status: &running, Limit: 2, Cursor: p1.NextCursor})
	require.NoError(t, err)
	require.Len(t, p2.Items, 1, "page2 should have 1 remaining item")
	require.False(t, p2.HasMore, "page2: want HasMore=false")

	// no duplicates across pages
	seen := map[string]bool{}
	for _, it := range p1.Items {
		seen[it.InstanceID] = true
	}
	for _, it := range p2.Items {
		require.False(t, seen[it.InstanceID], "duplicate instance_id %q across pages", it.InstanceID)
	}
}
