package persistence_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/persistence/cache/hotcache"
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

	type testCase struct {
		name    string
		backing humantask.TaskStore
		wantErr bool
	}
	cases := []testCase{
		{
			name:    "nil backing",
			backing: nil,
			wantErr: true,
		},
		{
			name:    "nil provider — checked by constructor",
			backing: humantask.NewMemTaskStore(),
			// provider nil is tested inline below
			wantErr: false, // handled separately
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.name == "nil provider — checked by constructor" {
				_, err := persistence.NewCachingTaskStore(tc.backing, nil)
				if err == nil {
					t.Fatal("expected error for nil provider, got nil")
				}
				return
			}
			_, err := persistence.NewCachingTaskStore(tc.backing, hotcache.New())
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestCachingTaskStorePassThroughMethods(t *testing.T) {
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
}
