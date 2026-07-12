package store_test

import (
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedInstance inserts a minimal instance via Store.Create for use in Lister tests.
func seedInstance(t *testing.T, s *store.Store, id string, status engine.Status, at time.Time) {
	t.Helper()
	_, err := s.Create(t.Context(), kernel.AppliedStep{
		State: engine.InstanceState{
			InstanceID: id,
			DefID:      "d",
			DefVersion: 1,
			Status:     status,
			StartedAt:  at,
		},
		Trigger: engine.NewStartInstance(at, nil),
	})
	require.NoError(t, err, "seedInstance %q on", id)
}

// seedInstanceWithIncidents inserts an instance with incidents embedded in its snapshot.
func seedInstanceWithIncidents(t *testing.T, s *store.Store, id string, at time.Time, incidents []engine.Incident) {
	t.Helper()
	_, err := s.Create(t.Context(), kernel.AppliedStep{
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
	require.NoError(t, err, "seedInstanceWithIncidents %q", id)
}

// TestListerOrdering verifies that List returns instances ordered newest-first
// (started_at DESC, instance_id DESC) across all three dialects.
func TestListerOrdering(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		lister, err := store.NewLister(b.conn, b.dialect)
		require.NoError(t, err)
		var _ kernel.InstanceLister = lister // compile-time interface check

		base := time.Date(2026, 6, 21, 8, 0, 0, 0, time.UTC)
		seedInstance(t, s, "a", engine.StatusRunning, base)
		seedInstance(t, s, "b", engine.StatusRunning, base.Add(time.Minute))
		seedInstance(t, s, "c", engine.StatusRunning, base.Add(2*time.Minute))

		page, err := lister.List(t.Context(), kernel.InstanceFilter{})
		require.NoError(t, err, "%s: list", b.name)
		require.Len(t, page.Items, 3, "%s: want 3 items", b.name)
		assert.Equal(t, "c", page.Items[0].InstanceID, "%s: want c first", b.name)
		assert.Equal(t, "b", page.Items[1].InstanceID, "%s: want b second", b.name)
		assert.Equal(t, "a", page.Items[2].InstanceID, "%s: want a third", b.name)
		assert.False(t, page.HasMore, "%s: want HasMore=false", b.name)
		assert.Empty(t, page.NextCursor, "%s: want empty cursor", b.name)
	})
}

// TestListerStatusFilter verifies that the Status filter restricts results.
func TestListerStatusFilter(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		lister, err := store.NewLister(b.conn, b.dialect)
		require.NoError(t, err)

		base := time.Date(2026, 6, 21, 8, 0, 0, 0, time.UTC)
		completed := engine.StatusCompleted

		seedInstance(t, s, "r1", engine.StatusRunning, base)
		seedInstance(t, s, "c1", engine.StatusCompleted, base.Add(time.Minute))
		seedInstance(t, s, "c2", engine.StatusCompleted, base.Add(2*time.Minute))

		page, err := lister.List(t.Context(), kernel.InstanceFilter{Status: &completed})
		require.NoError(t, err, "%s: list", b.name)
		require.Len(t, page.Items, 2, "%s: want 2 completed", b.name)
		for _, it := range page.Items {
			assert.Equal(t, engine.StatusCompleted, it.Status,
				"%s: status must be completed", b.name)
		}
	})
}

// TestListerKeyset_TwoPageWalk verifies two-page keyset pagination across dialects.
func TestListerKeyset_TwoPageWalk(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		lister, err := store.NewLister(b.conn, b.dialect)
		require.NoError(t, err)

		base := time.Date(2026, 6, 21, 8, 0, 0, 0, time.UTC)
		running := engine.StatusRunning

		seedInstance(t, s, "i1", engine.StatusRunning, base)
		seedInstance(t, s, "i2", engine.StatusRunning, base.Add(time.Minute))
		seedInstance(t, s, "i3", engine.StatusRunning, base.Add(2*time.Minute))

		// page 1: limit=2
		p1, err := lister.List(t.Context(), kernel.InstanceFilter{Status: &running, Limit: 2})
		require.NoError(t, err, "%s: page1", b.name)
		require.Len(t, p1.Items, 2, "%s: page1 want 2 items", b.name)
		assert.Equal(t, "i3", p1.Items[0].InstanceID, "%s: page1[0] want i3", b.name)
		assert.Equal(t, "i2", p1.Items[1].InstanceID, "%s: page1[1] want i2", b.name)
		assert.True(t, p1.HasMore, "%s: page1 want HasMore=true", b.name)
		assert.NotEmpty(t, p1.NextCursor, "%s: page1 want NextCursor", b.name)

		// page 2: use cursor from page 1
		p2, err := lister.List(t.Context(), kernel.InstanceFilter{Status: &running, Limit: 2, Cursor: p1.NextCursor})
		require.NoError(t, err, "%s: page2", b.name)
		require.Len(t, p2.Items, 1, "%s: page2 want 1 item", b.name)
		assert.Equal(t, "i1", p2.Items[0].InstanceID, "%s: page2[0] want i1", b.name)
		assert.False(t, p2.HasMore, "%s: page2 want HasMore=false", b.name)
	})
}

