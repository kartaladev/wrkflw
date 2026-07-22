package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/persistence/cache"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// Compile-time assertions.
var (
	_ kernel.InstanceStore = (*CachingInstanceStore)(nil)
	_ kernel.JournalReader = (*CachingInstanceStore)(nil)
	_ kernel.TxRunner      = (*CachingInstanceStore)(nil)
)

const defaultInstanceCacheTTL = 5 * time.Minute

// txTouched collects the instance ids write-through cached (via put) while a
// RunInTx's fn is running, so a rollback can evict exactly those entries.
// Safe for concurrent use: fn may call wrapper methods from multiple
// goroutines against the same txCtx.
type txTouched struct {
	mu  sync.Mutex
	ids map[string]struct{}
}

func newTxTouched() *txTouched { return &txTouched{ids: make(map[string]struct{})} }

func (tt *txTouched) mark(id string) {
	tt.mu.Lock()
	tt.ids[id] = struct{}{}
	tt.mu.Unlock()
}

func (tt *txTouched) snapshot() []string {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	ids := make([]string, 0, len(tt.ids))
	for id := range tt.ids {
		ids = append(ids, id)
	}
	return ids
}

// txTouchedKey is the context key under which RunInTx stashes a *txTouched for
// the duration of one fn call. Unexported so only this file can populate it.
type txTouchedKey struct{}

func withTxTouched(ctx context.Context, tt *txTouched) context.Context {
	return context.WithValue(ctx, txTouchedKey{}, tt)
}

func txTouchedFrom(ctx context.Context) *txTouched {
	tt, _ := ctx.Value(txTouchedKey{}).(*txTouched)
	return tt
}

// instanceEntry is the cached unit: the snapshot plus its optimistic version.
type instanceEntry struct {
	State   engine.InstanceState `json:"state"`
	Version kernel.Version       `json:"version"`
}

// cloneInstanceEntry deep-copies an entry so cached live values (value-cache
// substrates) can never be aliased by a caller.
func cloneInstanceEntry(e instanceEntry) instanceEntry {
	return instanceEntry{State: e.State.Clone(), Version: e.Version}
}

// unmarshalInstanceEntry decodes an entry from the byte-oriented substrate path.
func unmarshalInstanceEntry(b []byte) (instanceEntry, error) {
	var e instanceEntry
	return e, json.Unmarshal(b, &e)
}

// CachingInstanceStore is a write-through, ownership-gated cache in front of a
// durable [kernel.InstanceStore] (ADR-0020). It is correct ONLY when each cached
// instance has exactly one writing process, which the [kernel.InstanceOwnership]
// port guarantees: only owned instances are cached/served; a non-owned instance
// bypasses the cache and reads the backing store every time. The cache evicts an
// entry on [kernel.ErrConcurrentUpdate] (a stale version). Per-instance keyed
// serialization keeps the cache coherent under concurrent Load/Commit for the
// same instance.
//
// Storage is delegated to a [cache.Provider] substrate (in-memory by default via
// persistence/cache/hotcache). Capacity and TTL expiry are the substrate's
// responsibility; this store only supplies a per-Set TTL hint.
//
// # Multi-replica safety (READ THIS)
//
// Pairing a CachingInstanceStore with [kernel.AlwaysOwn] is SINGLE-WRITER /
// SINGLE-REPLICA ONLY. AlwaysOwn unconditionally reports ownership, so two
// replicas both caching the same instance would each serve their own stale
// snapshot and could fire a routing decision and its side-effects before the
// version-CAS rejected the write — a stale-read footgun (ADR-0020, ADR-0054).
// For ANY multi-replica deployment use a real lease —
// [persistence.NewAdvisoryLockOwnership] — so only the owning replica caches an
// instance. As a guard, [NewCachingInstanceStore] logs a one-time Warn when it is
// constructed with AlwaysOwn.
type CachingInstanceStore struct {
	backing kernel.InstanceStore
	owner   kernel.InstanceOwnership
	codec   *cache.Codec[instanceEntry]
	logger  *slog.Logger
	ttl     time.Duration

	klMu     sync.Mutex
	keyLocks map[string]*keyLock
}

type keyLock struct {
	mu   sync.Mutex
	refs int
}

// CachingInstanceStoreOption configures a CachingInstanceStore.
type CachingInstanceStoreOption func(*CachingInstanceStore)

