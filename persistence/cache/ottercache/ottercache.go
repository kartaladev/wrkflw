// Package ottercache is an in-memory cache.Provider backed by
// github.com/maypok86/otter/v2 (S3-FIFO + W-TinyLFU eviction policy). Its
// caches implement cache.ValueCache so the persistence Codec avoids
// serialization on the hot path.
package ottercache

import (
	"context"
	"sync"
	"time"

	"github.com/maypok86/otter/v2"

	"github.com/zakyalvan/krtlwrkflw/persistence/cache"
)

// Compile-time interface conformance checks.
var (
	_ cache.Cache      = (*otterCache)(nil)
	_ cache.ValueCache = (*otterCache)(nil)
)

const (
	defaultCapacity = 1024
	defaultTTL      = 5 * time.Minute
)

// Option configures the provider.
type Option func(*provider)

// WithCapacity sets the maximum entries per namespace cache. Values <= 0 are
// ignored and the default of 1024 is kept.
func WithCapacity(n int) Option {
	return func(p *provider) {
		if n > 0 {
			p.capacity = n
		}
	}
}

// WithTTL sets the default entry TTL (expire-after-write). Values <= 0 are
// ignored and the default of 5 minutes is kept. Per-call ttl on Set/SetValue
// overrides it.
func WithTTL(d time.Duration) Option {
	return func(p *provider) {
		if d > 0 {
			p.ttl = d
		}
	}
}

// New returns an in-memory cache.Provider backed by otter. Each namespace gets
// an independent bounded cache.
func New(opts ...Option) cache.Provider {
	p := &provider{
		capacity: defaultCapacity,
		ttl:      defaultTTL,
		caches:   map[string]*otterCache{},
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
	caches   map[string]*otterCache
}

// Cache returns (or lazily creates) the cache for the given namespace.
func (p *provider) Cache(ns string) (cache.Cache, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.caches[ns]; ok {
		return c, nil
	}
	oc := otter.Must(&otter.Options[string, any]{
		MaximumSize:      p.capacity,
		ExpiryCalculator: otter.ExpiryWriting[string, any](p.ttl),
	})
	c := &otterCache{def: p.ttl, oc: oc}
	p.caches[ns] = c
	return c, nil
}

// otterCache implements cache.Cache and cache.ValueCache over one otter.Cache.
type otterCache struct {
	def time.Duration
	oc  *otter.Cache[string, any]
}

// Get implements cache.Cache. On miss returns (nil, false, nil); otter has no
// error return from GetIfPresent, so errors are never produced.
func (c *otterCache) Get(_ context.Context, k string) ([]byte, bool, error) {
	v, ok := c.oc.GetIfPresent(k)
	if !ok {
		return nil, false, nil
	}
	b, _ := v.([]byte)
	return b, true, nil
}

// Set implements cache.Cache.
func (c *otterCache) Set(_ context.Context, k string, v []byte, ttl time.Duration) error {
	c.oc.Set(k, any(v))
	if t := c.pick(ttl); t != c.def {
		c.oc.SetExpiresAfter(k, t)
	}
	return nil
}

// GetValue implements cache.ValueCache. The returned value's dynamic type
// matches whatever was stored — including []byte if the entry was written via
// the byte-path Set.
func (c *otterCache) GetValue(_ context.Context, k string) (any, bool, error) {
	v, ok := c.oc.GetIfPresent(k)
	if !ok {
		return nil, false, nil
	}
	return v, true, nil
}

// SetValue implements cache.ValueCache.
func (c *otterCache) SetValue(_ context.Context, k string, v any, ttl time.Duration) error {
	c.oc.Set(k, v)
	if t := c.pick(ttl); t != c.def {
		c.oc.SetExpiresAfter(k, t)
	}
	return nil
}

// Delete implements cache.Cache and cache.ValueCache.
func (c *otterCache) Delete(_ context.Context, k string) error {
	c.oc.Invalidate(k)
	return nil
}

func (c *otterCache) pick(ttl time.Duration) time.Duration {
	if ttl > 0 {
		return ttl
	}
	return c.def
}
