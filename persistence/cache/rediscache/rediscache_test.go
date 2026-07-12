package rediscache_test

import (
	"errors"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/kartaladev/wrkflw/persistence/cache"
	"github.com/kartaladev/wrkflw/persistence/cache/cachetest"
	"github.com/kartaladev/wrkflw/persistence/cache/rediscache"
)

func TestRediscacheConformance(t *testing.T) {
	addr := cachetest.RunTestRedis(t)
	cachetest.RunConformance(t, func() cache.Provider {
		client := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = client.Close() })
		// Fresh keyspace per provider instance keeps namespace-isolation test honest.
		_ = client.FlushAll(t.Context()).Err()
		return rediscache.New(client)
	})
}

func TestRediscacheIsNotValueCache(t *testing.T) {
	addr := cachetest.RunTestRedis(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	c, _ := rediscache.New(client).Cache("humantasks")
	if _, ok := c.(cache.ValueCache); ok {
		t.Fatal("distributed rediscache must NOT implement cache.ValueCache")
	}
}

func TestRediscacheNilClient(t *testing.T) {
	t.Parallel()
	p := rediscache.New(nil)
	_, err := p.Cache("ns")
	if !errors.Is(err, cache.ErrNilCache) {
		t.Fatalf("expected cache.ErrNilCache, got %v", err)
	}
}

func TestRediscacheWithKeyPrefix(t *testing.T) {
	addr := cachetest.RunTestRedis(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	_ = client.FlushAll(t.Context()).Err()

	// Write a key via provider with a custom prefix, then verify it is
	// reachable under that prefix in the raw Redis keyspace.
	p := rediscache.New(client, rediscache.WithKeyPrefix("myapp:"))
	c, err := p.Cache("tasks")
	if err != nil {
		t.Fatalf("Cache: %v", err)
	}
	if err := c.Set(t.Context(), "k1", []byte("hello"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// The raw key must exist under myapp:tasks:k1.
	raw, err := client.Get(t.Context(), "myapp:tasks:k1").Bytes()
	if err != nil {
		t.Fatalf("raw Get: %v", err)
	}
	if string(raw) != "hello" {
		t.Fatalf("expected 'hello', got %q", raw)
	}
}