// WithInstanceCacheTTL sets the max age hint passed to the cache substrate for a
// cached snapshot. <= 0 is ignored. Default: 5m.
func WithInstanceCacheTTL(d time.Duration) CachingInstanceStoreOption {
	return func(c *CachingInstanceStore) {
		if d > 0 {
			c.ttl = d
		}
	}
}

// WithInstanceCacheLogger sets the structured logger used for the one-time
// AlwaysOwn single-replica warning emitted at construction. Default:
// slog.Default(). A nil value is ignored.
func WithInstanceCacheLogger(l *slog.Logger) CachingInstanceStoreOption {
	return func(c *CachingInstanceStore) {
		if l != nil {
			c.logger = l
		}
	}
}

// NewCachingInstanceStore wraps backing with an ownership-gated, write-through
// cache whose storage comes from provider.Cache("instances"). It fails fast with
// [kernel.ErrNilDependency] when any required dependency is nil.
func NewCachingInstanceStore(backing kernel.InstanceStore, owner kernel.InstanceOwnership, provider cache.Provider, opts ...CachingInstanceStoreOption) (*CachingInstanceStore, error) {
	if backing == nil {
		return nil, fmt.Errorf("%w: backing store", kernel.ErrNilDependency)
	}
	if owner == nil {
		return nil, fmt.Errorf("%w: owner", kernel.ErrNilDependency)
	}
	if provider == nil {
		return nil, fmt.Errorf("%w: cache provider", kernel.ErrNilDependency)
	}
	raw, err := provider.Cache("instances")
	if err != nil {
		return nil, err
	}
	codec, err := cache.NewCodec[instanceEntry](
		raw,
		func(e instanceEntry) ([]byte, error) { return json.Marshal(e) },
		unmarshalInstanceEntry,
		cloneInstanceEntry,
	)
	if err != nil {
		return nil, err
	}
	c := &CachingInstanceStore{
		backing:  backing,
		owner:    owner,
		codec:    codec,
		logger:   slog.Default(),
		ttl:      defaultInstanceCacheTTL,
		keyLocks: make(map[string]*keyLock),
	}
	for _, o := range opts {
		o(c)
	}
	// Single-replica footgun guard (ADR-0054): AlwaysOwn unconditionally grants
	// ownership, so caching under it is correct only when this process is the sole
	// writer. Warn once at construction so a multi-replica misconfiguration is
	// visible in logs; the safe alternative is a real lease
	// (persistence.NewAdvisoryLockOwnership).
	if _, ok := owner.(kernel.AlwaysOwn); ok {
		c.logger.Warn("persistence: CachingInstanceStore paired with AlwaysOwn is single-replica only; " +
			"use persistence.NewAdvisoryLockOwnership for multi-replica deployments to avoid stale cached reads")
	}
	return c, nil
}

// lockFor returns an unlock func after taking a refcounted per-instance lock.
func (c *CachingInstanceStore) lockFor(id string) func() {
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

// put write-through caches state under id at the given version. The codec owns
// isolation (it deep-clones on the value path and marshals on the byte path), so
// callers may pass their own live state without a defensive copy.
//
// When ctx carries a *txTouched (i.e. this write happened during a RunInTx's
// fn), the id is recorded so a subsequent rollback can evict it — a put made
// while a joined write's own Commit is a no-op (the outer RunInTx owns the
// real commit/rollback decision) would otherwise poison the cache with a value
// the database never durably committed (ADR-0134 Task 8 audit BLOCKER).
func (c *CachingInstanceStore) put(ctx context.Context, id string, state engine.InstanceState, version kernel.Version) {
	_ = c.codec.Set(ctx, id, instanceEntry{State: state, Version: version}, c.ttl)
	if tt := txTouchedFrom(ctx); tt != nil {
		tt.mark(id)
	}
}

// evict removes the cached entry for id.
func (c *CachingInstanceStore) evict(ctx context.Context, id string) {
	_ = c.codec.Delete(ctx, id)
}

// Create delegates to the backing store, then write-through caches the new state
// when this process owns the instance.
func (c *CachingInstanceStore) Create(ctx context.Context, step kernel.AppliedStep) (kernel.Version, error) {
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
		c.put(ctx, id, step.State, tok)
		unlock()
	}
	return tok, nil
}

