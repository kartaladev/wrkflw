// Package memcache is a distributed [cache.Provider] backed by
// github.com/bradfitz/gomemcache. Suitable for multi-replica deployments where
// a shared Memcached instance provides coherent caching across nodes.
//
// Caches returned by this provider implement only the byte-oriented [cache.Cache]
// interface — NOT [cache.ValueCache] — because serialization across process
// boundaries is mandatory for distributed operation.
//
// Each namespace is mapped to a key prefix of the form:
//
//	<globalPrefix><namespace>:<key>
//
// The default global prefix is "wrkflw:". Memcached keys must be ≤ 250 bytes
// and contain no spaces or control characters; the prefix scheme is safe for
// the token keys used by this engine.
package memcache

import (
	"context"
	"errors"
	"time"

	gomc "github.com/bradfitz/gomemcache/memcache"

	"github.com/zakyalvan/krtlwrkflw/persistence/cache"
)

// Compile-time assertion: mcCache satisfies cache.Cache but NOT cache.ValueCache.
var _ cache.Cache = (*mcCache)(nil)

// Option configures the provider.
type Option func(*provider)

// WithKeyPrefix overrides the global key prefix prepended to every key
// (default "wrkflw:"). A trailing colon is conventional but not enforced.
func WithKeyPrefix(p string) Option {
	return func(pr *provider) { pr.prefix = p }
}

// New returns a distributed [cache.Provider] backed by client. The caller owns
// the client's lifecycle (create, configure, close). If client is nil,
// [cache.ErrNilCache] is returned on every [cache.Provider.Cache] call.
func New(client *gomc.Client, opts ...Option) cache.Provider {
	p := &provider{client: client, prefix: "wrkflw:"}
	for _, o := range opts {
		o(p)
	}
	return p
}

type provider struct {
	client *gomc.Client
	prefix string
}

// Cache returns a [cache.Cache] scoped to the given namespace. Returns
// [cache.ErrNilCache] when the underlying client is nil.
func (p *provider) Cache(ns string) (cache.Cache, error) {
	if p.client == nil {
		return nil, cache.ErrNilCache
	}
	return &mcCache{client: p.client, prefix: p.prefix + ns + ":"}, nil
}

// mcCache is a namespaced byte cache over a shared *gomc.Client.
// It implements cache.Cache only; cache.ValueCache is deliberately omitted.
type mcCache struct {
	client *gomc.Client
	prefix string
}

// Get retrieves the value for key. A cache miss returns (nil, false, nil).
func (c *mcCache) Get(_ context.Context, key string) ([]byte, bool, error) {
	item, err := c.client.Get(c.prefix + key)
	if errors.Is(err, gomc.ErrCacheMiss) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return item.Value, true, nil
}

// Set stores val under key with the given TTL. A zero or negative TTL stores
// with no expiry (Expiration = 0 in Memcached terms).
func (c *mcCache) Set(_ context.Context, key string, val []byte, ttl time.Duration) error {
	exp := int32(0)
	if ttl > 0 {
		exp = int32(ttl.Seconds())
	}
	return c.client.Set(&gomc.Item{
		Key:        c.prefix + key,
		Value:      val,
		Expiration: exp,
	})
}

// Delete removes the key. It is a no-op if the key does not exist.
func (c *mcCache) Delete(_ context.Context, key string) error {
	err := c.client.Delete(c.prefix + key)
	if errors.Is(err, gomc.ErrCacheMiss) {
		return nil
	}
	return err
}