// TestListerKeyset_TieBoundary seeds 4 instances with identical started_at values and
// pages through them in pages of 2, asserting each instance appears exactly once.
func TestListerKeyset_TieBoundary(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		lister, err := store.NewLister(b.conn, b.dialect)
		require.NoError(t, err)

		tie := time.Date(2026, 6, 28, 9, 0, 0, 0, time.UTC)
		seedInstance(t, s, "tie-a", engine.StatusRunning, tie)
		seedInstance(t, s, "tie-b", engine.StatusRunning, tie)
		seedInstance(t, s, "tie-c", engine.StatusRunning, tie)
		seedInstance(t, s, "tie-d", engine.StatusRunning, tie)

		seen := make(map[string]int)
		var cursor string
		for {
			page, err := lister.List(t.Context(), kernel.InstanceFilter{Limit: 2, Cursor: cursor})
			require.NoError(t, err, "%s: list page", b.name)
			for _, it := range page.Items {
				seen[it.InstanceID]++
			}
			if !page.HasMore {
				break
			}
			cursor = page.NextCursor
		}
		assert.Len(t, seen, 4, "%s: all 4 instances must appear across pages", b.name)
		for id, count := range seen {
			assert.Equal(t, 1, count, "%s: instance %q appeared %d times (want 1)", b.name, id, count)
		}
	})
}

// TestListerDefaultLimit verifies that Limit=0 defaults to 50 (returns all 3 items when <50).
func TestListerDefaultLimit(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		lister, err := store.NewLister(b.conn, b.dialect)
		require.NoError(t, err)

		base := time.Date(2026, 6, 21, 8, 0, 0, 0, time.UTC)
		for i := range 3 {
			seedInstance(t, s, "def"+string(rune('a'+i)), engine.StatusRunning,
				base.Add(time.Duration(i)*time.Minute))
		}

		page, err := lister.List(t.Context(), kernel.InstanceFilter{})
		require.NoError(t, err, "%s: list", b.name)
		require.Len(t, page.Items, 3, "%s: want 3 items", b.name)
		assert.False(t, page.HasMore, "%s: want HasMore=false", b.name)
	})
}

// TestListerProjectsFields verifies that all summary fields are correctly projected.
func TestListerProjectsFields(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		lister, err := store.NewLister(b.conn, b.dialect)
		require.NoError(t, err)

		at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
		_, err = s.Create(t.Context(), kernel.AppliedStep{
			State: engine.InstanceState{
				InstanceID: "proj-1",
				DefID:      "mydef",
				DefVersion: 5,
				Status:     engine.StatusRunning,
				StartedAt:  at,
			},
			Trigger: engine.NewStartInstance(at, nil),
		})
		require.NoError(t, err, "%s: create", b.name)

		page, err := lister.List(t.Context(), kernel.InstanceFilter{})
		require.NoError(t, err, "%s: list", b.name)
		require.Len(t, page.Items, 1, "%s: want 1 item", b.name)
		it := page.Items[0]
		assert.Equal(t, "proj-1", it.InstanceID, "%s: InstanceID", b.name)
		assert.Equal(t, "mydef", it.DefID, "%s: DefID", b.name)
		assert.Equal(t, 5, it.DefVersion, "%s: DefVersion", b.name)
		assert.Equal(t, engine.StatusRunning, it.Status, "%s: Status", b.name)
		assert.True(t, it.StartedAt.Equal(at), "%s: StartedAt mismatch: want %v got %v", b.name, at, it.StartedAt)
		assert.Nil(t, it.EndedAt, "%s: EndedAt want nil", b.name)
	})
}

