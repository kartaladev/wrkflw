# Persistence Caching Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move all mutable-state caching into the persistence layer behind a neutral, library-backed `Cache` port with swappable adapters (in-memory default + distributed), covering the instance-state cache and a new human-task point-read cache, wired opinionated-on by default.

**Architecture:** A pure `persistence/cache` package defines a byte-oriented `Cache` port, an optional in-process `ValueCache` capability, a `Provider` factory, and a generic `Codec[V]` that fast-paths onto `ValueCache` (live clones, zero-serialization) or falls back to JSON bytes. Four adapters live in isolated subpackages so heavy deps stay optional. The existing `CachingInstanceStore` relocates from `runtime/kernel` into `persistence` and swaps its hand-rolled `container/list` LRU for the `Codec`; a new `CachingTaskStore` caches `humantask.TaskStore.Get`. The `DurableProvider` constructors wrap both stores by default (in-memory `hotcache`, `AlwaysOwn` + warn).

**Tech Stack:** Go 1.25; `github.com/samber/hot` (in-mem default); `github.com/maypok86/otter/v2` (in-mem); `github.com/redis/go-redis/v9` (distributed); `github.com/bradfitz/gomemcache` (distributed); `github.com/jonboulle/clockwork` via the in-repo `clock.Clock`; `testcontainers-go` for Redis/Memcached tests; `mockgen` (uber-go/mock) where interfaces need doubles.

## Global Constraints

- **Language:** Go 1.25 (hard requirement).
- **TDD strict:** No production code before a failing test. Every new exported symbol and every behavioral change is preceded by a Bash run of `go test ./<package>/...` showing a red state (compile error like `undefined: X` counts). Never create `foo_test.go` and `foo.go` in one edit pass with no `go test` between them.
- **Never import** watermill/casbin/gocron/clockwork directly from engine/workflow code; time comes from `clock.Clock` only. Cache adapter subpackages are the ONLY place their cache library is imported — the core `persistence`/`persistence/cache` packages must never import `go-redis`, `otter`, `samber/hot`, or `gomemcache`.
- **Tests:** black-box (`package xxx_test`) preferred; table tests use the project `table-test` skill's `assert` closure form (not `want`/`wantErr` fields); `t.Context()` over `context.Background()`; Redis/Memcached via `testcontainers-go` (never mocked); mocks via `mockgen --typed` placed beside the mocked interface.
- **Coverage:** each touched package ≥ 85% line coverage (`go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1`).
- **Lint:** `golangci-lint run ./...` clean before any task is "done".
- **Error sentinels:** message prefix `workflow-<package>:` (e.g. `workflow-cache: ...`).
- **Module path:** `github.com/kartaladev/wrkflw`.
- **Docs:** ADRs use the Nygard template under `docs/adr/NNNN-<slug>.md`; next free number is **0099**.
- **Commits:** Conventional Commits scoped to the area; commit per task. End commit messages with the two trailer lines the harness requires.

---

### Task 1: `cache` port + generic `Codec[V]`

**Files:**
- Create: `persistence/cache/cache.go`
- Create: `persistence/cache/codec.go`
- Test: `persistence/cache/codec_test.go`

**Interfaces:**
- Produces:
  - `cache.Cache` interface: `Get(ctx, key string) ([]byte, bool, error)`, `Set(ctx, key string, val []byte, ttl time.Duration) error`, `Delete(ctx, key string) error`.
  - `cache.ValueCache` interface: `GetValue(ctx, key string) (any, bool, error)`, `SetValue(ctx, key string, v any, ttl time.Duration) error`, `Delete(ctx, key string) error`.
  - `cache.Provider` interface: `Cache(namespace string) (Cache, error)`.
  - `cache.ErrNilCache error`.
  - `cache.NewCodec[V any](c Cache, marshal func(V) ([]byte, error), unmarshal func([]byte) (V, error), clone func(V) V) (*Codec[V], error)` with methods `Get(ctx, key) (V, bool, error)`, `Set(ctx, key string, v V, ttl time.Duration) error`, `Delete(ctx, key string) error`.

- [ ] **Step 1: Write the failing test**

`persistence/cache/codec_test.go`:
```go
package cache_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/persistence/cache"
)

type payload struct {
	N int `json:"n"`
}

func clonePayload(p payload) payload { return p }

// byteFake is a minimal byte-only Cache (no ValueCache) exercising the JSON path.
type byteFake struct{ m map[string][]byte }

func newByteFake() *byteFake { return &byteFake{m: map[string][]byte{}} }
func (f *byteFake) Get(_ context.Context, k string) ([]byte, bool, error) {
	v, ok := f.m[k]
	return v, ok, nil
}
func (f *byteFake) Set(_ context.Context, k string, v []byte, _ time.Duration) error {
	f.m[k] = v
	return nil
}
func (f *byteFake) Delete(_ context.Context, k string) error { delete(f.m, k); return nil }

// valueFake also implements ValueCache exercising the live-value fast path.
type valueFake struct {
	byteFake
	vals    map[string]any
	setCall int
}

func newValueFake() *valueFake { return &valueFake{byteFake: *newByteFake(), vals: map[string]any{}} }
func (f *valueFake) GetValue(_ context.Context, k string) (any, bool, error) {
	v, ok := f.vals[k]
	return v, ok, nil
}
func (f *valueFake) SetValue(_ context.Context, k string, v any, _ time.Duration) error {
	f.setCall++
	f.vals[k] = v
	return nil
}

func TestCodec(t *testing.T) {
	tests := []struct {
		name   string
		newCache func() cache.Cache
		assert func(t *testing.T, cd *cache.Codec[payload], c cache.Cache)
	}{
		{
			name:     "byte path round-trips via JSON",
			newCache: func() cache.Cache { return newByteFake() },
			assert: func(t *testing.T, cd *cache.Codec[payload], c cache.Cache) {
				ctx := t.Context()
				if err := cd.Set(ctx, "k", payload{N: 7}, time.Minute); err != nil {
					t.Fatalf("set: %v", err)
				}
				// A byte-only cache must hold JSON bytes, not a live value.
				raw, _, _ := c.Get(ctx, "k")
				var p payload
				if err := json.Unmarshal(raw, &p); err != nil || p.N != 7 {
					t.Fatalf("stored bytes not JSON payload: %q err=%v", raw, err)
				}
				got, ok, err := cd.Get(ctx, "k")
				if err != nil || !ok || got.N != 7 {
					t.Fatalf("get = %+v ok=%v err=%v", got, ok, err)
				}
			},
		},
		{
			name:     "value path uses ValueCache and skips JSON",
			newCache: func() cache.Cache { return newValueFake() },
			assert: func(t *testing.T, cd *cache.Codec[payload], c cache.Cache) {
				ctx := t.Context()
				vf := c.(*valueFake)
				if err := cd.Set(ctx, "k", payload{N: 9}, time.Minute); err != nil {
					t.Fatalf("set: %v", err)
				}
				if vf.setCall != 1 {
					t.Fatalf("expected ValueCache.SetValue used, setCall=%d", vf.setCall)
				}
				if len(vf.m) != 0 {
					t.Fatalf("byte map should be empty on value path, got %v", vf.m)
				}
				got, ok, err := cd.Get(ctx, "k")
				if err != nil || !ok || got.N != 9 {
					t.Fatalf("get = %+v ok=%v err=%v", got, ok, err)
				}
			},
		},
		{
			name:     "miss returns ok=false",
			newCache: func() cache.Cache { return newByteFake() },
			assert: func(t *testing.T, cd *cache.Codec[payload], _ cache.Cache) {
				_, ok, err := cd.Get(t.Context(), "absent")
				if err != nil || ok {
					t.Fatalf("miss = ok=%v err=%v", ok, err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.newCache()
			cd, err := cache.NewCodec[payload](c, json.Marshal, func(b []byte) (payload, error) {
				var p payload
				return p, json.Unmarshal(b, &p)
			}, clonePayload)
			if err != nil {
				t.Fatalf("new codec: %v", err)
			}
			tt.assert(t, cd, c)
		})
	}
}

func TestNewCodecNilCache(t *testing.T) {
	_, err := cache.NewCodec[payload](nil, json.Marshal, nil, clonePayload)
	if err == nil {
		t.Fatal("expected error for nil cache")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./persistence/cache/...`
