package ottercache_test

import (
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/persistence/cache"
	"github.com/kartaladev/wrkflw/persistence/cache/cachetest"
	"github.com/kartaladev/wrkflw/persistence/cache/ottercache"
)

func TestOttercacheConformance(t *testing.T) {
	cachetest.RunConformance(t, func() cache.Provider {
		return ottercache.New(ottercache.WithCapacity(128))
	})
}

func TestOttercacheImplementsValueCache(t *testing.T) {
	c, err := ottercache.New().Cache("instances")
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	vc, ok := c.(cache.ValueCache)
	if !ok {
		t.Fatal("ottercache must implement cache.ValueCache")
	}
	if err := vc.SetValue(t.Context(), "k", 42, time.Minute); err != nil {
		t.Fatalf("setvalue: %v", err)
	}
	got, ok, err := vc.GetValue(t.Context(), "k")
	if err != nil || !ok || got.(int) != 42 {
		t.Fatalf("getvalue = %v ok=%v err=%v", got, ok, err)
	}
}

func TestOttercacheGetValueMiss(t *testing.T) {
	c, err := ottercache.New().Cache("misses")
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	vc, ok := c.(cache.ValueCache)
	if !ok {
		t.Fatal("ottercache must implement cache.ValueCache")
	}
	got, ok, err := vc.GetValue(t.Context(), "absent")
	if err != nil || ok || got != nil {
		t.Fatalf("getvalue miss = %v ok=%v err=%v", got, ok, err)
	}
}

func TestOttercacheZeroTTLFallsBackToDefault(t *testing.T) {
	c, err := ottercache.New().Cache("ns")
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	if err := c.Set(t.Context(), "z", []byte("v"), 0); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, ok, _ := c.Get(t.Context(), "z"); !ok {
		t.Fatal("zero-ttl entry should be reachable (default TTL applied, not zero)")
	}
}

func TestOttercacheInvalidOptionsIgnored(t *testing.T) {
	p := ottercache.New(ottercache.WithCapacity(0), ottercache.WithTTL(0))
	c, err := p.Cache("guarded")
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	if err := c.Set(t.Context(), "k", []byte("hello"), time.Minute); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, ok, err := c.Get(t.Context(), "k")
	if err != nil || !ok || string(got) != "hello" {
		t.Fatalf("get after invalid-option construction = %v ok=%v err=%v", got, ok, err)
	}
}
