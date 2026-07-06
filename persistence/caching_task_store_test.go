package persistence_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/persistence/cache/hotcache"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// countingTaskStore counts backing Get calls to prove cache hits skip the backing.
type countingTaskStore struct {
	*humantask.MemTaskStore
	gets int
}

func (s *countingTaskStore) Get(ctx context.Context, token string) (humantask.HumanTask, error) {
	s.gets++
	return s.MemTaskStore.Get(ctx, token)
}

func TestCachingTaskStore(t *testing.T) {
	tests := []struct {
		name   string
		assert func(t *testing.T, cs *persistence.CachingTaskStore, backing *countingTaskStore)
	}{
		{
			name: "second Get is a cache hit",
			assert: func(t *testing.T, cs *persistence.CachingTaskStore, backing *countingTaskStore) {
				ctx := t.Context()
				_ = cs.Upsert(ctx, humantask.HumanTask{TaskToken: "t1", State: humantask.Unclaimed})
				if _, err := cs.Get(ctx, "t1"); err != nil {
					t.Fatalf("get1: %v", err)
				}
				before := backing.gets
				if _, err := cs.Get(ctx, "t1"); err != nil {
					t.Fatalf("get2: %v", err)
				}
				if backing.gets != before {
					t.Fatalf("expected cache hit, backing.gets went %d -> %d", before, backing.gets)
				}
			},
		},
		{
			name: "Upsert refreshes the cached entry (write-through)",
			assert: func(t *testing.T, cs *persistence.CachingTaskStore, backing *countingTaskStore) {
				ctx := t.Context()
				_ = cs.Upsert(ctx, humantask.HumanTask{TaskToken: "t1", State: humantask.Unclaimed})
				_, _ = cs.Get(ctx, "t1")
				_ = cs.Upsert(ctx, humantask.HumanTask{TaskToken: "t1", State: humantask.Claimed, ClaimedBy: "alice"})
				got, err := cs.Get(ctx, "t1")
				if err != nil {
					t.Fatalf("get: %v", err)
				}
				if got.State != humantask.Claimed || got.ClaimedBy != "alice" {
					t.Fatalf("stale after upsert: %+v", got)
				}
			},
		},
		{
			name: "not-found is not cached",
			assert: func(t *testing.T, cs *persistence.CachingTaskStore, backing *countingTaskStore) {
				ctx := t.Context()
				if _, err := cs.Get(ctx, "missing"); !errors.Is(err, humantask.ErrTaskNotFound) {
					t.Fatalf("want ErrTaskNotFound, got %v", err)
				}
				before := backing.gets
				_, _ = cs.Get(ctx, "missing")
				if backing.gets == before {
					t.Fatal("not-found must NOT be cached; backing should be hit again")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			backing := &countingTaskStore{MemTaskStore: humantask.NewMemTaskStore()}
			cs, err := persistence.NewCachingTaskStore(backing, hotcache.New(), persistence.WithHumanTaskCacheTTL(time.Minute))
			if err != nil {
				t.Fatalf("new: %v", err)
			}
			tt.assert(t, cs, backing)
		})
	}
}

func TestNewCachingTaskStoreFailsFast(t *testing.T) {
	t.Parallel()
	t.Run("nil backing", func(t *testing.T) {
		t.Parallel()
		_, err := persistence.NewCachingTaskStore(nil, hotcache.New())
		if !errors.Is(err, kernel.ErrNilDependency) {
			t.Fatalf("want ErrNilDependency, got %v", err)
		}
	})
	t.Run("nil provider", func(t *testing.T) {
		t.Parallel()
		_, err := persistence.NewCachingTaskStore(humantask.NewMemTaskStore(), nil)
		if !errors.Is(err, kernel.ErrNilDependency) {
			t.Fatalf("want ErrNilDependency, got %v", err)
		}
	})
}

func TestCachingTaskStorePassThroughMethods(t *testing.T) {
	t.Parallel()
	// AssignedTo and ClaimableBy must pass straight through to backing.
	backing := humantask.NewMemTaskStore()
	cs, err := persistence.NewCachingTaskStore(backing, hotcache.New())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := t.Context()
	task := humantask.HumanTask{
		TaskToken: "tok1",
		State:     humantask.Claimed,
		ClaimedBy: "bob",
	}
	if err := cs.Upsert(ctx, task); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	assigned, err := cs.AssignedTo(ctx, "bob")
	if err != nil {
		t.Fatalf("assigned-to: %v", err)
	}
	if len(assigned) != 1 || assigned[0].TaskToken != "tok1" {
		t.Fatalf("unexpected AssignedTo result: %v", assigned)
	}

	claimable, err := cs.ClaimableBy(ctx, authz.Actor{ID: "bob"})
	if err != nil {
		t.Fatalf("claimable-by: %v", err)
	}
	_ = claimable
}
