package persistence_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/persistence/cache"
	"github.com/zakyalvan/krtlwrkflw/persistence/cache/hotcache"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// --- test doubles ------------------------------------------------------------

// countingStore wraps a backing InstanceStore and counts Load calls (cache-miss proxy).
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

// stubOwnership is a non-AlwaysOwn Ownership for the no-warning case.
type stubOwnership struct{}

func (stubOwnership) Acquire(context.Context, string) (bool, error) { return true, nil }
func (stubOwnership) Release(context.Context, string) error         { return nil }

// syncBuffer is a goroutine-safe buffer for capturing slog output.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// byteOnlyProvider is a cache.Provider whose caches implement ONLY cache.Cache
// (not cache.ValueCache), exercising the codec's JSON marshal/unmarshal path.
type byteOnlyProvider struct {
	mu     sync.Mutex
	caches map[string]*byteOnlyCache
}

func newByteOnlyProvider() *byteOnlyProvider {
	return &byteOnlyProvider{caches: map[string]*byteOnlyCache{}}
}

func (p *byteOnlyProvider) Cache(ns string) (cache.Cache, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.caches[ns]
	if !ok {
		c = &byteOnlyCache{m: map[string][]byte{}}
		p.caches[ns] = c
	}
	return c, nil
}

type byteOnlyCache struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (c *byteOnlyCache) Get(_ context.Context, k string) ([]byte, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[k]
	return v, ok, nil
}

func (c *byteOnlyCache) Set(_ context.Context, k string, v []byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = v
	return nil
}

func (c *byteOnlyCache) Delete(_ context.Context, k string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, k)
	return nil
}

// --- helpers -----------------------------------------------------------------

func mustMemStore(t *testing.T) *kernel.MemInstanceStore {
	t.Helper()
	m, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	return m
}

func runningState(id string) engine.InstanceState {
	return engine.InstanceState{
		InstanceID: id,
		DefID:      "d",
		DefVersion: 1,
		Status:     engine.StatusRunning,
		StartedAt:  time.Unix(0, 0).UTC(),
	}
}

func startTrg() engine.Trigger { return engine.NewStartInstance(time.Unix(0, 0).UTC(), nil) }

// --- tests -------------------------------------------------------------------

