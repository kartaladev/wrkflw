package mysql_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	mypkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// newMySQLLister returns a freshly migrated Lister + Store backed by a MySQL
// testcontainer database (auto-migrated via database.RunTestMySQL).
func newMySQLLister(t *testing.T) (*mypkg.Lister, *mypkg.Store) {
	t.Helper()
	db := database.RunTestMySQL(t)
	return mypkg.NewLister(db), mypkg.NewStore(db)
}

// insertMySQLInstance creates a new instance via Store.Create.
func insertMySQLInstance(t *testing.T, s *mypkg.Store, id string, status engine.Status, at time.Time) {
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
	require.NoError(t, err, "insertMySQLInstance %q", id)
}

// insertMySQLInstanceWithIncidents creates a new instance with incident data
// embedded in the snapshot.
func insertMySQLInstanceWithIncidents(t *testing.T, s *mypkg.Store, id string, at time.Time, incidents []engine.Incident) {
	t.Helper()
	_, err := s.Create(t.Context(), runtime.AppliedStep{
		State: engine.InstanceState{
			InstanceID: id,
			DefID:      "d",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  at,
			Incidents:  incidents,
		},
		Trigger: engine.NewStartInstance(at, nil),
	})
	require.NoError(t, err, "insertMySQLInstanceWithIncidents %q", id)
}

// TestLister_KeysetPagination seeds N instances, pages through them deterministically,
// and asserts ordering and cursor behaviour mirror the postgres implementation.
func TestLister_KeysetPagination(t *testing.T) {
	t.Parallel()

	t.Run("ordering newest first", func(t *testing.T) {
		t.Parallel()
		lister, store := newMySQLLister(t)
		base := time.Date(2026, 6, 28, 8, 0, 0, 0, time.UTC)

		insertMySQLInstance(t, store, "a", engine.StatusRunning, base)
		insertMySQLInstance(t, store, "b", engine.StatusRunning, base.Add(time.Minute))
		insertMySQLInstance(t, store, "c", engine.StatusRunning, base.Add(2*time.Minute))

		page, err := lister.List(t.Context(), runtime.InstanceFilter{})
		require.NoError(t, err)
		require.Len(t, page.Items, 3)
		assert.Equal(t, "c", page.Items[0].InstanceID)
		assert.Equal(t, "b", page.Items[1].InstanceID)
		assert.Equal(t, "a", page.Items[2].InstanceID)
		assert.False(t, page.HasMore)
		assert.Empty(t, page.NextCursor)
	})

	t.Run("two-page keyset walk", func(t *testing.T) {
		t.Parallel()
		lister, store := newMySQLLister(t)
		base := time.Date(2026, 6, 28, 8, 0, 0, 0, time.UTC)
		running := engine.StatusRunning

		insertMySQLInstance(t, store, "i1", engine.StatusRunning, base)
		insertMySQLInstance(t, store, "i2", engine.StatusRunning, base.Add(time.Minute))
		insertMySQLInstance(t, store, "i3", engine.StatusRunning, base.Add(2*time.Minute))

		// Page 1: limit=2
		p1, err := lister.List(t.Context(), runtime.InstanceFilter{Status: &running, Limit: 2})
		require.NoError(t, err)
		require.Len(t, p1.Items, 2)
		assert.Equal(t, "i3", p1.Items[0].InstanceID)
		assert.Equal(t, "i2", p1.Items[1].InstanceID)
		assert.True(t, p1.HasMore)
		assert.NotEmpty(t, p1.NextCursor)

		// Page 2: use cursor from page 1
		p2, err := lister.List(t.Context(), runtime.InstanceFilter{Status: &running, Limit: 2, Cursor: p1.NextCursor})
		require.NoError(t, err)
		require.Len(t, p2.Items, 1)
		assert.Equal(t, "i1", p2.Items[0].InstanceID)
		assert.False(t, p2.HasMore)
		assert.Empty(t, p2.NextCursor)
	})

	t.Run("tie boundary same started_at distinct ids", func(t *testing.T) {
		t.Parallel()
		lister, store := newMySQLLister(t)
		// Use a fixed time so all 4 instances share the SAME started_at timestamp
		tie := time.Date(2026, 6, 28, 9, 0, 0, 0, time.UTC)

		insertMySQLInstance(t, store, "tie-a", engine.StatusRunning, tie)
		insertMySQLInstance(t, store, "tie-b", engine.StatusRunning, tie)
		insertMySQLInstance(t, store, "tie-c", engine.StatusRunning, tie)
		insertMySQLInstance(t, store, "tie-d", engine.StatusRunning, tie)

		// Page through with pageSize=2; expect every instance exactly once, no skips/dups.
		seen := make(map[string]int)
		var cursor string
		for {
			page, err := lister.List(t.Context(), runtime.InstanceFilter{Limit: 2, Cursor: cursor})
			require.NoError(t, err)
			for _, it := range page.Items {
				seen[it.InstanceID]++
			}
			if !page.HasMore {
				break
			}
			cursor = page.NextCursor
		}
		assert.Len(t, seen, 4, "all 4 instances must appear across pages")
		for id, count := range seen {
			assert.Equal(t, 1, count, "instance %q appeared %d times (want 1)", id, count)
		}
	})

	t.Run("default limit 50", func(t *testing.T) {
		t.Parallel()
		lister, store := newMySQLLister(t)
		base := time.Date(2026, 6, 28, 8, 0, 0, 0, time.UTC)

		for i := range 3 {
			insertMySQLInstance(t, store, "def"+string(rune('a'+i)), engine.StatusRunning, base.Add(time.Duration(i)*time.Minute))
		}

		page, err := lister.List(t.Context(), runtime.InstanceFilter{})
		require.NoError(t, err)
		require.Len(t, page.Items, 3)
		assert.False(t, page.HasMore)
	})

	t.Run("projects all summary fields", func(t *testing.T) {
		t.Parallel()
		lister, store := newMySQLLister(t)
		at := time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC)
		_, err := store.Create(t.Context(), runtime.AppliedStep{
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
		assert.Equal(t, "proj-1", it.InstanceID)
		assert.Equal(t, "mydef", it.DefID)
		assert.Equal(t, 5, it.DefVersion)
		assert.Equal(t, engine.StatusRunning, it.Status)
		assert.True(t, it.StartedAt.Equal(at))
		assert.Nil(t, it.EndedAt)
	})
}

// TestLister_StatusFilter verifies that Status filter restricts results.
func TestLister_StatusFilter(t *testing.T) {
	t.Parallel()
	lister, store := newMySQLLister(t)
	base := time.Date(2026, 6, 28, 8, 0, 0, 0, time.UTC)
	completed := engine.StatusCompleted

	insertMySQLInstance(t, store, "r1", engine.StatusRunning, base)
	insertMySQLInstance(t, store, "c1", engine.StatusCompleted, base.Add(time.Minute))
	insertMySQLInstance(t, store, "c2", engine.StatusCompleted, base.Add(2*time.Minute))

	page, err := lister.List(t.Context(), runtime.InstanceFilter{Status: &completed})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	for _, it := range page.Items {
		assert.Equal(t, engine.StatusCompleted, it.Status)
	}
}

// TestLister_IncidentCount verifies the MySQL JSON_TYPE/JSON_LENGTH incident
// counting expression: an instance with incidents yields IncidentCount>0; one
// without yields 0.
func TestLister_IncidentCount(t *testing.T) {
	t.Parallel()
	lister, store := newMySQLLister(t)
	base := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)

	// Instance with one incident.
	insertMySQLInstanceWithIncidents(t, store, "with-incident", base, []engine.Incident{
		{ID: "inc-1", NodeID: "task", TokenID: "tok-1", Error: "boom"},
	})
	// Instance with no incidents.
	insertMySQLInstance(t, store, "no-incident", engine.StatusRunning, base.Add(time.Minute))

	page, err := lister.List(t.Context(), runtime.InstanceFilter{})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)

	byID := make(map[string]runtime.InstanceSummary, len(page.Items))
	for _, it := range page.Items {
		byID[it.InstanceID] = it
	}

	assert.Equal(t, 1, byID["with-incident"].IncidentCount, "with-incident: want IncidentCount==1")
	assert.Equal(t, 0, byID["no-incident"].IncidentCount, "no-incident: want IncidentCount==0")
}