// Load serves owned instances from cache (populating on a miss under the
// per-instance lock so a concurrent Commit cannot interleave a stale write).
// Non-owned instances bypass the cache entirely.
func (c *CachingInstanceStore) Load(ctx context.Context, id string) (engine.InstanceState, kernel.Version, error) {
	owned, err := c.owner.Acquire(ctx, id)
	if err != nil || !owned {
		return c.backing.Load(ctx, id) // bypass; do not populate
	}
	unlock := c.lockFor(id)
	defer unlock()
	if e, ok, gerr := c.codec.Get(ctx, id); gerr == nil && ok {
		// codec.Get already returns an isolated clone (value path) or a fresh
		// unmarshal (byte path); no further copy is needed here.
		return e.State, e.Version, nil
	}
	st, tok, lerr := c.backing.Load(ctx, id)
	if lerr != nil {
		return engine.InstanceState{}, 0, lerr
	}
	// codec.Set (via put) clones internally on the value path, so caching st does
	// not alias the value we return to the caller.
	c.put(ctx, id, st, tok)
	return st, tok, nil
}

// Commit delegates under the per-instance lock; on success it write-through
// caches the new state, on ErrConcurrentUpdate it evicts the stale entry.
func (c *CachingInstanceStore) Commit(ctx context.Context, expected kernel.Version, step kernel.AppliedStep) (kernel.Version, error) {
	id := step.State.InstanceID
	unlock := c.lockFor(id)
	defer unlock()
	tok, err := c.backing.Commit(ctx, expected, step)
	if err != nil {
		if errors.Is(err, kernel.ErrConcurrentUpdate) {
			c.evict(ctx, id)
		}
		return 0, err
	}
	if owned, oerr := c.owner.Acquire(ctx, id); oerr == nil && owned {
		c.put(ctx, id, step.State, tok)
	}
	return tok, nil
}

// Release relinquishes ownership of an instance and evicts its cached state, so a
// future re-acquisition re-reads the backing store rather than serving a
// now-possibly-stale cached entry. Consumers using a CachingInstanceStore MUST
// relinquish ownership through THIS method (not the bare
// [kernel.InstanceOwnership]), or a re-acquired instance may serve stale state
// until its TTL expires (ADR-0020).
//
// The eviction is performed before forwarding to owner.Release so the cache entry
// is gone even if the underlying Release call errors.
func (c *CachingInstanceStore) Release(ctx context.Context, id string) error {
	c.evict(ctx, id) // evict FIRST so the entry is gone even if owner.Release errors
	return c.owner.Release(ctx, id)
}

// Entries forwards to the backing store's [kernel.JournalReader] if it implements
// one; the journal is never cached. Returns an error if the backing is not a
// reader.
func (c *CachingInstanceStore) Entries(ctx context.Context, id string) ([]engine.Trigger, error) {
	jr, ok := c.backing.(kernel.JournalReader)
	if !ok {
		return nil, errors.New("workflow-persistence: backing store is not a JournalReader")
	}
	return jr.Entries(ctx, id)
}

// RunInTx forwards the [kernel.TxRunner] capability to the backing store when
// it has one (ADR-0134). Without it, RunInTx degrades to a bare fn(ctx) call —
// the same sequencing-only contract [kernel.MemInstanceStore.RunInTx] offers,
// so a consumer wiring the wrapper over a non-transactional backing still gets
// a compiling, non-panicking call.
//
// With the capability: every Create/Commit the wrapper serves during fn
// write-through caches optimistically, before the outer unit actually commits
// (a joined write's own Commit is a no-op — see internal/database/transaction
// — the owner controls the real decision). If fn (or the transaction) later
// fails, those optimistic cache entries are exactly a poisoned view of state
// the database never durably persisted. RunInTx tracks every id [put] touches
// during fn via a context-scoped [txTouched] and evicts each one unless the
// inner call returned nil — never leaving a cached in-tx value behind. On
// success no eviction happens; the already-cached values are correct as-is.
//
// The eviction runs in a defer gated on a success flag, so it fires on the
// error return path AND when fn panics: the inner store's own defer rolls the
// database back either way, but only this defer guarantees the wrapper's
// cache doesn't keep serving the poisoned in-tx value to a caller that
// recovers upstream. The defer never recovers — a panic in fn propagates out
// of RunInTx unchanged, after eviction has already run.
func (c *CachingInstanceStore) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	runner, ok := c.backing.(kernel.TxRunner)
	if !ok {
		return fn(ctx) // inner store without the capability: degraded, same as direct use
	}

	tt := newTxTouched()
	succeeded := false
	defer func() {
		if succeeded {
			return
		}
		for _, id := range tt.snapshot() {
			c.evict(ctx, id)
		}
	}()

	err := runner.RunInTx(ctx, func(txCtx context.Context) error {
		return fn(withTxTouched(txCtx, tt))
	})
	succeeded = err == nil
	return err
}
