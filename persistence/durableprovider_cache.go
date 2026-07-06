package persistence

import (
	"log/slog"
	"time"

	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/persistence/cache"
	"github.com/zakyalvan/krtlwrkflw/persistence/cache/hotcache"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// DurableOption configures caching (and future concerns) on a DurableProvider.
type DurableOption func(*durableConfig)

type durableConfig struct {
	cacheEnabled      bool
	instanceProvider  cache.Provider
	humanTaskProvider cache.Provider
	instanceOwnership kernel.InstanceOwnership
	instanceTTL       time.Duration
	humanTaskTTL      time.Duration
	logger            *slog.Logger
}

// defaultDurableConfig is the opinionated, caching-on default: in-memory hotcache
// for both kinds, AlwaysOwn ownership (single-replica), 5m instance TTL, 30s
// human-task TTL.
func defaultDurableConfig() *durableConfig {
	return &durableConfig{
		cacheEnabled:      true,
		instanceProvider:  hotcache.New(),
		humanTaskProvider: hotcache.New(),
		instanceOwnership: kernel.AlwaysOwn{},
		instanceTTL:       5 * time.Minute,
		humanTaskTTL:      30 * time.Second,
		logger:            slog.Default(),
	}
}

// WithCacheProvider sets the substrate for BOTH the instance and human-task
// caches — the single simple knob. A nil provider is ignored (defaults remain).
func WithCacheProvider(p cache.Provider) DurableOption {
	return func(c *durableConfig) {
		if p != nil {
			c.instanceProvider = p
			c.humanTaskProvider = p
		}
	}
}

// WithInstanceCacheProvider overrides only the instance-cache substrate. A nil
// provider is ignored.
func WithInstanceCacheProvider(p cache.Provider) DurableOption {
	return func(c *durableConfig) {
		if p != nil {
			c.instanceProvider = p
		}
	}
}

// WithHumanTaskCacheProvider overrides only the human-task-cache substrate (use
// a distributed provider for multi-replica coherence). A nil provider is ignored.
func WithHumanTaskCacheProvider(p cache.Provider) DurableOption {
	return func(c *durableConfig) {
		if p != nil {
			c.humanTaskProvider = p
		}
	}
}

// WithDurableInstanceCacheOwnership sets the ownership gate for the instance
// cache. Supply a multi-replica ownership implementation for multi-replica
// deployments. A nil ownership is ignored.
func WithDurableInstanceCacheOwnership(o kernel.InstanceOwnership) DurableOption {
	return func(c *durableConfig) {
		if o != nil {
			c.instanceOwnership = o
		}
	}
}

// WithDurableInstanceCacheTTL sets the instance-cache TTL. Default 5m. A
// non-positive duration is ignored.
func WithDurableInstanceCacheTTL(d time.Duration) DurableOption {
	return func(c *durableConfig) {
		if d > 0 {
			c.instanceTTL = d
		}
	}
}

// WithDurableHumanTaskCacheTTL sets the human-task-cache TTL. Default 30s. A
// non-positive duration is ignored.
func WithDurableHumanTaskCacheTTL(d time.Duration) DurableOption {
	return func(c *durableConfig) {
		if d > 0 {
			c.humanTaskTTL = d
		}
	}
}

// WithoutCache disables all caching; stores are used unwrapped.
func WithoutCache() DurableOption {
	return func(c *durableConfig) { c.cacheEnabled = false }
}

// wrapCaching wraps is/ts per the config, or returns them unchanged when
// disabled. The wrapped instance store still satisfies kernel.JournalReader by
// delegating to the backing store, so the façade contract is preserved.
func (c *durableConfig) wrapCaching(is kernel.InstanceStore, ts humantask.TaskStore) (kernel.InstanceStore, humantask.TaskStore, error) {
	if !c.cacheEnabled {
		return is, ts, nil
	}
	wis, err := NewCachingInstanceStore(is, c.instanceOwnership, c.instanceProvider,
		WithInstanceCacheTTL(c.instanceTTL), WithInstanceCacheLogger(c.logger))
	if err != nil {
		return nil, nil, err
	}
	wts, err := NewCachingTaskStore(ts, c.humanTaskProvider, WithHumanTaskCacheTTL(c.humanTaskTTL))
	if err != nil {
		return nil, nil, err
	}
	return wis, wts, nil
}

func applyDurableOptions(opts []DurableOption) *durableConfig {
	cfg := defaultDurableConfig()
	for _, o := range opts {
		o(cfg)
	}
	return cfg
}
