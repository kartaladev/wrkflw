package memcache_test

import (
	"errors"
	"testing"
	"time"

	gomc "github.com/bradfitz/gomemcache/memcache"

	"github.com/zakyalvan/krtlwrkflw/persistence/cache"
	"github.com/zakyalvan/krtlwrkflw/persistence/cache/cachetest"
	"github.com/zakyalvan/krtlwrkflw/persistence/cache/memcache"
)

func TestMemcacheConformance(t *testing.T) {
	addr := cachetest.RunTestMemcached(t)
	cachetest.RunConformance(t, func() cache.Provider {
		client := gomc.New(addr)
		_ = client.DeleteAll()
		return memcache.New(client)
	})
}

func TestMemcacheIsNotValueCache(t *testing.T) {
	addr := cachetest.RunTestMemcached(t)
	client := gomc.New(addr)
	c, _ := memcache.New(client).Cache("humantasks")
	if _, ok := c.(cache.ValueCache); ok {
		t.Fatal("distributed memcache must NOT implement cache.ValueCache")
	}
}

func TestMemcacheNilClient(t *testing.T) {
	t.Parallel()
	p := memcache.New(nil)
	_, err := p.Cache("ns")
	if !errors.Is(err, cache.ErrNilCache) {
		t.Fatalf("expected cache.ErrNilCache, got %v", err)
	}
}

func TestMemcacheZeroTTLStoresWithoutExpiry(t *testing.T) {
	addr := cachetest.RunTestMemcached(t)
	client := gomc.New(addr)
	_ = client.DeleteAll()
	c, err := memcache.New(client).Cache("ns")
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	// ttl <= 0 must map to Expiration=0 (never expire), i.e. the entry is set and reachable.
	if err := c.Set(t.Context(), "z", []byte("v"), 0); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, ok, err := c.Get(t.Context(), "z")
	if err != nil || !ok || string(got) != "v" {
		t.Fatalf("zero-ttl get = %q ok=%v err=%v", got, ok, err)
	}
}

func TestMemcacheWithKeyPrefix(t *testing.T) {
	addr := cachetest.RunTestMemcached(t)
	client := gomc.New(addr)
	_ = client.DeleteAll()

	// Write a key via provider with a custom prefix, then verify the raw key
	// is stored under that prefix in Memcached.
	p := memcache.New(client, memcache.WithKeyPrefix("myapp:"))
	c, err := p.Cache("tasks")
	if err != nil {
		t.Fatalf("Cache: %v", err)
	}
	if err := c.Set(t.Context(), "k1", []byte("hello"), time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Retrieve directly via raw key to confirm prefix is applied.
	item, err := client.Get("myapp:tasks:k1")
	if err != nil {
		t.Fatalf("raw Get: %v", err)
	}
	if string(item.Value) != "hello" {
		t.Fatalf("expected 'hello', got %q", item.Value)
	}
}
