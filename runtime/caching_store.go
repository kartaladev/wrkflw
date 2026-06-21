package runtime

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// Compile-time assertions.
var (
	_ Store         = (*CachingStore)(nil)
	_ JournalReader = (*CachingStore)(nil)
)

const (
	defaultCacheTTL        = 5 * time.Minute
	defaultCacheMaxEntries = 1024
)

// CachingStore is a write-through, single-writer cache in front of a Store
// (ADR-0020). It is correct ONLY when each cached instance has exactly one
// writing process, which the Ownership port guarantees: only owned instances are
// cached/served; a non-owned instance bypasses the cache and reads the backing
// Store every time. The cache is bounded (LRU on entry count + TTL) and evicts an
// entry on ErrConcurrentUpdate (a stale token). Per-instance keyed serialization
// keeps the cache coherent under concurrent Load/Commit for the same instance.
type CachingStore struct {
	backing    Store
	owner      Ownership
	clk        clock.Clock
	ttl        time.Duration
	maxEntries int

	mu      sync.Mutex
	entries map[string]*cacheNode
	lru     *list.List // front = most recently used; Value = *cacheNode

	klMu     sync.Mutex
	keyLocks map[string]*keyLock
}

type cacheNode struct {
	id        string
	state     engine.InstanceState
	token     Token
	expiresAt time.Time // zero when ttl <= 0 (never expires)
	elem      *list.Element
}

type keyLock struct {
	mu   sync.Mutex
	refs int
}

// CachingStoreOption configures a CachingStore.
type CachingStoreOption func(*CachingStore)

// WithCacheTTL sets the maximum age of a cached instance entry before it is
// reloaded from the backing Store. <= 0 disables TTL expiry. Default: 5m.
func WithCacheTTL(d time.Duration) CachingStoreOption { return func(c *CachingStore) { c.ttl = d } }

// WithCacheMaxEntries caps the number of cached instances (LRU eviction beyond
// the cap). <= 0 means unbounded. Default: 1024.
func WithCacheMaxEntries(n int) CachingStoreOption {
	return func(c *CachingStore) { c.maxEntries = n }
}

