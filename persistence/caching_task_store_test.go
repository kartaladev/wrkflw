package persistence_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// TestCachingTaskStoreByteSubstrate proves that a HumanTask survives the JSON
// marshal→unmarshal round-trip exercised by the byte-only (non-ValueCache) substrate
// path — the path used by distributed caches such as Redis or Memcached.
//
// The byteOnlyProvider/byteOnlyCache test double is defined in
// caching_instance_store_test.go (same persistence_test package).
func TestCachingTaskStoreByteSubstrate(t *testing.T) {
	t.Parallel()

	backing := &countingTaskStore{MemTaskStore: humantask.NewMemTaskStore()}
	cs, err := persistence.NewCachingTaskStore(backing, newByteOnlyProvider(), persistence.WithHumanTaskCacheTTL(time.Minute))
	require.NoError(t, err)

	ctx := t.Context()
	want := humantask.HumanTask{
		TaskToken:  "byte-tok-1",
		InstanceID: "inst-99",
		State:      humantask.Claimed,
		ClaimedBy:  "alice",
		Candidates: []string{"alice", "bob"},
		Eligibility: authz.AuthzSpec{
			Roles: []string{"reviewer", "approver"},
		},
	}

	// Upsert write-through populates the byte cache.
	require.NoError(t, cs.Upsert(ctx, want))

	// First Get: should be served from the byte cache (write-through), not the backing.
	backingGetsBefore := backing.gets
	got, err := cs.Get(ctx, want.TaskToken)
	require.NoError(t, err)
	assert.Equal(t, backingGetsBefore, backing.gets, "first Get after Upsert should be a cache hit (byte path)")

	// Non-trivial field assertions — prove JSON unmarshal fidelity.
	assert.Equal(t, humantask.Claimed, got.State, "State must survive JSON round-trip")
	assert.Equal(t, "alice", got.ClaimedBy, "ClaimedBy must survive JSON round-trip")
	assert.Equal(t, want.Candidates, got.Candidates, "Candidates slice must survive JSON round-trip")
}
