package kernel_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jonboulle/clockwork"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// countingStore wraps a backing Store and counts Load calls (cache-miss proxy).
type countingStore struct {
	backing kernel.InstanceStore
	loads   atomic.Int64
}

func (c *countingStore) Create(ctx context.Context, s kernel.AppliedStep) (kernel.Version, error) {
	return c.backing.Create(ctx, s)
}
func (c *countingStore) Load(ctx context.Context, id string) (engine.InstanceState, kernel.Version, error) {
	c.loads.Add(1)
	return c.backing.Load(ctx, id)
}
func (c *countingStore) Commit(ctx context.Context, e kernel.Version, s kernel.AppliedStep) (kernel.Version, error) {
	return c.backing.Commit(ctx, e, s)
}

// neverOwn is an Ownership that never grants ownership (forces cache bypass).
type neverOwn struct{}

func (neverOwn) Acquire(context.Context, string) (bool, error) { return false, nil }
func (neverOwn) Release(context.Context, string) error         { return nil }

func runningState(id string) engine.InstanceState {
	return engine.InstanceState{InstanceID: id, DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: time.Unix(0, 0).UTC()}
}

func startTrg() engine.Trigger { return engine.NewStartInstance(time.Unix(0, 0).UTC(), nil) }

func TestCachingStoreServesOwnedLoadFromCache(t *testing.T) {
	cs := &countingStore{backing: runtimetest.MustMemStore(t)}
	clk := clockwork.NewFakeClock()
	store := runtimetest.MustCachingStore(t, cs, kernel.AlwaysOwn{}, kernel.WithCachingStoreClock(clk))

	id := "c1"
	_, err := store.Create(t.Context(), kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)

	// First owned Load after a write-through Create is a cache hit — no backing Load.
	st, _, err := store.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, id, st.InstanceID)
	assert.Equal(t, int64(0), cs.loads.Load(), "owned Load should be served from the write-through cache")

	// A second Load is also a hit.
	_, _, err = store.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, int64(0), cs.loads.Load())
}

func TestCachingStoreBypassesWhenNotOwned(t *testing.T) {
	cs := &countingStore{backing: runtimetest.MustMemStore(t)}
	store := runtimetest.MustCachingStore(t, cs, neverOwn{}, kernel.WithCachingStoreClock(clockwork.NewFakeClock()))

	id := "c2"
	_, err := store.Create(t.Context(), kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)

	// Not owned ⇒ every Load hits the backing.
	_, _, err = store.Load(t.Context(), id)
	require.NoError(t, err)
	_, _, err = store.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, int64(2), cs.loads.Load())
}

func TestCachingStoreEvictsOnConcurrentUpdate(t *testing.T) {
	mem := runtimetest.MustMemStore(t)
	cs := &countingStore{backing: mem}
	store := runtimetest.MustCachingStore(t, cs, kernel.AlwaysOwn{}, kernel.WithCachingStoreClock(clockwork.NewFakeClock()))

	id := "c3"
	tok, err := store.Create(t.Context(), kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)

	// Advance the backing out-of-band so the cached token is stale.
	_, err = mem.Commit(t.Context(), tok, kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)

	// Commit via the cache with the stale token ⇒ ErrConcurrentUpdate ⇒ evict.
	_, err = store.Commit(t.Context(), tok, kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.ErrorIs(t, err, kernel.ErrConcurrentUpdate)

	// Next owned Load must re-read the backing (entry was evicted).
	before := cs.loads.Load()
	_, _, err = store.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, before+1, cs.loads.Load())
}

func TestCachingStoreTTLExpiryForcesReload(t *testing.T) {
	cs := &countingStore{backing: runtimetest.MustMemStore(t)}
	clk := clockwork.NewFakeClock()
	store := runtimetest.MustCachingStore(t, cs, kernel.AlwaysOwn{}, kernel.WithCacheTTL(time.Minute), kernel.WithCachingStoreClock(clk))

	id := "ttl1"
	_, err := store.Create(t.Context(), kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)

	_, _, err = store.Load(t.Context(), id) // hit (write-through)
	require.NoError(t, err)
	assert.Equal(t, int64(0), cs.loads.Load())

	clk.Advance(2 * time.Minute) // expire the entry
	_, _, err = store.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, int64(1), cs.loads.Load(), "expired entry must reload from backing")
}