Expected: FAIL — `package github.com/kartaladev/wrkflw/persistence/cache is not in std` / `undefined: cache.NewCodec`.

- [ ] **Step 3: Write minimal implementation**

`persistence/cache/cache.go`:
```go
// Package cache defines a neutral, library-agnostic cache substrate used by the
// persistence layer. Adapters (in-memory or distributed) implement [Cache];
// in-process adapters may additionally implement [ValueCache] to avoid
// serialization on the hot path. [Provider] is a factory a consumer supplies via
// the persistence caching options so the store wires per-kind caches internally.
package cache

import (
	"context"
	"errors"
	"time"
)

// ErrNilCache is returned by constructors when a required Cache is nil.
var ErrNilCache = errors.New("workflow-cache: nil cache")

// Cache is the byte-oriented substrate every adapter implements. A miss returns
// (nil, false, nil); an I/O failure returns a non-nil error. ttl <= 0 means the
// adapter's configured default (or no expiry).
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// ValueCache is an optional capability implemented by in-process adapters that
// can store live values without serialization. [Codec] type-asserts it and, when
// present, skips (un)marshaling. Mirrors the dialect.Notifier/Locker pattern.
type ValueCache interface {
	GetValue(ctx context.Context, key string) (any, bool, error)
	SetValue(ctx context.Context, key string, v any, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// Provider builds namespaced caches. A store calls it once per cache-kind
// (e.g. "instances", "humantasks") so a consumer supplies one Provider and the
// store wires the concrete caches.
type Provider interface {
	Cache(namespace string) (Cache, error)
}
```

`persistence/cache/codec.go`:
```go
package cache

import (
	"context"
	"time"
)

// Codec adapts a byte-oriented [Cache] to a typed value V. When the underlying
// cache implements [ValueCache], Codec stores/returns live cloned values (no
// serialization); otherwise it marshals/unmarshals with the supplied functions.
type Codec[V any] struct {
	raw       Cache
	val       ValueCache
	marshal   func(V) ([]byte, error)
	unmarshal func([]byte) (V, error)
	clone     func(V) V
}

// NewCodec wraps c. marshal/unmarshal are used only on the byte path; clone is
// used only on the value path (to prevent aliasing of cached live values).
// Returns [ErrNilCache] when c is nil.
func NewCodec[V any](c Cache, marshal func(V) ([]byte, error), unmarshal func([]byte) (V, error), clone func(V) V) (*Codec[V], error) {
	if c == nil {
		return nil, ErrNilCache
	}
	cd := &Codec[V]{raw: c, marshal: marshal, unmarshal: unmarshal, clone: clone}
	if vc, ok := c.(ValueCache); ok {
		cd.val = vc
	}
	return cd, nil
}

// Get returns the value for key. A miss is (zero, false, nil).
func (cd *Codec[V]) Get(ctx context.Context, key string) (V, bool, error) {
	var zero V
	if cd.val != nil {
		v, ok, err := cd.val.GetValue(ctx, key)
		if err != nil || !ok {
			return zero, ok, err
		}
		tv, ok := v.(V)
		if !ok {
			return zero, false, nil // foreign value: treat as miss
		}
		return cd.clone(tv), true, nil
	}
	b, ok, err := cd.raw.Get(ctx, key)
	if err != nil || !ok {
		return zero, ok, err
	}
	tv, err := cd.unmarshal(b)
	if err != nil {
		return zero, false, err
	}
	return tv, true, nil
}

// Set stores v under key with ttl.
func (cd *Codec[V]) Set(ctx context.Context, key string, v V, ttl time.Duration) error {
	if cd.val != nil {
		return cd.val.SetValue(ctx, key, cd.clone(v), ttl)
	}
	b, err := cd.marshal(v)
	if err != nil {
		return err
	}
	return cd.raw.Set(ctx, key, b, ttl)
}

// Delete removes key.
func (cd *Codec[V]) Delete(ctx context.Context, key string) error {
	return cd.raw.Delete(ctx, key)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./persistence/cache/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add persistence/cache/cache.go persistence/cache/codec.go persistence/cache/codec_test.go
git commit -m "feat(cache): neutral Cache port + generic Codec"
```

---

### Task 2: shared adapter conformance harness

**Files:**
- Create: `persistence/cache/cachetest/conformance.go`
- Test: `persistence/cache/cachetest/conformance_test.go`

**Interfaces:**
- Consumes: `cache.Cache`, `cache.Provider` (Task 1).
- Produces: `cachetest.RunConformance(t *testing.T, newProvider func() cache.Provider)` — a reusable suite every adapter test calls. It exercises Get-miss, Set/Get round-trip, Delete, namespace isolation, and overwrite.

- [ ] **Step 1: Write the failing test**

`persistence/cache/cachetest/conformance_test.go` (validates the harness against a trivial in-memory reference Provider):
```go
package cachetest_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/persistence/cache"
	"github.com/kartaladev/wrkflw/persistence/cache/cachetest"
)

type mapProvider struct{ mu sync.Mutex; caches map[string]*mapCache }

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./persistence/cache/cachetest/...`
Expected: FAIL — `undefined: cachetest.RunConformance`.

- [ ] **Step 3: Write minimal implementation**

`persistence/cache/cachetest/conformance.go`:
```go
// Package cachetest provides a reusable conformance suite for cache.Provider
// adapters plus testcontainers helpers for distributed backends.
package cachetest

import (
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/persistence/cache"
)

// RunConformance exercises the behavioral contract every cache.Cache must honor.
// newProvider must return a fresh, empty provider on each call.
func RunConformance(t *testing.T, newProvider func() cache.Provider) {
	t.Helper()

	t.Run("miss returns false", func(t *testing.T) {
		c, err := newProvider().Cache("ns")
		if err != nil {
			t.Fatalf("cache: %v", err)
		}
		_, ok, err := c.Get(t.Context(), "absent")
		if err != nil || ok {
			t.Fatalf("miss = ok=%v err=%v", ok, err)
		}
	})

	t.Run("set then get round-trips", func(t *testing.T) {
		c, _ := newProvider().Cache("ns")
		if err := c.Set(t.Context(), "k", []byte("v"), time.Minute); err != nil {
			t.Fatalf("set: %v", err)
		}
		got, ok, err := c.Get(t.Context(), "k")
		if err != nil || !ok || string(got) != "v" {
			t.Fatalf("get = %q ok=%v err=%v", got, ok, err)
		}
	})

	t.Run("overwrite replaces value", func(t *testing.T) {
		c, _ := newProvider().Cache("ns")
		_ = c.Set(t.Context(), "k", []byte("a"), time.Minute)
		_ = c.Set(t.Context(), "k", []byte("b"), time.Minute)
		got, _, _ := c.Get(t.Context(), "k")
		if string(got) != "b" {
			t.Fatalf("overwrite got %q", got)
		}
	})

	t.Run("delete removes", func(t *testing.T) {
		c, _ := newProvider().Cache("ns")
		_ = c.Set(t.Context(), "k", []byte("v"), time.Minute)
		if err := c.Delete(t.Context(), "k"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		_, ok, _ := c.Get(t.Context(), "k")
		if ok {
			t.Fatal("expected miss after delete")
		}
	})

	t.Run("namespaces are isolated", func(t *testing.T) {
		p := newProvider()
		a, _ := p.Cache("a")
		b, _ := p.Cache("b")
		_ = a.Set(t.Context(), "k", []byte("va"), time.Minute)
		if _, ok, _ := b.Get(t.Context(), "k"); ok {
			t.Fatal("namespace b should not see namespace a's key")
		}
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./persistence/cache/cachetest/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add persistence/cache/cachetest/
git commit -m "test(cache): shared adapter conformance harness"
```

---

### Task 3: `hotcache` adapter (samber/hot, default in-memory, implements ValueCache)

**Files:**
- Create: `persistence/cache/hotcache/hotcache.go`
- Test: `persistence/cache/hotcache/hotcache_test.go`
- Modify: `go.mod` (add `github.com/samber/hot`)

**Interfaces:**
- Consumes: `cache.Cache`, `cache.ValueCache`, `cache.Provider` (Task 1); `cachetest.RunConformance` (Task 2).
- Produces: `hotcache.New(opts ...Option) cache.Provider`; `hotcache.WithCapacity(n int) Option`; `hotcache.WithTTL(d time.Duration) Option`. The returned provider's caches implement BOTH `cache.Cache` and `cache.ValueCache`.

