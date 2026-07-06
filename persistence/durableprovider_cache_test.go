package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/persistence/cache/hotcache"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// stubOwnership is a distinct InstanceOwnership so option tests can assert the
// override took effect (vs the AlwaysOwn default).
type stubOwnership struct{}

func (stubOwnership) Acquire(context.Context, string) (bool, error) { return true, nil }
func (stubOwnership) Release(context.Context, string) error         { return nil }

func newTestMemInstanceStore(t *testing.T) *kernel.MemInstanceStore {
	t.Helper()
	is, err := kernel.NewMemInstanceStore()
	if err != nil {
		t.Fatalf("new mem instance store: %v", err)
	}
	return is
}

func TestDurableCacheConfigDefaults(t *testing.T) {
	cfg := defaultDurableConfig()
	if !cfg.cacheEnabled {
		t.Fatal("caching must be on by default")
	}
	if cfg.instanceProvider == nil || cfg.humanTaskProvider == nil {
		t.Fatal("default providers must be set")
	}
	if _, ok := cfg.instanceOwnership.(kernel.AlwaysOwn); !ok {
		t.Fatalf("default ownership = %T, want kernel.AlwaysOwn", cfg.instanceOwnership)
	}
	if cfg.instanceTTL != 5*time.Minute || cfg.humanTaskTTL != 30*time.Second {
		t.Fatalf("default TTLs = %v / %v", cfg.instanceTTL, cfg.humanTaskTTL)
	}
}

func TestWithoutCacheDisables(t *testing.T) {
	cfg := defaultDurableConfig()
	WithoutCache()(cfg)
	if cfg.cacheEnabled {
		t.Fatal("WithoutCache must disable caching")
	}
}

func TestWithCacheProviderSetsBoth(t *testing.T) {
	cfg := defaultDurableConfig()
	p := hotcache.New()
	WithCacheProvider(p)(cfg)
	if cfg.instanceProvider != p || cfg.humanTaskProvider != p {
		t.Fatal("WithCacheProvider must set both instance and human-task providers")
	}
}

func TestDurableCacheOptions(t *testing.T) {
	p := hotcache.New()

	tests := map[string]struct {
		option DurableOption
		assert func(t *testing.T, cfg *durableConfig)
	}{
		"WithInstanceCacheProvider sets instance only": {
			option: WithInstanceCacheProvider(p),
			assert: func(t *testing.T, cfg *durableConfig) {
				if cfg.instanceProvider != p {
					t.Fatal("instance provider not set")
				}
				if cfg.humanTaskProvider == p {
					t.Fatal("human-task provider must be untouched")
				}
			},
		},
		"WithInstanceCacheProvider ignores nil": {
			option: WithInstanceCacheProvider(nil),
			assert: func(t *testing.T, cfg *durableConfig) {
				if cfg.instanceProvider == nil {
					t.Fatal("nil provider must be ignored, default preserved")
				}
			},
		},
		"WithHumanTaskCacheProvider sets human-task only": {
			option: WithHumanTaskCacheProvider(p),
			assert: func(t *testing.T, cfg *durableConfig) {
				if cfg.humanTaskProvider != p {
					t.Fatal("human-task provider not set")
				}
				if cfg.instanceProvider == p {
					t.Fatal("instance provider must be untouched")
				}
			},
		},
		"WithHumanTaskCacheProvider ignores nil": {
			option: WithHumanTaskCacheProvider(nil),
			assert: func(t *testing.T, cfg *durableConfig) {
				if cfg.humanTaskProvider == nil {
					t.Fatal("nil provider must be ignored, default preserved")
				}
			},
		},
		"WithDurableInstanceCacheOwnership sets ownership": {
			option: WithDurableInstanceCacheOwnership(stubOwnership{}),
			assert: func(t *testing.T, cfg *durableConfig) {
				if _, ok := cfg.instanceOwnership.(stubOwnership); !ok {
					t.Fatalf("ownership = %T, want stubOwnership", cfg.instanceOwnership)
				}
			},
		},
		"WithDurableInstanceCacheOwnership ignores nil": {
			option: WithDurableInstanceCacheOwnership(nil),
			assert: func(t *testing.T, cfg *durableConfig) {
				if _, ok := cfg.instanceOwnership.(kernel.AlwaysOwn); !ok {
					t.Fatal("nil ownership must be ignored, AlwaysOwn preserved")
				}
			},
		},
		"WithDurableInstanceCacheTTL sets ttl": {
			option: WithDurableInstanceCacheTTL(time.Minute),
			assert: func(t *testing.T, cfg *durableConfig) {
				if cfg.instanceTTL != time.Minute {
					t.Fatalf("instance ttl = %v", cfg.instanceTTL)
				}
			},
		},
		"WithDurableInstanceCacheTTL ignores non-positive": {
			option: WithDurableInstanceCacheTTL(0),
			assert: func(t *testing.T, cfg *durableConfig) {
				if cfg.instanceTTL != 5*time.Minute {
					t.Fatalf("non-positive ttl must be ignored, got %v", cfg.instanceTTL)
				}
			},
		},
		"WithDurableHumanTaskCacheTTL sets ttl": {
			option: WithDurableHumanTaskCacheTTL(time.Minute),
			assert: func(t *testing.T, cfg *durableConfig) {
				if cfg.humanTaskTTL != time.Minute {
					t.Fatalf("human-task ttl = %v", cfg.humanTaskTTL)
				}
			},
		},
		"WithDurableHumanTaskCacheTTL ignores non-positive": {
			option: WithDurableHumanTaskCacheTTL(-1),
			assert: func(t *testing.T, cfg *durableConfig) {
				if cfg.humanTaskTTL != 30*time.Second {
					t.Fatalf("non-positive ttl must be ignored, got %v", cfg.humanTaskTTL)
				}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := defaultDurableConfig()
			tc.option(cfg)
			tc.assert(t, cfg)
		})
	}
}

func TestWrapCachingWrapsBothStores(t *testing.T) {
	cfg := defaultDurableConfig()
	is := newTestMemInstanceStore(t)
	ts := humantask.NewMemTaskStore()
	wis, wts, err := cfg.wrapCaching(is, ts)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if _, ok := wis.(*CachingInstanceStore); !ok {
		t.Fatalf("instance store not wrapped: %T", wis)
	}
	if _, ok := wts.(*CachingTaskStore); !ok {
		t.Fatalf("task store not wrapped: %T", wts)
	}
}

func TestWrapCachingDisabledReturnsOriginals(t *testing.T) {
	cfg := defaultDurableConfig()
	WithoutCache()(cfg)
	is := newTestMemInstanceStore(t)
	ts := humantask.NewMemTaskStore()
	wis, wts, err := cfg.wrapCaching(is, ts)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if _, ok := wis.(*CachingInstanceStore); ok {
		t.Fatal("caching disabled: instance store must be unwrapped")
	}
	if _, ok := wts.(*CachingTaskStore); ok {
		t.Fatal("caching disabled: task store must be unwrapped")
	}
}
