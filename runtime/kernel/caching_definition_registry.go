package kernel

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

// Compile-time assertion: CachingDefinitionRegistry satisfies DefinitionRegistry.
var _ DefinitionRegistry = (*CachingDefinitionRegistry)(nil)

// cacheEntry holds a cached definition and the wall-clock time at which it expires.
type cacheEntry struct {
	def       *model.ProcessDefinition
	expiresAt time.Time
}

// CachingDefinitionRegistry is a read-through, TTL-bounded, single-flight cache
// in front of any DefinitionRegistry.
//
// Correctness guarantees:
//   - A cache hit (within TTL) never calls the backing registry.
//   - Concurrent misses for the same Qualifier collapse to exactly one backing call
//     via [singleflight.Group].
//   - Error responses (including [ErrDefinitionNotFound]) are NOT cached so that
//     transient failures do not persist beyond the next call.
//   - TTL is measured using the injected [clock.Clock] (per ADR-0003); callers
//     may pass a fake clock in tests to advance time deterministically.
//
// Definitions are immutable per (defID, version), so caching them without
// invalidation is safe. The only eviction mechanism is TTL expiry.
type CachingDefinitionRegistry struct {
	backing DefinitionRegistry
	ttl     time.Duration
	clk     clock.Clock

	mu      sync.Mutex
	entries map[string]cacheEntry
	group   singleflight.Group
}

// CachingDefinitionRegistryOption configures a [CachingDefinitionRegistry].
type CachingDefinitionRegistryOption func(*CachingDefinitionRegistry)

// WithCachingDefinitionRegistryClock sets the time source used to evaluate TTL.
// Default: [clock.System]. A nil clock is ignored. Inject a fake clock in tests.
func WithCachingDefinitionRegistryClock(clk clock.Clock) CachingDefinitionRegistryOption {
	return func(c *CachingDefinitionRegistry) {
		if clk != nil {
			c.clk = clk
		}
	}
}

// NewCachingDefinitionRegistry wraps backing with a TTL-bounded, single-flight
// read-through cache. ttl is the maximum age of a cached definition. The time
// source used to evaluate TTL defaults to [clock.System]; override it with
// [WithCachingDefinitionRegistryClock] (a fake clock in tests).
func NewCachingDefinitionRegistry(backing DefinitionRegistry, ttl time.Duration, opts ...CachingDefinitionRegistryOption) (*CachingDefinitionRegistry, error) {
	if backing == nil {
		return nil, fmt.Errorf("%w: backing registry", ErrNilDependency)
	}
	c := &CachingDefinitionRegistry{
		backing: backing,
		ttl:     ttl,
		clk:     clock.System(),
		entries: make(map[string]cacheEntry),
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// Lookup returns the ProcessDefinition for q. On a cache miss (or after
// TTL expiry) the backing registry is consulted exactly once per key (concurrent
// callers share the same in-flight request via singleflight). Errors from the
// backing registry are returned as-is and never cached.
//
// The internal cache is keyed on q.String() (a stable string representation of
// the Qualifier) to satisfy singleflight's string-key requirement.
func (c *CachingDefinitionRegistry) Lookup(ctx context.Context, q model.Qualifier) (*model.ProcessDefinition, error) {
	key := q.String()
	now := c.clk.Now()

	// Fast path: cache hit within TTL — no lock contention on the singleflight group.
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && now.Before(e.expiresAt) {
		c.mu.Unlock()
		return e.def, nil
	}
	c.mu.Unlock()

	// Slow path: single-flight to ensure exactly one backing call per key.
	v, err, _ := c.group.Do(key, func() (any, error) {
		// Double-check the cache inside the flight. The fast-path check above and
		// this Do are not atomic: a prior flight for this key may have completed
		// (populating the cache and freeing the singleflight key) in the window
		// between a straggler's fast-path miss and its arrival here. Re-checking
		// collapses that straggler onto the cached value instead of issuing a
		// redundant backing call — making single-call suppression robust to
		// goroutine-scheduling skew, not just to strictly-overlapping flights.
		now := c.clk.Now()
		c.mu.Lock()
		if e, ok := c.entries[key]; ok && now.Before(e.expiresAt) {
			c.mu.Unlock()
			return e.def, nil
		}
		c.mu.Unlock()

		def, err := c.backing.Lookup(ctx, q)
		if err != nil {
			// Do NOT cache errors — let the next caller retry the backing.
			return nil, err
		}
		c.mu.Lock()
		c.entries[key] = cacheEntry{def: def, expiresAt: c.clk.Now().Add(c.ttl)}
		c.mu.Unlock()
		return def, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*model.ProcessDefinition), nil
}

// ListDefinitions implements [DefinitionLister] by delegating to the backing
// registry when it also implements DefinitionLister. It never enumerates the
// cache's own entries map — that cache is a partial, TTL-bounded set of
// recently looked-up qualifiers, not the full registered set, so it would
// silently under-report. Returns nil when the backing registry does not
// implement DefinitionLister.
func (c *CachingDefinitionRegistry) ListDefinitions(ctx context.Context) []*model.ProcessDefinition {
	lister, ok := c.backing.(DefinitionLister)
	if !ok {
		return nil
	}
	return lister.ListDefinitions(ctx)
}

// Compile-time assertion: CachingDefinitionRegistry satisfies DefinitionLister.
var _ DefinitionLister = (*CachingDefinitionRegistry)(nil)