> **Library note:** after `go get`, run `go doc github.com/samber/hot` to confirm the exact constructor/method names (`NewHotCache`, builder `.WithTTL().Build()`, `Set`/`SetWithTTL`, `Get`, `Delete`). Adjust the calls below to the pinned version; the conformance + ValueCache tests are the source of truth.

- [ ] **Step 1: Write the failing test**

`persistence/cache/hotcache/hotcache_test.go`:
```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go get github.com/samber/hot && go test ./persistence/cache/hotcache/...`
Expected: FAIL — `undefined: hotcache.New`.

- [ ] **Step 3: Write minimal implementation**

`persistence/cache/hotcache/hotcache.go`:
```go
// Package hotcache is the default in-memory cache.Provider backed by
// github.com/samber/hot. Its caches implement cache.ValueCache, so the persistence
// Codec stores live values without serialization.
package hotcache

import (
	"context"
	"sync"
	"time"

	"github.com/samber/hot"

	"github.com/kartaladev/wrkflw/persistence/cache"
)

const (
	defaultCapacity = 1024
	defaultTTL      = 5 * time.Minute
)

// Option configures the provider.
type Option func(*provider)

// WithCapacity caps entries per namespace (LRU eviction beyond it). Default 1024.
func WithCapacity(n int) Option { return func(p *provider) { if n > 0 { p.capacity = n } } }

// WithTTL sets the default entry TTL. Default 5m. Per-call ttl on Set overrides it.
func WithTTL(d time.Duration) Option { return func(p *provider) { if d > 0 { p.ttl = d } } }

// New returns an in-memory cache.Provider. Each namespace gets an independent
// bounded cache.
func New(opts ...Option) cache.Provider {
	p := &provider{capacity: defaultCapacity, ttl: defaultTTL, caches: map[string]*hotCache{}}
	for _, o := range opts {
		o(p)
	}
	return p
}

type provider struct {
	mu       sync.Mutex
	capacity int
	ttl      time.Duration
	caches   map[string]*hotCache
}

func (p *provider) Cache(ns string) (cache.Cache, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.caches[ns]; ok {
		return c, nil
	}
	c := &hotCache{
		def: p.ttl,
		hc:  hot.NewHotCache[string, any](hot.LRU, p.capacity).WithTTL(p.ttl).Build(),
	}
	p.caches[ns] = c
	return c, nil
}

// hotCache implements cache.Cache and cache.ValueCache over one hot.HotCache.
type hotCache struct {
	def time.Duration
	hc  *hot.HotCache[string, any]
}

func (c *hotCache) Get(_ context.Context, k string) ([]byte, bool, error) {
	v, ok := c.hc.Get(k)
	if !ok {
		return nil, false, nil
	}
	b, _ := v.([]byte)
	return b, true, nil
}

func (c *hotCache) Set(_ context.Context, k string, v []byte, ttl time.Duration) error {
	c.hc.SetWithTTL(k, any(v), c.pick(ttl))
	return nil
}

func (c *hotCache) GetValue(_ context.Context, k string) (any, bool, error) {
	return c.hc.Get(k)
}

func (c *hotCache) SetValue(_ context.Context, k string, v any, ttl time.Duration) error {
	c.hc.SetWithTTL(k, v, c.pick(ttl))
	return nil
}

func (c *hotCache) Delete(_ context.Context, k string) error {
	c.hc.Delete(k)
	return nil
}

func (c *hotCache) pick(ttl time.Duration) time.Duration {
	if ttl > 0 {
		return ttl
	}
	return c.def
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./persistence/cache/hotcache/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum persistence/cache/hotcache/
git commit -m "feat(cache): samber/hot in-memory adapter (default provider)"
```

---

### Task 4: `ottercache` adapter (maypok86/otter v2, in-memory, implements ValueCache)

**Files:**
- Create: `persistence/cache/ottercache/ottercache.go`
- Test: `persistence/cache/ottercache/ottercache_test.go`
- Modify: `go.mod` (add `github.com/maypok86/otter/v2`)

**Interfaces:**
- Consumes: `cache.*` (Task 1); `cachetest.RunConformance` (Task 2).
- Produces: `ottercache.New(opts ...Option) cache.Provider`; `ottercache.WithCapacity(n int) Option`; `ottercache.WithTTL(d time.Duration) Option`. Caches implement `cache.Cache` + `cache.ValueCache`.

> **Library note:** confirm the otter v2 API with `go doc github.com/maypok86/otter/v2` after `go get` (v2 uses `otter.Must(&otter.Options[K,V]{...})`, `Set`, `GetIfPresent`, `Invalidate`, and an `ExpiryCalculator` such as `otter.ExpiryWriting`). Adjust the code to the pinned version.

- [ ] **Step 1: Write the failing test**

`persistence/cache/ottercache/ottercache_test.go`:
```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go get github.com/maypok86/otter/v2 && go test ./persistence/cache/ottercache/...`
Expected: FAIL — `undefined: ottercache.New`.

- [ ] **Step 3: Write minimal implementation**

`persistence/cache/ottercache/ottercache.go`:
```go
// Package ottercache is an in-memory cache.Provider backed by
// github.com/maypok86/otter/v2 (S3-FIFO + W-TinyLFU). Its caches implement
// cache.ValueCache so the persistence Codec avoids serialization.
package ottercache

import (
	"context"
	"sync"
	"time"

	"github.com/maypok86/otter/v2"

	"github.com/kartaladev/wrkflw/persistence/cache"
)

const (
	defaultCapacity = 1024
	defaultTTL      = 5 * time.Minute
)

// Option configures the provider.
type Option func(*provider)

// WithCapacity sets the maximum entries per namespace. Default 1024.
func WithCapacity(n int) Option { return func(p *provider) { if n > 0 { p.capacity = n } } }

// WithTTL sets the default entry TTL (expire-after-write). Default 5m.
func WithTTL(d time.Duration) Option { return func(p *provider) { if d > 0 { p.ttl = d } } }

// New returns an in-memory cache.Provider; each namespace is an independent cache.
func New(opts ...Option) cache.Provider {
	p := &provider{capacity: defaultCapacity, ttl: defaultTTL, caches: map[string]*otterCache{}}
	for _, o := range opts {
		o(p)
	}
	return p
}

type provider struct {
	mu       sync.Mutex
	capacity int
	ttl      time.Duration
	caches   map[string]*otterCache
}

func (p *provider) Cache(ns string) (cache.Cache, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.caches[ns]; ok {
		return c, nil
	}
	oc := otter.Must(&otter.Options[string, any]{
		MaximumSize:      p.capacity,
		ExpiryCalculator: otter.ExpiryWriting[string, any](p.ttl),
	})
	c := &otterCache{oc: oc}
	p.caches[ns] = c
	return c, nil
}

// otterCache implements cache.Cache + cache.ValueCache.
type otterCache struct {
	oc *otter.Cache[string, any]
}

func (c *otterCache) Get(_ context.Context, k string) ([]byte, bool, error) {
	v, ok := c.oc.GetIfPresent(k)
	if !ok {
		return nil, false, nil
	}
	b, _ := v.([]byte)
	return b, true, nil
}
func (c *otterCache) Set(_ context.Context, k string, v []byte, _ time.Duration) error {
	c.oc.Set(k, any(v))
	return nil
}
func (c *otterCache) GetValue(_ context.Context, k string) (any, bool, error) {
	v, ok := c.oc.GetIfPresent(k)
	return v, ok, nil
}
func (c *otterCache) SetValue(_ context.Context, k string, v any, _ time.Duration) error {
	c.oc.Set(k, v)
	return nil
}
func (c *otterCache) Delete(_ context.Context, k string) error {
	c.oc.Invalidate(k)
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./persistence/cache/ottercache/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum persistence/cache/ottercache/
git commit -m "feat(cache): maypok86/otter in-memory adapter"
```

---

### Task 5: Redis + Memcached testcontainers helpers

**Files:**
- Create: `persistence/cache/cachetest/containers.go`
- Test: `persistence/cache/cachetest/containers_test.go`
- Modify: `go.mod` (add `github.com/testcontainers/testcontainers-go` + `.../modules/redis` if not already present)

