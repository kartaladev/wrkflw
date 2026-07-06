package ottercache_test

import (
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/persistence/cache"
	"github.com/zakyalvan/krtlwrkflw/persistence/cache/cachetest"
	"github.com/zakyalvan/krtlwrkflw/persistence/cache/ottercache"
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