// TestListerStartedAtUTC verifies that StartedAt survives the round-trip with UTC location
// on all dialects (ADR-0080). Uses a non-UTC timezone for the test.
func TestListerStartedAtUTC(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		lister, err := store.NewLister(b.conn, b.dialect)
		require.NoError(t, err)

		// Use second-precision to avoid sub-second SQLite truncation
		at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
		seedInstance(t, s, "utc-chk", engine.StatusRunning, at)

		page, err := lister.List(t.Context(), kernel.InstanceFilter{})
		require.NoError(t, err, "%s: list", b.name)
		require.Len(t, page.Items, 1)

		it := page.Items[0]
		assert.True(t, it.StartedAt.Equal(at),
			"%s: StartedAt must round-trip to same instant: want %v got %v", b.name, at, it.StartedAt)
		assert.Equal(t, time.UTC, it.StartedAt.Location(),
			"%s: StartedAt must be UTC-located", b.name)
	})
}

// TestListerIncidentCount verifies that the incident count is correctly read
// from the snapshot JSON column for each dialect.
func TestListerIncidentCount(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		lister, err := store.NewLister(b.conn, b.dialect)
		require.NoError(t, err)

		base := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

		// Instance with one incident.
		seedInstanceWithIncidents(t, s, "with-incident", base, []engine.Incident{
			{ID: "inc-1", NodeID: "task", TokenID: "tok-1", Error: "boom"},
		})
		// Instance with no incidents.
		seedInstance(t, s, "no-incident", engine.StatusRunning, base.Add(time.Minute))

		page, err := lister.List(t.Context(), kernel.InstanceFilter{})
		require.NoError(t, err, "%s: list", b.name)
		require.Len(t, page.Items, 2, "%s: want 2 items", b.name)

		byID := make(map[string]kernel.InstanceSummary, len(page.Items))
		for _, it := range page.Items {
			byID[it.InstanceID] = it
		}
		assert.Equal(t, 1, byID["with-incident"].IncidentCount,
			"%s: with-incident: want IncidentCount==1", b.name)
		assert.Equal(t, 0, byID["no-incident"].IncidentCount,
			"%s: no-incident: want IncidentCount==0", b.name)
	})
}

// TestListerIncludeTotal verifies opt-in total-count via COUNT(*) on all dialects.
func TestListerIncludeTotal(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		lister, err := store.NewLister(b.conn, b.dialect)
		require.NoError(t, err)

		base := time.Date(2026, 6, 23, 8, 0, 0, 0, time.UTC)
		completed := engine.StatusCompleted

		seedInstance(t, s, "r1", engine.StatusRunning, base)
		seedInstance(t, s, "r2", engine.StatusRunning, base.Add(time.Minute))
		seedInstance(t, s, "c1", engine.StatusCompleted, base.Add(2*time.Minute))
		seedInstance(t, s, "c2", engine.StatusCompleted, base.Add(3*time.Minute))
		seedInstance(t, s, "c3", engine.StatusCompleted, base.Add(4*time.Minute))

		t.Run("IncludeTotal=true with status filter independent of Limit", func(t *testing.T) {
			t.Parallel()
			page, err := lister.List(t.Context(), kernel.InstanceFilter{
				Status:       &completed,
				Limit:        1,
				IncludeTotal: true,
			})
			require.NoError(t, err, "%s: list", b.name)
			require.Len(t, page.Items, 1, "%s: want 1 item (limit=1)", b.name)
			assert.Equal(t, 3, page.TotalCount, "%s: want TotalCount=3 for completed", b.name)
		})

		t.Run("IncludeTotal=false returns TotalCount=0", func(t *testing.T) {
			t.Parallel()
			page, err := lister.List(t.Context(), kernel.InstanceFilter{
				Status:       &completed,
				Limit:        10,
				IncludeTotal: false,
			})
			require.NoError(t, err, "%s: list", b.name)
			assert.Equal(t, 0, page.TotalCount, "%s: want TotalCount=0 when not requested", b.name)
		})

		t.Run("IncludeTotal=true no status filter counts all", func(t *testing.T) {
			t.Parallel()
			page, err := lister.List(t.Context(), kernel.InstanceFilter{
				Limit:        1,
				IncludeTotal: true,
			})
			require.NoError(t, err, "%s: list", b.name)
			assert.Equal(t, 5, page.TotalCount, "%s: want TotalCount=5 for all instances", b.name)
		})
	})
}
