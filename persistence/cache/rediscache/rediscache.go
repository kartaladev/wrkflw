// Package rediscache is a distributed [cache.Provider] backed by
// github.com/redis/go-redis/v9. Suitable for multi-replica deployments where
// a shared Redis instance provides coherent caching across nodes.
//
// Caches returned by this provider implement only the byte-oriented [cache.Cache]
// interface — NOT [cache.ValueCache] — because serialization across process
// boundaries is mandatory for distributed operation.
//
// Each namespace is mapped to a key prefix of the form:
//
//	<globalPrefix><namespace>:<key>
//
// The default global prefix is "wrkflw:".
package rediscache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/kartaladev/wrkflw/persistence/cache"
)

// Compile-time assertion: redisCache satisfies cache.Cache but NOT cache.ValueCache.
var _ cache.Cache = (*redisCache)(nil)

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
func New(client *redis.Client, opts ...Option) cache.Provider {
	p := &provider{client: client, prefix: "wrkflw:"}
	for _, o := range opts {
		o(p)
	}
	return p
}

type provider struct {
	client *redis.Client
	prefix string
}

// Cache returns a [cache.Cache] scoped to the given namespace. Returns
// [cache.ErrNilCache] when the underlying client is nil.
func (p *provider) Cache(ns string) (cache.Cache, error) {
	if p.client == nil {
		return nil, cache.ErrNilCache
	}
	return &redisCache{client: p.client, prefix: p.prefix + ns + ":"}, nil
}

// redisCache is a namespaced byte cache over a shared *redis.Client.
// It implements cache.Cache only; cache.ValueCache is deliberately omitted.
type redisCache struct {
	client *redis.Client
	prefix string
}

// Get retrieves the value for key. A cache miss returns (nil, false, nil).
func (c *redisCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	b, err := c.client.Get(ctx, c.prefix+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// Set stores val under key with the given TTL. A zero or negative TTL stores
// with no expiry.
func (c *redisCache) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	return c.client.Set(ctx, c.prefix+key, val, ttl).Err()
}

// Delete removes the key. It is a no-op if the key does not exist.
func (c *redisCache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, c.prefix+key).Err()
}