func TestCachingInstanceStoreServesOwnedLoadFromCache(t *testing.T) {
	cs := &countingStore{backing: mustMemStore(t)}
	store, err := persistence.NewCachingInstanceStore(cs, kernel.AlwaysOwn{}, hotcache.New())
	require.NoError(t, err)

	id := "c1"
	_, err = store.Create(t.Context(), kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
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

func TestCachingInstanceStoreBypassesWhenNotOwned(t *testing.T) {
	cs := &countingStore{backing: mustMemStore(t)}
	store, err := persistence.NewCachingInstanceStore(cs, neverOwn{}, hotcache.New())
	require.NoError(t, err)

	id := "c2"
	_, err = store.Create(t.Context(), kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)

	// Not owned ⇒ every Load hits the backing.
	_, _, err = store.Load(t.Context(), id)
	require.NoError(t, err)
	_, _, err = store.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, int64(2), cs.loads.Load())
}

func TestCachingInstanceStoreEvictsOnConcurrentUpdate(t *testing.T) {
	mem := mustMemStore(t)
	cs := &countingStore{backing: mem}
	store, err := persistence.NewCachingInstanceStore(cs, kernel.AlwaysOwn{}, hotcache.New())
	require.NoError(t, err)

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

func TestCachingInstanceStoreConcurrentLoadCommitStayCoherent(t *testing.T) {
	mem := mustMemStore(t)
	store, err := persistence.NewCachingInstanceStore(mem, kernel.AlwaysOwn{}, hotcache.New())
	require.NoError(t, err)

	id := "race1"
	tok, err := store.Create(t.Context(), kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)

	// Hammer Load while a single Commit advances the token; the cache must never
	// serve a torn write-through (per-instance keyed serialization).
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
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

func TestCachingInstanceStoreReleaseEvicts(t *testing.T) {
	cs := &countingStore{backing: mustMemStore(t)}
	store, err := persistence.NewCachingInstanceStore(cs, kernel.AlwaysOwn{}, hotcache.New())
	require.NoError(t, err)

	id := "rel1"
	_, err = store.Create(t.Context(), kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
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

func TestNewCachingInstanceStoreDefaultLoggerNoPanic(t *testing.T) {
	// No logger option → defaults to slog.Default(); construction + a basic op must not panic.
	cs := &countingStore{backing: mustMemStore(t)}
	s, err := persistence.NewCachingInstanceStore(cs, kernel.AlwaysOwn{}, hotcache.New())
	require.NoError(t, err)
	assert.NotNil(t, s)
}

func TestNewCachingInstanceStoreWithTTLOption(t *testing.T) {
	// WithInstanceCacheTTL is accepted and the store operates normally.
	cs := &countingStore{backing: mustMemStore(t)}
	s, err := persistence.NewCachingInstanceStore(cs, kernel.AlwaysOwn{}, hotcache.New(), persistence.WithInstanceCacheTTL(time.Minute))
	require.NoError(t, err)
	assert.NotNil(t, s)

	id := "ttlopt1"
	_, err = s.Create(t.Context(), kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)

	// Load immediately — entry was write-through cached; no backing load.
	_, _, err = s.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, int64(0), cs.loads.Load(), "write-through Create must populate cache; Load must be a hit")
}

func TestNewCachingInstanceStoreFailsFast(t *testing.T) {
	t.Parallel()

	mem := mustMemStore(t)
	type testCase struct {
		name     string
		backing  kernel.InstanceStore
		owner    kernel.InstanceOwnership
		provider cache.Provider
		assert   func(t *testing.T, s *persistence.CachingInstanceStore, err error)
	}
	cases := []testCase{
		{
			name:     "nil backing",
			backing:  nil,
			owner:    kernel.AlwaysOwn{},
			provider: hotcache.New(),
			assert: func(t *testing.T, s *persistence.CachingInstanceStore, err error) {
				require.ErrorIs(t, err, kernel.ErrNilDependency)
				require.Nil(t, s)
			},
		},
		{
			name:     "nil owner",
			backing:  mem,
			owner:    nil,
			provider: hotcache.New(),
			assert: func(t *testing.T, s *persistence.CachingInstanceStore, err error) {
				require.ErrorIs(t, err, kernel.ErrNilDependency)
				require.Nil(t, s)
			},
		},
		{
			name:     "nil provider",
			backing:  mem,
			owner:    kernel.AlwaysOwn{},
			provider: nil,
			assert: func(t *testing.T, s *persistence.CachingInstanceStore, err error) {
				require.ErrorIs(t, err, kernel.ErrNilDependency)
				require.Nil(t, s)
			},
		},
		{
			name:     "valid args",
			backing:  mem,
			owner:    kernel.AlwaysOwn{},
			provider: hotcache.New(),
			assert: func(t *testing.T, s *persistence.CachingInstanceStore, err error) {
				require.NoError(t, err)
				require.NotNil(t, s)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, err := persistence.NewCachingInstanceStore(tc.backing, tc.owner, tc.provider)
			tc.assert(t, s, err)
		})
	}
}

func TestNewCachingInstanceStoreAlwaysOwnWarning(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		owner  kernel.InstanceOwnership
		assert func(t *testing.T, logged string)
	}

	cases := []testCase{
		{
			name:  "AlwaysOwn emits a single-replica warning",
			owner: kernel.AlwaysOwn{},
			assert: func(t *testing.T, logged string) {
				assert.Contains(t, strings.ToLower(logged), "single")
				assert.Contains(t, logged, "AlwaysOwn")
			},
		},
		{
			name:  "a real ownership emits no warning",
			owner: stubOwnership{},
			assert: func(t *testing.T, logged string) {
				assert.Empty(t, logged)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf syncBuffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

			_, err := persistence.NewCachingInstanceStore(
				mustMemStore(t),
				tc.owner,
				hotcache.New(),
				persistence.WithInstanceCacheLogger(logger),
			)
			require.NoError(t, err)

			tc.assert(t, buf.String())
		})
	}
}

// assertCreateLoadCommit exercises the create → load(from cache) → commit flow
// against a store and asserts cache-served correctness.
func assertCreateLoadCommit(t *testing.T, store *persistence.CachingInstanceStore, cs *countingStore) {
	t.Helper()
	id := "sub1"
	tok, err := store.Create(t.Context(), kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)

	// Owned Load is served from cache — no backing Load.
	st, ltok, err := store.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, id, st.InstanceID)
	assert.Equal(t, tok, ltok)
	assert.Equal(t, int64(0), cs.loads.Load(), "owned Load must be served from cache")

	// Commit advances the token and refreshes the cache.
	next, err := store.Commit(t.Context(), tok, kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)

	st2, ltok2, err := store.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, id, st2.InstanceID)
	assert.Equal(t, next, ltok2)
	assert.Equal(t, int64(0), cs.loads.Load(), "post-commit Load must still be served from cache")
}

func TestCachingInstanceStoreEntriesDelegatesToJournalReader(t *testing.T) {
	mem := mustMemStore(t) // MemInstanceStore is a JournalReader
	store, err := persistence.NewCachingInstanceStore(mem, kernel.AlwaysOwn{}, hotcache.New())
	require.NoError(t, err)

	id := "j1"
	_, err = store.Create(t.Context(), kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)

	entries, err := store.Entries(t.Context(), id)
	require.NoError(t, err)
	require.Len(t, entries, 1, "journal must reflect the create trigger")
}

func TestCachingInstanceStoreEntriesErrorsWhenBackingNotReader(t *testing.T) {
	// countingStore wraps a backing but does NOT expose a JournalReader itself.
	cs := &countingStore{backing: mustMemStore(t)}
	store, err := persistence.NewCachingInstanceStore(cs, kernel.AlwaysOwn{}, hotcache.New())
	require.NoError(t, err)

	_, err = store.Entries(t.Context(), "nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JournalReader")
}

func TestCachingInstanceStore_Substrates(t *testing.T) {
	providers := map[string]func() cache.Provider{
		"hotcache-value": func() cache.Provider { return hotcache.New() },
		"byte-only":      func() cache.Provider { return newByteOnlyProvider() }, // JSON path
	}
	for name, np := range providers {
		t.Run(name, func(t *testing.T) {
			cs := &countingStore{backing: mustMemStore(t)}
			store, err := persistence.NewCachingInstanceStore(cs, kernel.AlwaysOwn{}, np())
			require.NoError(t, err)
			assertCreateLoadCommit(t, store, cs)
		})
	}
}
