package postgres_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/database"
	"github.com/zakyalvan/krtlwrkflw/engine"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// newPgStoreWithLister spins up a Postgres container, migrates, and returns both
// a Store (for inserts) and a Lister (for list assertions).
func newPgStoreWithLister(t *testing.T) (*pg.Store, *pg.Lister) {
	t.Helper()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))
	return pg.NewStore(pool), pg.NewLister(pool)
}

// insertInstance creates a new instance via Store.Create and panics on error.
func insertInstance(t *testing.T, s *pg.Store, id string, status engine.Status, at time.Time) {
	t.Helper()
	_, err := s.Create(t.Context(), runtime.AppliedStep{
		State: engine.InstanceState{
			InstanceID: id,
			DefID:      "d",
			DefVersion: 1,
			Status:     status,
			StartedAt:  at,
		},
		Trigger: engine.NewStartInstance(at, nil),
	})
	require.NoError(t, err, "insertInstance %q", id)
}

func TestPgListerOrdering(t *testing.T) {
	t.Parallel()
	s, lister := newPgStoreWithLister(t)
	base := time.Date(2026, 6, 21, 8, 0, 0, 0, time.UTC)

	insertInstance(t, s, "a", engine.StatusRunning, base)
	insertInstance(t, s, "b", engine.StatusRunning, base.Add(time.Minute))
	insertInstance(t, s, "c", engine.StatusRunning, base.Add(2*time.Minute))

	page, err := lister.List(t.Context(), runtime.InstanceFilter{})
	require.NoError(t, err)
	require.Len(t, page.Items, 3, "want 3 items")
	require.Equal(t, "c", page.Items[0].InstanceID, "want c first")
	require.Equal(t, "b", page.Items[1].InstanceID, "want b second")
	require.Equal(t, "a", page.Items[2].InstanceID, "want a third")
	require.False(t, page.HasMore, "want HasMore=false")
}

func TestPgListerStatusFilter(t *testing.T) {
	t.Parallel()
	s, lister := newPgStoreWithLister(t)
	base := time.Date(2026, 6, 21, 8, 0, 0, 0, time.UTC)
	completed := engine.StatusCompleted

	insertInstance(t, s, "r1", engine.StatusRunning, base)
	insertInstance(t, s, "c1", engine.StatusCompleted, base.Add(time.Minute))
	insertInstance(t, s, "c2", engine.StatusCompleted, base.Add(2*time.Minute))

	page, err := lister.List(t.Context(), runtime.InstanceFilter{Status: &completed})
	require.NoError(t, err)
	require.Len(t, page.Items, 2, "want 2 completed")
	for _, it := range page.Items {
		require.Equal(t, engine.StatusCompleted, it.Status)
	}
}

func TestPgListerKeyset_TwoPageWalk(t *testing.T) {
	t.Parallel()
	s, lister := newPgStoreWithLister(t)
	base := time.Date(2026, 6, 21, 8, 0, 0, 0, time.UTC)
	running := engine.StatusRunning

	insertInstance(t, s, "i1", engine.StatusRunning, base)
	insertInstance(t, s, "i2", engine.StatusRunning, base.Add(time.Minute))
	insertInstance(t, s, "i3", engine.StatusRunning, base.Add(2*time.Minute))

	// page 1: limit=2
	p1, err := lister.List(t.Context(), runtime.InstanceFilter{Status: &running, Limit: 2})
	require.NoError(t, err)
	require.Len(t, p1.Items, 2, "page1: want 2 items")
	require.Equal(t, "i3", p1.Items[0].InstanceID, "page1[0] want i3")
	require.Equal(t, "i2", p1.Items[1].InstanceID, "page1[1] want i2")
	require.True(t, p1.HasMore, "page1: want HasMore=true")
	require.NotEmpty(t, p1.NextCursor, "page1: want NextCursor")

	// page 2: use cursor from page 1
	p2, err := lister.List(t.Context(), runtime.InstanceFilter{Status: &running, Limit: 2, Cursor: p1.NextCursor})
	require.NoError(t, err)
	require.Len(t, p2.Items, 1, "page2: want 1 item")
	require.Equal(t, "i1", p2.Items[0].InstanceID, "page2[0] want i1")
	require.False(t, p2.HasMore, "page2: want HasMore=false")
}

func TestPgListerProjectsFields(t *testing.T) {
	t.Parallel()
	s, lister := newPgStoreWithLister(t)
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	_, err := s.Create(t.Context(), runtime.AppliedStep{
		State: engine.InstanceState{
			InstanceID: "proj-1",
			DefID:      "mydef",
			DefVersion: 5,
			Status:     engine.StatusRunning,
			StartedAt:  at,
		},
		Trigger: engine.NewStartInstance(at, nil),
	})
	require.NoError(t, err)

	page, err := lister.List(t.Context(), runtime.InstanceFilter{})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	it := page.Items[0]
	require.Equal(t, "proj-1", it.InstanceID)
	require.Equal(t, "mydef", it.DefID)
	require.Equal(t, 5, it.DefVersion)
	require.Equal(t, engine.StatusRunning, it.Status)
	require.True(t, it.StartedAt.Equal(at), "StartedAt mismatch")
	require.Nil(t, it.EndedAt)
}

func TestPgListerDefaultLimit(t *testing.T) {
	t.Parallel()
	s, lister := newPgStoreWithLister(t)
	base := time.Date(2026, 6, 21, 8, 0, 0, 0, time.UTC)

	// insert 3 items; no limit in filter → default 50, so all 3 returned
	for i := range 3 {
		insertInstance(t, s, "def"+string(rune('a'+i)), engine.StatusRunning, base.Add(time.Duration(i)*time.Minute))
	}

	page, err := lister.List(t.Context(), runtime.InstanceFilter{})
	require.NoError(t, err)
	require.Len(t, page.Items, 3)
	require.False(t, page.HasMore)
}