// NewCachingStore wraps backing with a single-writer, write-through cache gated
// by owner. clk drives TTL (use clock.System() in production, a fake clock in
// tests).
func NewCachingStore(backing Store, owner Ownership, clk clock.Clock, opts ...CachingStoreOption) *CachingStore {
	c := &CachingStore{
		backing:    backing,
		owner:      owner,
		clk:        clk,
		ttl:        defaultCacheTTL,
		maxEntries: defaultCacheMaxEntries,
		entries:    make(map[string]*cacheNode),
		lru:        list.New(),
		keyLocks:   make(map[string]*keyLock),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// lockFor returns an unlock func after taking a refcounted per-instance lock.
func (c *CachingStore) lockFor(id string) func() {
	c.klMu.Lock()
	kl := c.keyLocks[id]
	if kl == nil {
		kl = &keyLock{}
		c.keyLocks[id] = kl
	}
	kl.refs++
	c.klMu.Unlock()

	kl.mu.Lock()
	return func() {
		kl.mu.Unlock()
		c.klMu.Lock()
		kl.refs--
		if kl.refs == 0 {
			delete(c.keyLocks, id)
		}
		c.klMu.Unlock()
	}
}

// get returns a fresh cached node (moving it to the LRU front) or false.
func (c *CachingStore) get(id string) (*cacheNode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n, ok := c.entries[id]
	if !ok {
		return nil, false
	}
	if c.ttl > 0 && !c.clk.Now().Before(n.expiresAt) {
		c.removeLocked(n) // expired
		return nil, false
	}
	c.lru.MoveToFront(n.elem)
	return n, true
}

// put upserts an entry, refreshing TTL and evicting the LRU tail if over cap.
func (c *CachingStore) put(id string, state engine.InstanceState, token Token) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var exp time.Time
	if c.ttl > 0 {
		exp = c.clk.Now().Add(c.ttl)
	}
	if n, ok := c.entries[id]; ok {
		n.state, n.token, n.expiresAt = state, token, exp
		c.lru.MoveToFront(n.elem)
		return
	}
	n := &cacheNode{id: id, state: state, token: token, expiresAt: exp}
	n.elem = c.lru.PushFront(n)
	c.entries[id] = n
	if c.maxEntries > 0 {
		for c.lru.Len() > c.maxEntries {
			c.removeLocked(c.lru.Back().Value.(*cacheNode))
		}
	}
}

func (c *CachingStore) evict(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n, ok := c.entries[id]; ok {
		c.removeLocked(n)
	}
}

// removeLocked drops a node; caller holds c.mu.
func (c *CachingStore) removeLocked(n *cacheNode) {
	c.lru.Remove(n.elem)
	delete(c.entries, n.id)
}

// Create delegates to the backing Store, then write-through caches the new state
// when this process owns the instance.
func (c *CachingStore) Create(ctx context.Context, step AppliedStep) (Token, error) {
	tok, err := c.backing.Create(ctx, step)
	if err != nil {
		return 0, err
	}
	id := step.State.InstanceID
	if owned, oerr := c.owner.Acquire(ctx, id); oerr == nil && owned {
		// Hold the per-instance keyed lock around the write-through, for the
		// same invariant Load/Commit rely on: every cache write for an id is
		// serialized so a concurrent Load-populate cannot interleave.
		unlock := c.lockFor(id)
		c.put(id, step.State.Clone(), tok)
		unlock()
	}
	return tok, nil
}

// Load serves owned instances from cache (populating on a miss under the
// per-instance lock so a concurrent Commit cannot interleave a stale write).
// Non-owned instances bypass the cache entirely.
func (c *CachingStore) Load(ctx context.Context, id string) (engine.InstanceState, Token, error) {
	owned, err := c.owner.Acquire(ctx, id)
	if err != nil || !owned {
		return c.backing.Load(ctx, id) // bypass; do not populate
	}
	unlock := c.lockFor(id)
	defer unlock()
	if n, ok := c.get(id); ok {
		return n.state.Clone(), n.token, nil
	}
	st, tok, lerr := c.backing.Load(ctx, id)
	if lerr != nil {
		return engine.InstanceState{}, 0, lerr
	}
	c.put(id, st.Clone(), tok)
	return st, tok, nil
}

// Commit delegates under the per-instance lock; on success it write-through
// caches the new state, on ErrConcurrentUpdate it evicts the stale entry.
func (c *CachingStore) Commit(ctx context.Context, expected Token, step AppliedStep) (Token, error) {
	id := step.State.InstanceID
	unlock := c.lockFor(id)
	defer unlock()
	tok, err := c.backing.Commit(ctx, expected, step)
	if err != nil {
		if errors.Is(err, ErrConcurrentUpdate) {
			c.evict(id)
		}
		return 0, err
	}
	if owned, oerr := c.owner.Acquire(ctx, id); oerr == nil && owned {
		c.put(id, step.State.Clone(), tok)
	}
	return tok, nil
}

// Release relinquishes ownership of an instance and evicts its cached state,
// so a future re-acquisition re-reads the backing Store rather than serving a
// now-possibly-stale cached entry. Consumers using a CachingStore MUST relinquish
// ownership through THIS method (not the bare Ownership), or a re-acquired
// instance may serve stale state until its TTL expires (ADR-0020).
//
// The eviction is performed before forwarding to owner.Release so the cache entry
// is gone even if the underlying Release call errors.
func (c *CachingStore) Release(ctx context.Context, id string) error {
	c.evict(id) // evict FIRST so the entry is gone even if owner.Release errors
	return c.owner.Release(ctx, id)
}

// Entries forwards to the backing Store's JournalReader if it implements one;
// the journal is never cached. Returns an error if the backing is not a reader.
func (c *CachingStore) Entries(ctx context.Context, id string) ([]engine.Trigger, error) {
	jr, ok := c.backing.(JournalReader)
	if !ok {
		return nil, errors.New("runtime: backing store is not a JournalReader")
	}
	return jr.Entries(ctx, id)
}
