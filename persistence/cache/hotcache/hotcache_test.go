package hotcache_test

import (
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/persistence/cache"
	"github.com/kartaladev/wrkflw/persistence/cache/cachetest"
	"github.com/kartaladev/wrkflw/persistence/cache/hotcache"
)

func TestHotcacheConformance(t *testing.T) {
	cachetest.RunConformance(t, func() cache.Provider {
		return hotcache.New(hotcache.WithCapacity(128))
	})
}

// TestHotcacheWithTTL verifies that WithTTL is accepted and the cache still works.
// (It also satisfies coverage of the WithTTL option and the pick() zero-ttl branch
// since the cache is constructed with a custom TTL and Set is called with ttl=0.)
func TestHotcacheWithTTL(t *testing.T) {
	p := hotcache.New(hotcache.WithTTL(10 * time.Minute))
	c, err := p.Cache("ttl-ns")
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	// Set with zero ttl — pick() should fall back to the configured default.
	if err := c.Set(t.Context(), "k", []byte("val"), 0); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, ok, err := c.Get(t.Context(), "k")
	if err != nil || !ok || string(got) != "val" {
		t.Fatalf("get = %q ok=%v err=%v", got, ok, err)
	}
}

func TestHotcacheImplementsValueCache(t *testing.T) {
	c, err := hotcache.New().Cache("instances")
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	vc, ok := c.(cache.ValueCache)
	if !ok {
		t.Fatal("hotcache must implement cache.ValueCache")
	}
	type box struct{ N int }
	if err := vc.SetValue(t.Context(), "k", box{N: 3}, time.Minute); err != nil {
		t.Fatalf("setvalue: %v", err)
	}
	got, ok, err := vc.GetValue(t.Context(), "k")
	if err != nil || !ok {
		t.Fatalf("getvalue ok=%v err=%v", ok, err)
	}
	if got.(box).N != 3 {
		t.Fatalf("getvalue = %+v", got)
	}
}
