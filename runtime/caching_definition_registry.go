package runtime

import (
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/model"
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
//   - Concurrent misses for the same DefRef collapse to exactly one backing call
//     via [singleflight.Group].
//   - Error responses (including [ErrDefinitionNotFound]) are NOT cached so that
//     transient failures do not persist beyond the next call.
//   - TTL is measured using the injected [clock.Clock] (per ADR-0003); callers
//     may pass a fake clock in tests to advance time deterministically.
//
// Definitions are immutable per (defID, version), so caching them without
// invalidation is safe (spec §6). The only eviction mechanism is TTL expiry.
type CachingDefinitionRegistry struct {
	backing DefinitionRegistry
	ttl     time.Duration
	clk     clock.Clock

	mu      sync.Mutex
	entries map[string]cacheEntry
	group   singleflight.Group
}

// NewCachingDefinitionRegistry wraps backing with a TTL-bounded, single-flight
// read-through cache. ttl is the maximum age of a cached definition; clk is
// the time source used to evaluate TTL (use [clock.System] in production,
// a fake clock in tests).
func NewCachingDefinitionRegistry(backing DefinitionRegistry, ttl time.Duration, clk clock.Clock) *CachingDefinitionRegistry {
	return &CachingDefinitionRegistry{
		backing: backing,
		ttl:     ttl,
		clk:     clk,
		entries: make(map[string]cacheEntry),
	}
}

// Lookup returns the ProcessDefinition for defRef. On a cache miss (or after
// TTL expiry) the backing registry is consulted exactly once per key (concurrent
// callers share the same in-flight request via singleflight). Errors from the
// backing registry are returned as-is and never cached.
func (c *CachingDefinitionRegistry) Lookup(defRef string) (*model.ProcessDefinition, error) {
	now := c.clk.Now()

	// Fast path: cache hit within TTL — no lock contention on the singleflight group.
	c.mu.Lock()
	if e, ok := c.entries[defRef]; ok && now.Before(e.expiresAt) {
		c.mu.Unlock()
		return e.def, nil
	}
	c.mu.Unlock()

	// Slow path: single-flight to ensure exactly one backing call per key.
	v, err, _ := c.group.Do(defRef, func() (any, error) {
		def, err := c.backing.Lookup(defRef)
		if err != nil {
			// Do NOT cache errors — let the next caller retry the backing.
			return nil, err
		}
		c.mu.Lock()
		c.entries[defRef] = cacheEntry{def: def, expiresAt: c.clk.Now().Add(c.ttl)}
		c.mu.Unlock()
		return def, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*model.ProcessDefinition), nil
}