**Interfaces:**
- Produces:
  - `cachetest.RunTestRedis(t *testing.T) string` — starts a Redis container, returns its address (`host:port`), registers cleanup via `t.Cleanup`.
  - `cachetest.RunTestMemcached(t *testing.T) string` — starts a Memcached container, returns its address, registers cleanup.

> Modeled on the repo's `database.RunTestDatabase` per the `use-testcontainers` skill. These need a running Docker daemon; the tests skip with `t.Skip` when Docker is unavailable (check for a testcontainers start error and `t.Skipf`).

- [ ] **Step 1: Write the failing test**

`persistence/cache/cachetest/containers_test.go`:
```go
package cachetest_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/persistence/cache/cachetest"
)

func TestRunTestRedisReturnsAddr(t *testing.T) {
	addr := cachetest.RunTestRedis(t)
	if addr == "" {
		t.Fatal("expected non-empty redis addr")
	}
}

func TestRunTestMemcachedReturnsAddr(t *testing.T) {
	addr := cachetest.RunTestMemcached(t)
	if addr == "" {
		t.Fatal("expected non-empty memcached addr")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./persistence/cache/cachetest/... -run RunTest`
Expected: FAIL — `undefined: cachetest.RunTestRedis`.

- [ ] **Step 3: Write minimal implementation**

`persistence/cache/cachetest/containers.go`:
```go
package cachetest

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// RunTestRedis starts a throwaway Redis 7 container and returns its host:port.
// It skips the test when no Docker daemon is reachable.
func RunTestRedis(t *testing.T) string {
	t.Helper()
	return runContainer(t, "redis:7-alpine", "6379/tcp", wait.ForListeningPort("6379/tcp"))
}

// RunTestMemcached starts a throwaway Memcached container and returns host:port.
// It skips the test when no Docker daemon is reachable.
func RunTestMemcached(t *testing.T) string {
	t.Helper()
	return runContainer(t, "memcached:1.6-alpine", "11211/tcp", wait.ForListeningPort("11211/tcp"))
}

func runContainer(t *testing.T, image, port string, wf wait.Strategy) string {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        image,
			ExposedPorts: []string{port},
			WaitingFor:   wf,
		},
		Started: true,
	})
	if err != nil {
		t.Skipf("docker unavailable, skipping: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	mapped, err := c.MappedPort(ctx, testcontainers.NewPort(port))
	if err != nil {
		t.Fatalf("mapped port: %v", err)
	}
	return host + ":" + mapped.Port()
}
```

> If `testcontainers.NewPort` is not the exact helper in the pinned version, use `nat.Port(port)` from `github.com/docker/go-connections/nat` (already a transitive dep). Confirm via `go doc`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./persistence/cache/cachetest/... -run RunTest`
Expected: PASS (or SKIP if Docker is unavailable — acceptable).

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum persistence/cache/cachetest/containers.go persistence/cache/cachetest/containers_test.go
git commit -m "test(cache): testcontainers helpers for Redis and Memcached"
```

---

### Task 6: `rediscache` adapter (go-redis v9, distributed, byte Cache)

**Files:**
- Create: `persistence/cache/rediscache/rediscache.go`
- Test: `persistence/cache/rediscache/rediscache_test.go`
- Modify: `go.mod` (add `github.com/redis/go-redis/v9`)

**Interfaces:**
- Consumes: `cache.*` (Task 1); `cachetest.RunConformance`, `cachetest.RunTestRedis` (Tasks 2, 5).
- Produces: `rediscache.New(client *redis.Client, opts ...Option) cache.Provider`; `rediscache.WithKeyPrefix(p string) Option`. Caches implement `cache.Cache` only (byte path); namespace becomes a key prefix over the shared client.

- [ ] **Step 1: Write the failing test**

`persistence/cache/rediscache/rediscache_test.go`:
```go
package rediscache_test

import (
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go get github.com/redis/go-redis/v9 && go test ./persistence/cache/rediscache/...`
Expected: FAIL — `undefined: rediscache.New`.

- [ ] **Step 3: Write minimal implementation**

`persistence/cache/rediscache/rediscache.go`:
```go
// Package rediscache is a distributed cache.Provider backed by
// github.com/redis/go-redis/v9. Suitable for the multi-replica human-task cache.
// It implements only the byte-oriented cache.Cache (no cache.ValueCache).
package rediscache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/kartaladev/wrkflw/persistence/cache"
)

// Option configures the provider.
type Option func(*provider)

// WithKeyPrefix prefixes every key (e.g. "wrkflw:"). Default "wrkflw:".
func WithKeyPrefix(p string) Option { return func(pr *provider) { pr.prefix = p } }

// New returns a distributed cache.Provider over client. The caller owns client's
// lifecycle.
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

func (p *provider) Cache(ns string) (cache.Cache, error) {
	if p.client == nil {
		return nil, cache.ErrNilCache
	}
	return &redisCache{client: p.client, prefix: p.prefix + ns + ":"}, nil
}

type redisCache struct {
	client *redis.Client
	prefix string
}

func (c *redisCache) Get(ctx context.Context, k string) ([]byte, bool, error) {
	b, err := c.client.Get(ctx, c.prefix+k).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

func (c *redisCache) Set(ctx context.Context, k string, v []byte, ttl time.Duration) error {
	return c.client.Set(ctx, c.prefix+k, v, ttl).Err()
}

func (c *redisCache) Delete(ctx context.Context, k string) error {
	return c.client.Del(ctx, c.prefix+k).Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./persistence/cache/rediscache/...`
Expected: PASS (or SKIP without Docker).

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum persistence/cache/rediscache/
git commit -m "feat(cache): go-redis distributed adapter"
```

---

### Task 7: `memcache` adapter (gomemcache, distributed, byte Cache)

**Files:**
- Create: `persistence/cache/memcache/memcache.go`
- Test: `persistence/cache/memcache/memcache_test.go`
- Modify: `go.mod` (add `github.com/bradfitz/gomemcache`)

**Interfaces:**
- Consumes: `cache.*` (Task 1); `cachetest.RunConformance`, `cachetest.RunTestMemcached` (Tasks 2, 5).
- Produces: `memcache.New(client *memcache.Client, opts ...Option) cache.Provider`; `memcache.WithKeyPrefix(p string) Option`. Caches implement `cache.Cache` only.

> Memcached keys must be ≤ 250 bytes and contain no control chars/spaces. Namespacing uses a prefix; the human-task token keys are safe. TTL maps to `Item.Expiration` (seconds; 0 = never).

- [ ] **Step 1: Write the failing test**

`persistence/cache/memcache/memcache_test.go`:
```go
package memcache_test

import (
	"testing"

	gomc "github.com/bradfitz/gomemcache/memcache"

	"github.com/kartaladev/wrkflw/persistence/cache"
	"github.com/kartaladev/wrkflw/persistence/cache/cachetest"
	"github.com/kartaladev/wrkflw/persistence/cache/memcache"
)