// TestLister_IncludeTotal verifies opt-in total-count via COUNT(*):
//   - IncludeTotal=true + status filter → TotalCount == matching count, independent of Limit.
//   - IncludeTotal=false → TotalCount==0.
//   - IncludeTotal=true without status filter → TotalCount == total row count.
func TestLister_IncludeTotal(t *testing.T) {
	t.Parallel()
	lister, store := newMySQLLister(t)
	base := time.Date(2026, 6, 28, 8, 0, 0, 0, time.UTC)
	completed := engine.StatusCompleted

	insertMySQLInstance(t, store, "r1", engine.StatusRunning, base)
	insertMySQLInstance(t, store, "r2", engine.StatusRunning, base.Add(time.Minute))
	insertMySQLInstance(t, store, "c1", engine.StatusCompleted, base.Add(2*time.Minute))
	insertMySQLInstance(t, store, "c2", engine.StatusCompleted, base.Add(3*time.Minute))
	insertMySQLInstance(t, store, "c3", engine.StatusCompleted, base.Add(4*time.Minute))

	t.Run("with status filter counts matching", func(t *testing.T) {
		t.Parallel()
		page, err := lister.List(t.Context(), runtime.InstanceFilter{
			Status:       &completed,
			Limit:        1,
			IncludeTotal: true,
		})
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
		assert.Equal(t, 3, page.TotalCount)
	})

	t.Run("without IncludeTotal returns zero", func(t *testing.T) {
		t.Parallel()
		page, err := lister.List(t.Context(), runtime.InstanceFilter{
			Status:       &completed,
			Limit:        10,
			IncludeTotal: false,
		})
		require.NoError(t, err)
		assert.Equal(t, 0, page.TotalCount)
	})

	t.Run("no status filter counts all", func(t *testing.T) {
		t.Parallel()
		page, err := lister.List(t.Context(), runtime.InstanceFilter{
			Limit:        1,
			IncludeTotal: true,
		})
		require.NoError(t, err)
		assert.Equal(t, 5, page.TotalCount)
	})
}
