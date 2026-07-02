package runtime_test

import (
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// newInstanceState builds a minimal engine.InstanceState for seeding tests.
func newInstanceState(id string, st engine.Status, at time.Time) engine.InstanceState {
	return engine.InstanceState{
		InstanceID: id,
		DefID:      "d",
		DefVersion: 1,
		Status:     st,
		StartedAt:  at,
	}
}

// seedMemStore creates a MemStore and inserts each InstanceState via Create, returning the store.
func seedMemStore(t *testing.T, states ...engine.InstanceState) *runtime.MemStore {
	t.Helper()
	ms := mustMemStore(t)
	for _, st := range states {
		now := st.StartedAt
		_, err := ms.Create(t.Context(), runtime.AppliedStep{
			State:   st,
			Trigger: engine.NewStartInstance(now, nil),
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	return ms
}

func TestMemStoreList(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	running := engine.StatusRunning
	completed := engine.StatusCompleted

	tests := []struct {
		name   string
		filter runtime.InstanceFilter
		seed   func(t *testing.T) *runtime.MemStore
		assert func(t *testing.T, page runtime.InstancePage)
	}{
		{
			name:   "orders by started_at desc then instance_id desc",
			filter: runtime.InstanceFilter{},
			seed: func(t *testing.T) *runtime.MemStore {
				return seedMemStore(t,
					newInstanceState("a", engine.StatusRunning, base),
					newInstanceState("b", engine.StatusRunning, base.Add(time.Minute)),
					newInstanceState("c", engine.StatusRunning, base.Add(2*time.Minute)),
				)
			},
			assert: func(t *testing.T, page runtime.InstancePage) {
				ids := make([]string, len(page.Items))
				for i, it := range page.Items {
					ids[i] = it.InstanceID
				}
				if len(ids) != 3 || ids[0] != "c" || ids[1] != "b" || ids[2] != "a" {
					t.Fatalf("want [c b a], got %v", ids)
				}
				if page.HasMore {
					t.Fatal("want HasMore=false for all-items page")
				}
			},
		},
		{
			name:   "same started_at: tiebreak on instance_id desc",
			filter: runtime.InstanceFilter{},
			seed: func(t *testing.T) *runtime.MemStore {
				return seedMemStore(t,
					newInstanceState("alpha", engine.StatusRunning, base),
					newInstanceState("beta", engine.StatusRunning, base),
					newInstanceState("gamma", engine.StatusRunning, base),
				)
			},
			assert: func(t *testing.T, page runtime.InstancePage) {
				if len(page.Items) != 3 {
					t.Fatalf("want 3 items, got %d", len(page.Items))
				}
				// lexicographic desc: gamma > beta > alpha
				if page.Items[0].InstanceID != "gamma" || page.Items[2].InstanceID != "alpha" {
					t.Fatalf("want [gamma beta alpha], got [%s … %s]",
						page.Items[0].InstanceID, page.Items[2].InstanceID)
				}
			},
		},
		{
			name:   "status filter returns only matching instances",
			filter: runtime.InstanceFilter{Status: &completed},
			seed: func(t *testing.T) *runtime.MemStore {
				return seedMemStore(t,
					newInstanceState("r1", engine.StatusRunning, base),
					newInstanceState("c1", engine.StatusCompleted, base.Add(time.Minute)),
					newInstanceState("c2", engine.StatusCompleted, base.Add(2*time.Minute)),
				)
			},
			assert: func(t *testing.T, page runtime.InstancePage) {
				if len(page.Items) != 2 {
					t.Fatalf("want 2 completed, got %d", len(page.Items))
				}
				for _, it := range page.Items {
					if it.Status != engine.StatusCompleted {
						t.Fatalf("unexpected status %v for %q", it.Status, it.InstanceID)
					}
				}
			},
		},
		{
			name:   "limit=1 yields HasMore=true and usable NextCursor",
			filter: runtime.InstanceFilter{Status: &running, Limit: 1},
			seed: func(t *testing.T) *runtime.MemStore {
				return seedMemStore(t,
					newInstanceState("i1", engine.StatusRunning, base),
					newInstanceState("i2", engine.StatusRunning, base.Add(time.Minute)),
				)
			},
			assert: func(t *testing.T, page runtime.InstancePage) {
				if len(page.Items) != 1 {
					t.Fatalf("want 1 item, got %d", len(page.Items))
				}
				if !page.HasMore {
					t.Fatal("want HasMore=true")
				}
				if page.NextCursor == "" {
					t.Fatal("want non-empty NextCursor")
				}
				// the first item should be i2 (newest)
				if page.Items[0].InstanceID != "i2" {
					t.Fatalf("want i2 first, got %q", page.Items[0].InstanceID)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ms := tc.seed(t)
			page, err := ms.List(t.Context(), tc.filter)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			tc.assert(t, page)
		})
	}
}

// TestMemStoreListIncludeTotal verifies opt-in total-count behaviour:
//   - IncludeTotal=true returns TotalCount == full match count, independent of Limit.
//   - IncludeTotal=false returns TotalCount==0 (no extra query).
func TestMemStoreListIncludeTotal(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)
	completed := engine.StatusCompleted

	ms := seedMemStore(t,
		newInstanceState("r1", engine.StatusRunning, base),
		newInstanceState("r2", engine.StatusRunning, base.Add(time.Minute)),
		newInstanceState("c1", engine.StatusCompleted, base.Add(2*time.Minute)),
		newInstanceState("c2", engine.StatusCompleted, base.Add(3*time.Minute)),
		newInstanceState("c3", engine.StatusCompleted, base.Add(4*time.Minute)),
	)

	t.Run("IncludeTotal=true returns full matching count independent of Limit", func(t *testing.T) {
		t.Parallel()
		page, err := ms.List(t.Context(), runtime.InstanceFilter{
			Status:       &completed,
			Limit:        1,
			IncludeTotal: true,
		})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(page.Items) != 1 {
			t.Fatalf("want 1 item (limit=1), got %d", len(page.Items))
		}
		if page.TotalCount != 3 {
			t.Fatalf("want TotalCount=3, got %d", page.TotalCount)
		}
	})

	t.Run("IncludeTotal=false returns TotalCount=0", func(t *testing.T) {
		t.Parallel()
		page, err := ms.List(t.Context(), runtime.InstanceFilter{
			Status:       &completed,
			Limit:        10,
			IncludeTotal: false,
		})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if page.TotalCount != 0 {
			t.Fatalf("want TotalCount=0 when IncludeTotal=false, got %d", page.TotalCount)
		}
	})

	t.Run("IncludeTotal=true with nil status counts all", func(t *testing.T) {
		t.Parallel()
		page, err := ms.List(t.Context(), runtime.InstanceFilter{
			Limit:        1,
			IncludeTotal: true,
		})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if page.TotalCount != 5 {
			t.Fatalf("want TotalCount=5 for all instances, got %d", page.TotalCount)
		}
	})
}

// TestMemStoreListTwoPageWalk asserts that two page fetches with a cursor
// correctly walk all items without duplicates or gaps.
func TestMemStoreListTwoPageWalk(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	running := engine.StatusRunning

	ms := seedMemStore(t,
		newInstanceState("i1", engine.StatusRunning, base),
		newInstanceState("i2", engine.StatusRunning, base.Add(time.Minute)),
		newInstanceState("i3", engine.StatusRunning, base.Add(2*time.Minute)),
	)

	// first page: limit=2, should return i3, i2 (newest first)
	p1, err := ms.List(t.Context(), runtime.InstanceFilter{Status: &running, Limit: 2})
	if err != nil {
		t.Fatalf("page1 List: %v", err)
	}
	if len(p1.Items) != 2 {
		t.Fatalf("page1: want 2 items, got %d", len(p1.Items))
	}
	if p1.Items[0].InstanceID != "i3" || p1.Items[1].InstanceID != "i2" {
		t.Fatalf("page1: want [i3 i2], got [%s %s]",
			p1.Items[0].InstanceID, p1.Items[1].InstanceID)
	}
	if !p1.HasMore {
		t.Fatal("page1: want HasMore=true")
	}
	if p1.NextCursor == "" {
		t.Fatal("page1: want non-empty NextCursor")
	}

	// second page using cursor from first page
	p2, err := ms.List(t.Context(), runtime.InstanceFilter{Status: &running, Limit: 2, Cursor: p1.NextCursor})
	if err != nil {
		t.Fatalf("page2 List: %v", err)
	}
	if len(p2.Items) != 1 {
		t.Fatalf("page2: want 1 item, got %d", len(p2.Items))
	}
	if p2.Items[0].InstanceID != "i1" {
		t.Fatalf("page2: want [i1], got [%s]", p2.Items[0].InstanceID)
	}
	if p2.HasMore {
		t.Fatal("page2: want HasMore=false")
	}
}

// TestMemStoreListProjectsFields verifies InstanceSummary fields are correctly
// projected from engine.InstanceState.
func TestMemStoreListProjectsFields(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	ms := seedMemStore(t, engine.InstanceState{
		InstanceID: "proj-1",
		DefID:      "mydef",
		DefVersion: 3,
		Status:     engine.StatusCompleted,
		StartedAt:  now,
	})

	page, err := ms.List(t.Context(), runtime.InstanceFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(page.Items))
	}
	got := page.Items[0]
	if got.InstanceID != "proj-1" {
		t.Errorf("InstanceID: want proj-1, got %q", got.InstanceID)
	}
	if got.DefID != "mydef" {
		t.Errorf("DefID: want mydef, got %q", got.DefID)
	}
	if got.DefVersion != 3 {
		t.Errorf("DefVersion: want 3, got %d", got.DefVersion)
	}
	if got.Status != engine.StatusCompleted {
		t.Errorf("Status: want Completed, got %v", got.Status)
	}
	if !got.StartedAt.Equal(now) {
		t.Errorf("StartedAt: want %v, got %v", now, got.StartedAt)
	}
}