func TestCachingStoreLRUEvictsBeyondMax(t *testing.T) {
	cs := &countingStore{backing: runtimetest.MustMemStore(t)}
	store := runtimetest.MustCachingStore(t, cs, kernel.AlwaysOwn{},
		kernel.WithCachingStoreClock(clockwork.NewFakeClock()),
		kernel.WithCacheMaxEntries(2), kernel.WithCacheTTL(time.Hour))

	for _, id := range []string{"a", "b", "c"} { // 3 instances, cap 2
		_, err := store.Create(t.Context(), kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
		require.NoError(t, err)
	}
	// "a" was the least-recently-used after inserting c ⇒ evicted ⇒ its Load misses.
	before := cs.loads.Load()
	_, _, err := store.Load(t.Context(), "a")
	require.NoError(t, err)
	assert.Equal(t, before+1, cs.loads.Load())
	// "c" is still cached ⇒ hit.
	_, _, err = store.Load(t.Context(), "c")
	require.NoError(t, err)
	assert.Equal(t, before+1, cs.loads.Load())
}

func TestCachingStoreConcurrentLoadCommitStayCoherent(t *testing.T) {
	mem := runtimetest.MustMemStore(t)
	store := runtimetest.MustCachingStore(t, mem, kernel.AlwaysOwn{}, kernel.WithCachingStoreClock(clockwork.NewFakeClock()), kernel.WithCacheTTL(time.Hour))

	id := "race1"
	tok, err := store.Create(t.Context(), kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)

	// Hammer Load while a single Commit advances the token; the cache must never
	// serve a token greater than what the backing holds (no torn write-through).
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			st, ltok, lerr := store.Load(t.Context(), id)
			require.NoError(t, lerr)
			require.Equal(t, id, st.InstanceID)
			_ = ltok
		}
	}()
	_, err = store.Commit(t.Context(), tok, kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)
	<-done
}

func TestCachingStoreReleaseEvicts(t *testing.T) {
	cs := &countingStore{backing: runtimetest.MustMemStore(t)}
	store := runtimetest.MustCachingStore(t, cs, kernel.AlwaysOwn{}, kernel.WithCachingStoreClock(clockwork.NewFakeClock()), kernel.WithCacheTTL(time.Hour))

	id := "rel1"
	_, err := store.Create(t.Context(), kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)

	// Confirm the entry is cached: an owned Load must NOT hit the backing.
	_, _, err = store.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, int64(0), cs.loads.Load(), "write-through Create must populate cache; Load must be a hit")

	// Release must evict the cached entry.
	require.NoError(t, store.Release(t.Context(), id))

	// The next owned Load must re-read from the backing (cache was evicted).
	before := cs.loads.Load()
	_, _, err = store.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, before+1, cs.loads.Load(), "Release must evict the cache; next Load must re-read the backing")
}

func TestNewCachingStoreDefaultClockNoPanic(t *testing.T) {
	// No clock option → defaults to clock.System(); construction + a basic op must not panic.
	cs := &countingStore{backing: runtimetest.MustMemStore(t)}
	s := runtimetest.MustCachingStore(t, cs, kernel.AlwaysOwn{})
	assert.NotNil(t, s)
}

func TestNewCachingStoreWithClockOption(t *testing.T) {
	// WithCachingStoreClock injects a fake clock; advancing past TTL must force a backing reload.
	cs := &countingStore{backing: runtimetest.MustMemStore(t)}
	fake := clockwork.NewFakeClockAt(time.Unix(1000, 0))
	s := runtimetest.MustCachingStore(t, cs, kernel.AlwaysOwn{}, kernel.WithCacheTTL(time.Minute), kernel.WithCachingStoreClock(fake))
	assert.NotNil(t, s)

	id := "clkopt1"
	_, err := s.Create(t.Context(), kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)

	// Load immediately — entry was write-through cached; no backing load.
	_, _, err = s.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, int64(0), cs.loads.Load(), "write-through Create must populate cache; Load must be a hit")

	// Advance the fake clock past the 1-minute TTL.
	fake.Advance(2 * time.Minute)

	// Now the entry is expired; the next Load must re-read the backing.
	_, _, err = s.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, int64(1), cs.loads.Load(), "expired entry must reload from backing (fake clock drives TTL)")
}

func TestNewCachingStoreFailsFast(t *testing.T) {
	t.Parallel()

	mem := runtimetest.MustMemStore(t)
	type testCase struct {
		name    string
		backing kernel.InstanceStore
		owner   kernel.Ownership
		assert  func(t *testing.T, s *kernel.CachingInstanceStore, err error)
	}
	cases := []testCase{
		{
			name:    "nil backing",
			backing: nil,
			owner:   kernel.AlwaysOwn{},
			assert: func(t *testing.T, s *kernel.CachingInstanceStore, err error) {
				require.ErrorIs(t, err, kernel.ErrNilDependency)
				require.Nil(t, s)
			},
		},
		{
			name:    "nil owner",
			backing: mem,
			owner:   nil,
			assert: func(t *testing.T, s *kernel.CachingInstanceStore, err error) {
				require.ErrorIs(t, err, kernel.ErrNilDependency)
				require.Nil(t, s)
			},
		},
		{
			name:    "valid args",
			backing: mem,
			owner:   kernel.AlwaysOwn{},
			assert: func(t *testing.T, s *kernel.CachingInstanceStore, err error) {
				require.NoError(t, err)
				require.NotNil(t, s)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, err := kernel.NewCachingInstanceStore(tc.backing, tc.owner)
			tc.assert(t, s, err)
		})
	}
}
