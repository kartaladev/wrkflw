// Package hotcache is the default in-memory cache.Provider backed by
// github.com/samber/hot. Its caches implement cache.ValueCache, so the persistence
// Codec stores live values without serialization.
package hotcache

import (
	"context"
	"sync"
	"time"

	"github.com/samber/hot"

	"github.com/zakyalvan/krtlwrkflw/persistence/cache"
)

const (
	defaultCapacity = 1024
	defaultTTL      = 5 * time.Minute
)

// Compile-time interface conformance checks.
var (
	_ cache.Cache      = (*hotCache)(nil)
	_ cache.ValueCache = (*hotCache)(nil)
)

// Option configures the provider.
type Option func(*provider)

// WithCapacity caps entries per namespace (LRU eviction beyond it). Default 1024.
func WithCapacity(n int) Option {
	return func(p *provider) {
		if n > 0 {
			p.capacity = n
		}
	}
}

// WithTTL sets the default entry TTL. Default 5m. Per-call ttl on Set overrides it.
func WithTTL(d time.Duration) Option {
	return func(p *provider) {
		if d > 0 {
			p.ttl = d
		}
	}
}

// New returns an in-memory cache.Provider. Each namespace gets an independent
// bounded cache.
func New(opts ...Option) cache.Provider {
	p := &provider{
		capacity: defaultCapacity,
		ttl:      defaultTTL,
		caches:   map[string]*hotCache{},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

type provider struct {
	mu       sync.Mutex
	capacity int
	ttl      time.Duration
	caches   map[string]*hotCache
}

// Cache returns (or lazily creates) the cache for the given namespace.
func (p *provider) Cache(ns string) (cache.Cache, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.caches[ns]; ok {
		return c, nil
	}
	hc := hot.NewHotCache[string, any](hot.LRU, p.capacity).
		WithTTL(p.ttl).
		Build()
	c := &hotCache{
		def: p.ttl,
		hc:  hc,
	}
	p.caches[ns] = c
	return c, nil
}

// hotCache implements cache.Cache and cache.ValueCache over one hot.HotCache.
type hotCache struct {
	def time.Duration
	hc  *hot.HotCache[string, any]
}

// Get implements cache.Cache.
func (c *hotCache) Get(_ context.Context, k string) ([]byte, bool, error) {
	v, found, err := c.hc.Get(k)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	b, _ := v.([]byte)
	return b, true, nil
}

// Set implements cache.Cache.
func (c *hotCache) Set(_ context.Context, k string, v []byte, ttl time.Duration) error {
	c.hc.SetWithTTL(k, any(v), c.pick(ttl))
	return nil
}

// GetValue implements cache.ValueCache. The returned value's dynamic type
// matches whatever was stored — including []byte if the entry was written via
// the byte-path Set.
func (c *hotCache) GetValue(_ context.Context, k string) (any, bool, error) {
	return c.hc.Get(k)
}

// SetValue implements cache.ValueCache.
func (c *hotCache) SetValue(_ context.Context, k string, v any, ttl time.Duration) error {
	c.hc.SetWithTTL(k, v, c.pick(ttl))
	return nil
}

// Delete implements cache.Cache and cache.ValueCache.
func (c *hotCache) Delete(_ context.Context, k string) error {
	c.hc.Delete(k)
	return nil
}

func (c *hotCache) pick(ttl time.Duration) time.Duration {
	if ttl > 0 {
		return ttl
	}
	return c.def
}
