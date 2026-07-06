package cachetest_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/persistence/cache"
	"github.com/zakyalvan/krtlwrkflw/persistence/cache/cachetest"
)

type mapProvider struct {
	mu     sync.Mutex
	caches map[string]*mapCache
}

func (p *mapProvider) Cache(ns string) (cache.Cache, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.caches == nil {
		p.caches = map[string]*mapCache{}
	}
	c, ok := p.caches[ns]
	if !ok {
		c = &mapCache{m: map[string][]byte{}}
		p.caches[ns] = c
	}
	return c, nil
}

type mapCache struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (c *mapCache) Get(_ context.Context, k string) ([]byte, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[k]
	return v, ok, nil
}
func (c *mapCache) Set(_ context.Context, k string, v []byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = v
	return nil
}
func (c *mapCache) Delete(_ context.Context, k string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, k)
	return nil
}

func TestRunConformance(t *testing.T) {
	cachetest.RunConformance(t, func() cache.Provider { return &mapProvider{} })
}