func TestMemcacheConformance(t *testing.T) {
	addr := cachetest.RunTestMemcached(t)
	cachetest.RunConformance(t, func() cache.Provider {
		client := gomc.New(addr)
		_ = client.DeleteAll()
		return memcache.New(client)
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go get github.com/bradfitz/gomemcache && go test ./persistence/cache/memcache/...`
Expected: FAIL — `undefined: memcache.New`.

- [ ] **Step 3: Write minimal implementation**

`persistence/cache/memcache/memcache.go`:
```go
// Package memcache is a distributed cache.Provider backed by
// github.com/bradfitz/gomemcache. It implements only the byte-oriented
// cache.Cache (no cache.ValueCache).
package memcache

import (
	"context"
	"errors"
	"time"

	gomc "github.com/bradfitz/gomemcache/memcache"

	"github.com/kartaladev/wrkflw/persistence/cache"
)

// Option configures the provider.
type Option func(*provider)

// WithKeyPrefix prefixes every key. Default "wrkflw:".
func WithKeyPrefix(p string) Option { return func(pr *provider) { pr.prefix = p } }

// New returns a distributed cache.Provider over client. The caller owns client.
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

func (p *provider) Cache(ns string) (cache.Cache, error) {
	if p.client == nil {
		return nil, cache.ErrNilCache
	}
	return &mcCache{client: p.client, prefix: p.prefix + ns + ":"}, nil
}

type mcCache struct {
	client *gomc.Client
	prefix string
}

func (c *mcCache) Get(_ context.Context, k string) ([]byte, bool, error) {
	item, err := c.client.Get(c.prefix + k)
	if errors.Is(err, gomc.ErrCacheMiss) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return item.Value, true, nil
}

func (c *mcCache) Set(_ context.Context, k string, v []byte, ttl time.Duration) error {
	return c.client.Set(&gomc.Item{Key: c.prefix + k, Value: v, Expiration: int32(ttl.Seconds())})
}

func (c *mcCache) Delete(_ context.Context, k string) error {
	err := c.client.Delete(c.prefix + k)
	if errors.Is(err, gomc.ErrCacheMiss) {
		return nil
	}
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./persistence/cache/memcache/...`
Expected: PASS (or SKIP without Docker).

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum persistence/cache/memcache/
git commit -m "feat(cache): gomemcache distributed adapter"
```

---

### Task 8: Relocate + re-substrate `CachingInstanceStore` into `persistence`

**Files:**
- Create: `persistence/caching_instance_store.go`
- Create: `persistence/caching_instance_store_test.go` (ported from `runtime/kernel/caching_store_test.go`, `caching_store_alwaysown_test.go`, `caching_store_example_test.go`)
- Reference (read for behavior parity): `runtime/kernel/caching_store.go`

**Interfaces:**
- Consumes: `cache.Provider`, `cache.NewCodec` (Task 1); `hotcache.New` (Task 3); `kernel.InstanceStore`, `kernel.JournalReader`, `kernel.InstanceOwnership`, `kernel.Version`, `kernel.AppliedStep`, `kernel.AlwaysOwn`, `kernel.ErrNilDependency`, `kernel.ErrConcurrentUpdate`; `engine.InstanceState`.
- Produces:
  - `persistence.CachingInstanceStore` (implements `kernel.InstanceStore` + `kernel.JournalReader`).
  - `persistence.NewCachingInstanceStore(backing kernel.InstanceStore, owner kernel.InstanceOwnership, provider cache.Provider, opts ...CachingInstanceStoreOption) (*CachingInstanceStore, error)`.
  - `persistence.CachingInstanceStoreOption`; `persistence.WithInstanceCacheTTL(d time.Duration) CachingInstanceStoreOption`; `persistence.WithInstanceCacheLogger(l *slog.Logger) CachingInstanceStoreOption`.

**Design:** keep ALL correctness-bearing logic from `runtime/kernel/caching_store.go` (ownership gate, per-instance refcounted keyed locks, evict-on-`ErrConcurrentUpdate`, `Release` evicts-first, one-time `AlwaysOwn` warn). Replace the `map + container/list + manual TTL + clock` storage with a `*cache.Codec[instanceEntry]` from `provider.Cache("instances")`. Drop `WithCacheMaxEntries` and `WithCachingStoreClock` (capacity/expiry are now the adapter's job).

- [ ] **Step 1: Write the failing test**

Copy the three kernel test files into `persistence/caching_instance_store_test.go` as `package persistence_test`, then adapt:
- construct via `persistence.NewCachingInstanceStore(backing, owner, hotcache.New(), persistence.WithInstanceCacheTTL(...))` (add the `provider` arg; TTL option renamed).
- replace `kernel.WithCacheTTL` → `persistence.WithInstanceCacheTTL`, `kernel.WithCacheLogger` → `persistence.WithInstanceCacheLogger`.
- keep the ownership-bypass, evict-on-conflict, keyed-serialization, and AlwaysOwn-warn assertions unchanged.

Add one new table test proving the substrate is pluggable (behaves identically on `hotcache` vs a byte-only provider). Minimal new assertion:
```go
func TestCachingInstanceStore_Substrates(t *testing.T) {
	providers := map[string]func() cache.Provider{
		"hotcache-value": func() cache.Provider { return hotcache.New() },
		"byte-only":      func() cache.Provider { return newByteOnlyProvider() }, // JSON path
	}
	for name, np := range providers {
		t.Run(name, func(t *testing.T) {
			backing := kernel.NewMemInstanceStore()
			cs, err := persistence.NewCachingInstanceStore(backing, kernel.AlwaysOwn{}, np())
			if err != nil {
				t.Fatalf("new: %v", err)
			}
			// Create -> Load served from cache -> value equals what was stored.
			// (Reuse the existing helper that builds an AppliedStep for a fresh instance.)
			assertCreateLoadCommit(t, cs)
		})
	}
}
```
(`newByteOnlyProvider` = the `mapProvider` shape from Task 2's test, copied locally; `assertCreateLoadCommit` = a small helper mirroring the existing kernel test's create/load/commit flow.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./persistence/... -run CachingInstanceStore`
Expected: FAIL — `undefined: persistence.NewCachingInstanceStore`.

- [ ] **Step 3: Write minimal implementation**

`persistence/caching_instance_store.go` — port `runtime/kernel/caching_store.go` with the substrate swap. Key structure:
```go
package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/persistence/cache"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

var (
	_ kernel.InstanceStore = (*CachingInstanceStore)(nil)
	_ kernel.JournalReader = (*CachingInstanceStore)(nil)
)

const defaultInstanceCacheTTL = 5 * time.Minute

// instanceEntry is the cached unit: the snapshot plus its optimistic version.
type instanceEntry struct {
	State   engine.InstanceState `json:"state"`
	Version kernel.Version       `json:"version"`
}

func cloneInstanceEntry(e instanceEntry) instanceEntry {
	return instanceEntry{State: e.State.Clone(), Version: e.Version}
}

func unmarshalInstanceEntry(b []byte) (instanceEntry, error) {
	var e instanceEntry
	return e, json.Unmarshal(b, &e)
}

// CachingInstanceStore is a write-through, ownership-gated cache in front of a
// durable kernel.InstanceStore (ADR-0020). Storage is delegated to a cache.Provider
// substrate (in-memory by default). See the package docs for the multi-replica
// contract (pair with an advisory-lock ownership; AlwaysOwn is single-replica).
type CachingInstanceStore struct {
	backing kernel.InstanceStore
	owner   kernel.InstanceOwnership
	codec   *cache.Codec[instanceEntry]
	logger  *slog.Logger
	ttl     time.Duration

	klMu     sync.Mutex
	keyLocks map[string]*keyLock
}

type keyLock struct {
	mu   sync.Mutex
	refs int
}

// CachingInstanceStoreOption configures a CachingInstanceStore.
type CachingInstanceStoreOption func(*CachingInstanceStore)

// WithInstanceCacheTTL sets the max age of a cached snapshot. Default 5m.
func WithInstanceCacheTTL(d time.Duration) CachingInstanceStoreOption {
	return func(c *CachingInstanceStore) { if d > 0 { c.ttl = d } }
}

// WithInstanceCacheLogger sets the logger for the one-time AlwaysOwn warning.
func WithInstanceCacheLogger(l *slog.Logger) CachingInstanceStoreOption {
	return func(c *CachingInstanceStore) { if l != nil { c.logger = l } }
}

// NewCachingInstanceStore wraps backing with an ownership-gated write-through cache
// whose storage comes from provider.Cache("instances").
func NewCachingInstanceStore(backing kernel.InstanceStore, owner kernel.InstanceOwnership, provider cache.Provider, opts ...CachingInstanceStoreOption) (*CachingInstanceStore, error) {
	if backing == nil {
		return nil, fmt.Errorf("%w: backing store", kernel.ErrNilDependency)
	}
	if owner == nil {
		return nil, fmt.Errorf("%w: owner", kernel.ErrNilDependency)
	}
	if provider == nil {
		return nil, fmt.Errorf("%w: cache provider", kernel.ErrNilDependency)
	}
	raw, err := provider.Cache("instances")
	if err != nil {
		return nil, err
	}
	codec, err := cache.NewCodec[instanceEntry](raw, json.Marshal, unmarshalInstanceEntry, cloneInstanceEntry)
	if err != nil {
		return nil, err
	}
	c := &CachingInstanceStore{
		backing:  backing,
		owner:    owner,
		codec:    codec,
		logger:   slog.Default(),
		ttl:      defaultInstanceCacheTTL,
		keyLocks: make(map[string]*keyLock),
	}
	for _, o := range opts {
		o(c)
	}
	if _, ok := owner.(kernel.AlwaysOwn); ok {
		c.logger.Warn("persistence: CachingInstanceStore paired with AlwaysOwn is single-replica only; " +
			"use persistence.NewAdvisoryLockOwnership for multi-replica deployments to avoid stale cached reads")
	}
	return c, nil
}
```
Port `lockFor`, `Create`, `Load`, `Commit`, `Release`, `Entries` verbatim from the kernel version, replacing the storage calls:
- `c.put(id, state, tok)` → `_ = c.codec.Set(ctx, id, instanceEntry{State: state, Version: tok}, c.ttl)`
- `c.get(id)` → `e, ok, _ := c.codec.Get(ctx, id)` then use `e.State`, `e.Version`
- `c.evict(id)` → `_ = c.codec.Delete(ctx, id)`
Keep the `Clone()` calls exactly where the kernel version had them (the value path already clones inside the codec; the extra defensive `Clone` on read return preserves current semantics — retain it to keep the ported tests green).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./persistence/... -run CachingInstanceStore`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add persistence/caching_instance_store.go persistence/caching_instance_store_test.go go.mod go.sum
git commit -m "feat(persistence): relocate CachingInstanceStore onto the cache substrate"
```

---

### Task 9: Remove `kernel.CachingInstanceStore` and repoint references

**Files:**
- Delete: `runtime/kernel/caching_store.go`, `runtime/kernel/caching_store_test.go`, `runtime/kernel/caching_store_alwaysown_test.go`, `runtime/kernel/caching_store_example_test.go`
- Modify: `runtime/internal/runtimetest/constructors.go` (repoint `MustCachingStore`)
- Modify: `examples/sqlite_wiring/main.go:195-196`, `examples/mysql_wiring/main.go:163`
- Modify (doc comments only): `runtime/kernel/ownership.go`, `persistence/persistence.go`, `persistence/sqlite.go`, `persistence/mysql.go`, `doc.go`

**Interfaces:**
- Consumes: `persistence.NewCachingInstanceStore`, `persistence.CachingInstanceStoreOption` (Task 8).
- Produces: no new symbols; removes `kernel.CachingInstanceStore`, `kernel.NewCachingInstanceStore`, and `kernel.WithCache*` options.

- [ ] **Step 1: Write the failing test (compile-red)**

Delete the four kernel caching files listed above, then run the build to surface every dangling reference:

Run: `go build ./...`
Expected: FAIL — `undefined: kernel.NewCachingInstanceStore` in `runtime/internal/runtimetest/constructors.go`, `examples/sqlite_wiring/main.go`, `examples/mysql_wiring/main.go`. (This red state IS the checklist of edits.)

- [ ] **Step 2: Repoint `runtimetest` helper**

In `runtime/internal/runtimetest/constructors.go`, change `MustCachingStore` to build the persistence type:
```go
// MustCachingStore builds a persistence.CachingInstanceStore or fails the test.
func MustCachingStore(t *testing.T, backing kernel.InstanceStore, owner kernel.InstanceOwnership, opts ...persistence.CachingInstanceStoreOption) *persistence.CachingInstanceStore {
	t.Helper()
	s, err := persistence.NewCachingInstanceStore(backing, owner, hotcache.New(), opts...)
	if err != nil {
		t.Fatalf("new caching store: %v", err)
	}
	return s
}
```
Add imports `github.com/kartaladev/wrkflw/persistence` and `github.com/kartaladev/wrkflw/persistence/cache/hotcache`. If any caller passed `kernel.WithCacheTTL`/`kernel.WithCacheMaxEntries`, translate to `persistence.WithInstanceCacheTTL` / drop max-entries (adapter concern).

> Verify no import cycle: `persistence` must not import `runtime/internal/runtimetest`. It does not. `runtime/internal/runtimetest` importing `persistence` is one-directional and legal.

- [ ] **Step 3: Repoint the two examples**

`examples/sqlite_wiring/main.go` (~line 196) and `examples/mysql_wiring/main.go` (~line 163): replace
`kernel.NewCachingInstanceStore(store, owner)` with
`persistence.NewCachingInstanceStore(store, owner, hotcache.New())`
and add the `hotcache` import. Update the nearby explanatory comments (`kernel.NewCachingInstanceStore` → `persistence.NewCachingInstanceStore`).

- [ ] **Step 4: Fix doc comments**

Update the doc-comment references `[kernel.CachingInstanceStore]` / `[kernel.NewCachingInstanceStore]` → `[CachingInstanceStore]` / `[NewCachingInstanceStore]` (or `persistence.`-qualified from outside) in `runtime/kernel/ownership.go`, `persistence/persistence.go`, `persistence/sqlite.go`, `persistence/mysql.go`, `doc.go`. These are comments only — no behavior change.

- [ ] **Step 5: Verify build + full tests pass**

Run: `go build ./... && go test ./runtime/... ./persistence/... ./examples/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor(runtime): remove kernel CachingInstanceStore; repoint to persistence"
```

---

### Task 10: New `persistence.CachingTaskStore` (human-task point-read cache)

**Files:**
- Create: `persistence/caching_task_store.go`
- Test: `persistence/caching_task_store_test.go`

**Interfaces:**
- Consumes: `cache.Provider`, `cache.NewCodec` (Task 1); `hotcache.New` (Task 3); `humantask.TaskStore`, `humantask.HumanTask`, `humantask.ErrTaskNotFound`, `humantask.NewMemTaskStore`; `authz.Actor`.
- Produces:
  - `persistence.CachingTaskStore` (implements `humantask.TaskStore`).
  - `persistence.NewCachingTaskStore(backing humantask.TaskStore, provider cache.Provider, opts ...CachingTaskStoreOption) (*CachingTaskStore, error)`.
  - `persistence.CachingTaskStoreOption`; `persistence.WithHumanTaskCacheTTL(d time.Duration) CachingTaskStoreOption`.

**Design:** cache `Get(token)` read-through; write-through refresh on `Upsert(token)`; never cache `ErrTaskNotFound`; pass `AssignedTo`/`ClaimableBy` straight through (uncached, per spec).

- [ ] **Step 1: Write the failing test**

`persistence/caching_task_store_test.go`:
```go
package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/persistence"
	"github.com/kartaladev/wrkflw/persistence/cache/hotcache"
)

// countingStore counts backing Get calls to prove cache hits skip the backing.
type countingStore struct {
	*humantask.MemTaskStore
	gets int
}

func (s *countingStore) Get(ctx context.Context, token string) (humantask.HumanTask, error) {
	s.gets++
	return s.MemTaskStore.Get(ctx, token)
}

func TestCachingTaskStore(t *testing.T) {
	tests := []struct {
		name   string
		assert func(t *testing.T, cs *persistence.CachingTaskStore, backing *countingStore)
	}{
		{
			name: "second Get is a cache hit",
			assert: func(t *testing.T, cs *persistence.CachingTaskStore, backing *countingStore) {
				ctx := t.Context()
				_ = cs.Upsert(ctx, humantask.HumanTask{TaskToken: "t1", State: humantask.Unclaimed})
				if _, err := cs.Get(ctx, "t1"); err != nil {
					t.Fatalf("get1: %v", err)
				}
				before := backing.gets
				if _, err := cs.Get(ctx, "t1"); err != nil {
					t.Fatalf("get2: %v", err)
				}
				if backing.gets != before {
					t.Fatalf("expected cache hit, backing.gets went %d -> %d", before, backing.gets)
				}
			},
		},
		{
			name: "Upsert refreshes the cached entry (write-through)",
			assert: func(t *testing.T, cs *persistence.CachingTaskStore, backing *countingStore) {
				ctx := t.Context()
				_ = cs.Upsert(ctx, humantask.HumanTask{TaskToken: "t1", State: humantask.Unclaimed})
				_, _ = cs.Get(ctx, "t1")
				_ = cs.Upsert(ctx, humantask.HumanTask{TaskToken: "t1", State: humantask.Claimed, ClaimedBy: "alice"})
				got, err := cs.Get(ctx, "t1")
				if err != nil {
					t.Fatalf("get: %v", err)
				}
				if got.State != humantask.Claimed || got.ClaimedBy != "alice" {
					t.Fatalf("stale after upsert: %+v", got)
				}
			},
		},
		{
			name: "not-found is not cached",
			assert: func(t *testing.T, cs *persistence.CachingTaskStore, backing *countingStore) {
				ctx := t.Context()
				if _, err := cs.Get(ctx, "missing"); err != humantask.ErrTaskNotFound {
					t.Fatalf("want ErrTaskNotFound, got %v", err)
				}
				before := backing.gets
				_, _ = cs.Get(ctx, "missing")
				if backing.gets == before {
					t.Fatal("not-found must NOT be cached; backing should be hit again")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backing := &countingStore{MemTaskStore: humantask.NewMemTaskStore()}
			cs, err := persistence.NewCachingTaskStore(backing, hotcache.New(), persistence.WithHumanTaskCacheTTL(time.Minute))
			if err != nil {
				t.Fatalf("new: %v", err)
			}
			tt.assert(t, cs, backing)
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./persistence/... -run CachingTaskStore`
Expected: FAIL — `undefined: persistence.NewCachingTaskStore`.

- [ ] **Step 3: Write minimal implementation**

`persistence/caching_task_store.go`:
```go
package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"time"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/persistence/cache"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

var _ humantask.TaskStore = (*CachingTaskStore)(nil)

const defaultHumanTaskCacheTTL = 30 * time.Second

// CachingTaskStore caches point reads (Get by token) of a humantask.TaskStore.
// Reads are read-through; Upsert writes through (refreshing the cached entry).
// AssignedTo / ClaimableBy are set-wide queries and are NOT cached (they pass
// straight through). With an in-memory provider point reads are coherent only
// single-replica; use a distributed provider (rediscache/memcache) for
// multi-replica coherence.
type CachingTaskStore struct {
	backing humantask.TaskStore
	codec   *cache.Codec[humantask.HumanTask]
	ttl     time.Duration
}

// CachingTaskStoreOption configures a CachingTaskStore.
type CachingTaskStoreOption func(*CachingTaskStore)

// WithHumanTaskCacheTTL sets the max age of a cached task. Default 30s.
func WithHumanTaskCacheTTL(d time.Duration) CachingTaskStoreOption {
	return func(c *CachingTaskStore) { if d > 0 { c.ttl = d } }
}

// NewCachingTaskStore wraps backing with a point-read cache from
// provider.Cache("humantasks").
func NewCachingTaskStore(backing humantask.TaskStore, provider cache.Provider, opts ...CachingTaskStoreOption) (*CachingTaskStore, error) {
	if backing == nil {
		return nil, fmt.Errorf("%w: backing task store", kernel.ErrNilDependency)
	}
	if provider == nil {
		return nil, fmt.Errorf("%w: cache provider", kernel.ErrNilDependency)
	}
	raw, err := provider.Cache("humantasks")
	if err != nil {
		return nil, err
	}
	codec, err := cache.NewCodec[humantask.HumanTask](raw, json.Marshal, unmarshalTask, cloneTask)
	if err != nil {
		return nil, err
	}
	c := &CachingTaskStore{backing: backing, codec: codec, ttl: defaultHumanTaskCacheTTL}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

func (s *CachingTaskStore) Get(ctx context.Context, token string) (humantask.HumanTask, error) {
	if t, ok, err := s.codec.Get(ctx, token); err == nil && ok {
		return t, nil
	}
	t, err := s.backing.Get(ctx, token)
	if err != nil {
		return humantask.HumanTask{}, err // do not cache misses / ErrTaskNotFound
	}
	_ = s.codec.Set(ctx, token, t, s.ttl)
	return t, nil
}

func (s *CachingTaskStore) Upsert(ctx context.Context, t humantask.HumanTask) error {
	if err := s.backing.Upsert(ctx, t); err != nil {
		return err
	}
	_ = s.codec.Set(ctx, t.TaskToken, t, s.ttl) // write-through refresh
	return nil
}

func (s *CachingTaskStore) AssignedTo(ctx context.Context, actorID string) ([]humantask.HumanTask, error) {
	return s.backing.AssignedTo(ctx, actorID)
}

func (s *CachingTaskStore) ClaimableBy(ctx context.Context, actor authz.Actor) ([]humantask.HumanTask, error) {
	return s.backing.ClaimableBy(ctx, actor)
}

func unmarshalTask(b []byte) (humantask.HumanTask, error) {
	var t humantask.HumanTask
	return t, json.Unmarshal(b, &t)
}

// cloneTask deep-copies the mutable fields so a cached live value cannot be
// aliased by callers (value-path only; the byte path round-trips via JSON).
func cloneTask(t humantask.HumanTask) humantask.HumanTask {
	t.Candidates = append([]string(nil), t.Candidates...)
	t.Eligibility.Roles = append([]string(nil), t.Eligibility.Roles...)
	t.Eligibility.Privileges = append([]string(nil), t.Eligibility.Privileges...)
	if t.Vars != nil {
		t.Vars = maps.Clone(t.Vars)
	}
	return t
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./persistence/... -run CachingTaskStore`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add persistence/caching_task_store.go persistence/caching_task_store_test.go
git commit -m "feat(persistence): human-task point-read cache (CachingTaskStore)"
```

---

### Task 11: Default-on cache wiring on the `DurableProvider` constructors

**Files:**
- Create: `persistence/durableprovider_cache.go`
- Modify: `persistence/durableprovider.go` (add `opts ...DurableOption` to the three constructors; wrap `is` + `tasks`)
- Test: `persistence/durableprovider_cache_test.go`

**Interfaces:**
- Consumes: `cache.Provider` (Task 1); `hotcache.New` (Task 3); `NewCachingInstanceStore` (Task 8); `NewCachingTaskStore` (Task 10); `kernel.InstanceOwnership`, `kernel.AlwaysOwn`.
- Produces:
  - `persistence.DurableOption`.
  - `persistence.WithCacheProvider(p cache.Provider) DurableOption` (sets BOTH the instance and human-task substrate — the single simple knob).
  - `persistence.WithInstanceCacheProvider(p cache.Provider) DurableOption`, `persistence.WithHumanTaskCacheProvider(p cache.Provider) DurableOption`.
  - `persistence.WithDurableInstanceCacheOwnership(o kernel.InstanceOwnership) DurableOption`.
  - `persistence.WithDurableInstanceCacheTTL(d time.Duration) DurableOption`, `persistence.WithDurableHumanTaskCacheTTL(d time.Duration) DurableOption`.
  - `persistence.WithoutCache() DurableOption`.
  - Updated signatures: `NewDurableProvider(ctx, pool, opts ...DurableOption)`, `NewMySQLDurableProvider(ctx, db, opts ...DurableOption)`, `NewSQLiteDurableProvider(ctx, db, opts ...DurableOption)`.

- [ ] **Step 1: Write the failing test**

`persistence/durableprovider_cache_test.go` (in-memory only — exercises option plumbing without a DB by testing the config + wrap helper directly):
```go
package persistence

import (
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/persistence/cache/hotcache"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

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

func TestWrapCachingWrapsBothStores(t *testing.T) {
	cfg := defaultDurableConfig()
	is := kernel.NewMemInstanceStore()
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
	is := kernel.NewMemInstanceStore()
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
```
(This is a white-box `package persistence` test so it can see `defaultDurableConfig`, `durableConfig.wrapCaching`, and the option funcs.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./persistence/... -run 'DurableCache|WithoutCache|WithCacheProvider|WrapCaching'`
Expected: FAIL — `undefined: defaultDurableConfig`.

- [ ] **Step 3: Write minimal implementation**

`persistence/durableprovider_cache.go`:
```go
package persistence

import (
	"log/slog"
	"time"

	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/persistence/cache"
	"github.com/kartaladev/wrkflw/persistence/cache/hotcache"
	"github.com/kartaladev/wrkflw/runtime/kernel"
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
// for both kinds, AlwaysOwn ownership (single-replica; emits a one-time warn),
// 5m instance TTL, 30s human-task TTL.
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

// WithCacheProvider sets the substrate for BOTH the instance and human-task caches.
// A nil provider is ignored (defaults remain).
func WithCacheProvider(p cache.Provider) DurableOption {
	return func(c *durableConfig) {
		if p != nil {
			c.instanceProvider = p
			c.humanTaskProvider = p
		}
	}
}

// WithInstanceCacheProvider overrides only the instance-cache substrate.
func WithInstanceCacheProvider(p cache.Provider) DurableOption {
	return func(c *durableConfig) { if p != nil { c.instanceProvider = p } }
}

// WithHumanTaskCacheProvider overrides only the human-task-cache substrate
// (use a distributed provider for multi-replica coherence).
func WithHumanTaskCacheProvider(p cache.Provider) DurableOption {
	return func(c *durableConfig) { if p != nil { c.humanTaskProvider = p } }
}

// WithDurableInstanceCacheOwnership sets the ownership gate for the instance cache.
// Supply persistence.NewAdvisoryLockOwnership for multi-replica deployments.
func WithDurableInstanceCacheOwnership(o kernel.InstanceOwnership) DurableOption {
	return func(c *durableConfig) { if o != nil { c.instanceOwnership = o } }
}

// WithDurableInstanceCacheTTL sets the instance-cache TTL. Default 5m.
func WithDurableInstanceCacheTTL(d time.Duration) DurableOption {
	return func(c *durableConfig) { if d > 0 { c.instanceTTL = d } }
}

// WithDurableHumanTaskCacheTTL sets the human-task-cache TTL. Default 30s.
func WithDurableHumanTaskCacheTTL(d time.Duration) DurableOption {
	return func(c *durableConfig) { if d > 0 { c.humanTaskTTL = d } }
}

// WithoutCache disables all caching; stores are used unwrapped.
func WithoutCache() DurableOption {
	return func(c *durableConfig) { c.cacheEnabled = false }
}

// wrapCaching wraps is/ts per the config, or returns them unchanged when disabled.
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
```

Then modify `persistence/durableprovider.go`. For EACH of the three constructors, add `opts ...DurableOption`, build the config, and wrap `is`+`tasks` before assembling the struct. Example for `NewDurableProvider` (apply the identical shape to MySQL + SQLite):
```go
func NewDurableProvider(ctx context.Context, pool *pgxpool.Pool, opts ...DurableOption) (*DurableProvider, error) {
	cfg := applyDurableOptions(opts)
	is, err := OpenPostgres(ctx, pool)
	if err != nil {
		return nil, err
	}
	// ... defs, lister, tasks, timers, links (unchanged) ...
	is, tasks, err = cfg.wrapCaching(is, tasks)
	if err != nil {
		return nil, err
	}
	return &DurableProvider{
		instanceStore: is,
		definitions:   defs,
		lister:        lister,
		taskStore:     tasks,
		timerStore:    timers,
		callLinkStore: links,
	}, nil
}
```
> Note: `is` is declared as `InstanceStore` (the façade interface) from `OpenPostgres`; `wrapCaching` takes/returns `kernel.InstanceStore`. Since `InstanceStore` embeds `kernel.InstanceStore` + `kernel.JournalReader`, and `CachingInstanceStore` implements both, assign through a local `kernel.InstanceStore` variable and store it in the struct field (which is typed `kernel.InstanceStore`). Adjust the variable types so it compiles; the struct field `instanceStore kernel.InstanceStore` already accepts it.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./persistence/... -run 'DurableCache|WithoutCache|WithCacheProvider|WrapCaching' && go build ./...`
Expected: PASS + build clean.

- [ ] **Step 5: Commit**

```bash
git add persistence/durableprovider_cache.go persistence/durableprovider.go persistence/durableprovider_cache_test.go
git commit -m "feat(persistence): default-on cache wiring for DurableProvider"
```

---

### Task 12: ADR-0099 + docs

**Files:**
- Create: `docs/adr/0099-persistence-caching-refactor.md`
- Modify: `doc.go` (mention the new cache packages in the surface list, if it enumerates packages)
- Modify: `CHANGELOG.md` (add an entry under Unreleased)

**Interfaces:** none (docs only).

- [ ] **Step 1: Write the ADR (Nygard template)**

`docs/adr/0099-persistence-caching-refactor.md` with sections **Status** (Accepted, 2026-07-06), **Context**, **Decision**, **Consequences**. Content must state:
- Context: caching was hand-rolled in `runtime/kernel` and only partially owned by persistence; project rule requires caching to be part of persistence; goal to stop reinventing eviction/TTL and gain a distributed path.
- Decision: neutral `persistence/cache.Cache` port + optional `ValueCache` capability + `Provider` factory + generic `Codec[V]`; four adapters (`hotcache` default, `ottercache`, `rediscache`, `memcache`); `CachingInstanceStore` relocated into `persistence` and re-substrated (behavior preserved incl. ownership gate + AlwaysOwn warn); new `CachingTaskStore` point-read cache; default-on wiring on `DurableProvider` (hotcache, AlwaysOwn+warn, 5m/30s TTLs); `WithoutCache` escape hatch. Definition cache deferred; human-task query caching deferred.
- Consequences: positive (persistence-owned caching, maintained libraries, distributed path, zero-config default); negative (breaking `NewCachingInstanceStore` signature — pre-v0.1.0; human-task in-mem cache single-replica-coherent only; four optional deps isolated in subpackages); note that instance cache under a distributed substrate is self-healing via version-CAS but offers no cross-replica benefit under ownership.

- [ ] **Step 2: Update CHANGELOG + doc.go**

Add a CHANGELOG "Added/Changed" entry summarizing the cache port, adapters, and default-on caching, plus the `NewCachingInstanceStore` signature change under "Breaking". If `doc.go` lists the public packages, add `persistence/cache` and its adapters.

- [ ] **Step 3: Commit**

```bash
git add docs/adr/0099-persistence-caching-refactor.md CHANGELOG.md doc.go
git commit -m "docs(adr): 0099 persistence caching refactor"
```

---

### Task 13: Final verification

**Files:** none (verification only; fix-forward any failures with a follow-up commit).

- [ ] **Step 1: Full race + coverage**

Run: `go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1`
Expected: PASS, no data races. Confirm each touched package ≥ 85% (`go tool cover -func=cover.out | grep -E 'persistence/cache|caching_'`). If a package is under 85%, add the missing case as a new red→green cycle.

- [ ] **Step 2: Lint**

Run: `golangci-lint run ./...`
Expected: no findings. Fix any and re-run.

- [ ] **Step 3: Extraction / cycle check**

Run: `go build ./... && go vet ./...`
Expected: clean. Confirm no core package imports a cache library:
`! grep -rn "samber/hot\|maypok86/otter\|redis/go-redis\|bradfitz/gomemcache" persistence/*.go persistence/cache/cache.go persistence/cache/codec.go`
Expected: no matches (adapters isolate those imports).

- [ ] **Step 4: Examples build**

Run: `go build ./examples/...`
Expected: clean.

- [ ] **Step 5: Commit any fixes**

```bash
git add -A
git commit -m "test(persistence): close coverage/lint gaps for cache refactor"
```

---

## Self-Review

**Spec coverage:**
- Cache port + `ValueCache` + `Provider` + `Codec` → Task 1. ✓
- Four adapters (hotcache/ottercache/rediscache/memcache), subpackage-isolated → Tasks 3,4,6,7. ✓
- Shared conformance + testcontainers → Tasks 2,5. ✓
- Instance cache relocated + re-substrated, behavior preserved → Tasks 8,9. ✓
- New human-task point-read cache; queries uncached → Task 10. ✓
- Default-on `DurableProvider` wiring, `WithCacheProvider`/`WithoutCache`, AlwaysOwn+warn, sensible TTLs → Task 11. ✓
- Definition cache untouched (not in any task) ✓; human-task query caching deferred (documented in Tasks 10, 12) ✓.
- ADR-0099 + docs → Task 12. ✓
- TDD-strict, testcontainers, coverage/lint gates → every task + Task 13. ✓

**Placeholder scan:** No "TBD"/"add error handling"/"similar to Task N" — every code step shows real code. Library-API "confirm via go doc" notes accompany full best-effort code (third-party surface), not placeholders.

**Type consistency:** `cache.Cache`/`ValueCache`/`Provider`/`NewCodec` names are stable across Tasks 1–11. `instanceEntry`/`cloneInstanceEntry`/`unmarshalInstanceEntry` (Task 8) reused nowhere else. `NewCachingInstanceStore(backing, owner, provider, opts...)` signature consistent in Tasks 8, 9, 11. `NewCachingTaskStore(backing, provider, opts...)` consistent in Tasks 10, 11. `DurableOption` + `wrapCaching` + `defaultDurableConfig` consistent within Task 11. Option names (`WithInstanceCacheTTL`, `WithHumanTaskCacheTTL`, `WithCacheProvider`, `WithoutCache`) stable.
