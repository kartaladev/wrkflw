# Persistence Caching Refactor — Design Spec

- **Date:** 2026-07-06
- **Status:** Approved (pending implementation)
- **ADR:** 0099 (to be written alongside implementation)
- **Slug:** persistence-caching-refactor

## Problem

Caching is currently hand-rolled and scattered outside the persistence layer:

- `runtime/kernel/caching_store.go` — `CachingInstanceStore`: write-through,
  single-writer, `container/list` LRU + manual TTL + per-instance keyed locks,
  ownership-gated (ADR-0020/0054).
- `runtime/kernel/caching_definition_registry.go` — `CachingDefinitionRegistry`:
  read-through TTL + `singleflight`.

Two problems motivate this refactor:

1. **Ownership / placement.** The project rule is that *all caching mechanisms
   are part of persistence*. Today the caches live in `runtime/kernel` and are
   only partially surfaced through the `persistence` façade.
2. **Reinvention.** The eviction/TTL machinery is hand-rolled with
   `container/list` + `sync`. We want to stop maintaining cache internals and
   delegate to popular, well-maintained cache libraries — ideally with a path to
   **distributed** caching — behind a neutral port, with **2+ swappable
   adapters**.

The design also applies the project's **sensible-default** principle
(opinionated out-of-the-box behavior, fully overridable): zero-config gives a
working in-memory cache; any piece is replaceable via an option.

## Scope

**In scope**

- A neutral `Cache` **port** + a `Provider` factory, plus a generic `Codec`
  helper, all owned by the persistence layer.
- **Four adapters** (each in its own subpackage so heavy deps stay optional):
  - `hotcache` — `github.com/samber/hot` (in-memory, **default provider**).
  - `ottercache` — `github.com/maypok86/otter` v2 (in-memory alternative).
  - `rediscache` — `github.com/redis/go-redis/v9` (distributed).
  - `memcache` — `github.com/bradfitz/gomemcache` (distributed, non-Redis).
- **Instance-state cache**: relocate `CachingInstanceStore` into `persistence`
  and re-substrate its storage onto the `Cache` port. All correctness-bearing
  behavior is preserved.
- **Human-task cache**: a NEW `CachingTaskStore` decorator over
  `humantask.TaskStore` (point-read caching).
- Wiring on the `DurableProvider` constructors (default-on, opinionated),
  plus public decorator constructors for hand-wiring the `Open*` path.
- ADR-0099 (Nygard template).

**Out of scope (this iteration)**

- The **definition** store/registry cache. Keep the existing in-memory default
  (`NewMemDefinitionRegistry`) and `CachingDefinitionRegistry` unchanged.
  Definitions are immutable per `(defID, version)`; a distributed definition
  cache is a natural but non-urgent follow-up.
- Query-result caching for `AssignedTo` / `ClaimableBy` (documented as a future
  opt-in flag only).

## Approach

### The `Cache` port (byte substrate + optional value capability — "approach C")

Package `persistence/cache` is **pure** (imports only stdlib + `clock`; no
`kernel`/`engine`/`humantask` dependency).

```go
package cache

// Cache is the byte-oriented substrate every adapter implements.
type Cache interface {
    Get(ctx context.Context, key string) ([]byte, bool, error)
    Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
    Delete(ctx context.Context, key string) error
}

// ValueCache is an OPTIONAL capability that in-process adapters may implement to
// store live values without serialization. The Codec type-asserts it and, when
// present, skips (un)marshaling on the hot path. Mirrors the existing
// dialect.Notifier / dialect.Locker capability-interface pattern.
type ValueCache interface {
    GetValue(ctx context.Context, key string) (any, bool, error)
    SetValue(ctx context.Context, key string, v any, ttl time.Duration) error
    Delete(ctx context.Context, key string) error
}

// Provider builds namespaced caches. A store calls it once per cache-kind
// (e.g. "instances", "humantasks"), so a consumer never hand-builds cache
// instances — they supply one Provider and the store wires the rest.
type Provider interface {
    Cache(namespace string) (Cache, error)
}
```

**Value representation.** A generic `Codec[V any]` in `persistence/cache` wraps a
`Cache`:

- If the underlying cache implements `ValueCache` (in-mem adapters), the codec
  stores/returns **live cloned values** — zero serialization, preserving today's
  hot-path performance for the instance cache.
- Otherwise (distributed adapters) it **marshals to JSON bytes**. Decorators own
  the concrete type `V` and the clone function.

This keeps adapters trivially uniform (a distributed adapter only implements the
3-method byte `Cache`) while avoiding an in-memory serialization regression.

### Package layout

```
persistence/cache/                      port (Cache, ValueCache, Provider) + Codec[V]  (pure)
persistence/cache/hotcache/             samber/hot        adapter  (default)
persistence/cache/ottercache/           maypok86/otter    adapter
persistence/cache/rediscache/           go-redis          adapter
persistence/cache/memcache/             gomemcache        adapter
persistence/caching_instance_store.go   CachingInstanceStore  (relocated from runtime/kernel)
persistence/caching_task_store.go       CachingTaskStore      (new)
```

Both decorators live in the root `persistence` package (which already imports
`kernel`, `humantask`, `engine`). Import direction is acyclic:
`persistence → {cache, kernel, humantask, engine}`; each adapter `→ cache` only.

### Instance cache — behavior-preserving re-substrate

`CachingInstanceStore` retains **all** correctness-bearing logic from the current
`runtime/kernel` implementation:

- Ownership gate (`kernel.InstanceOwnership`): only owned instances are
  cached/served; non-owned instances bypass the cache and read the backing store.
- Per-instance refcounted keyed locks serializing `Load`-populate vs `Commit`.
- Eviction on `ErrConcurrentUpdate` (stale version).
- One-time `Warn` when paired with `kernel.AlwaysOwn` (single-replica footgun,
  ADR-0054).
- `Release` evicts before delegating to `owner.Release`.

**Only the storage substrate changes**: the internal `map + container/list +
manual TTL` is replaced by a `Codec[instanceEntry]` (where
`instanceEntry{State engine.InstanceState; Version kernel.Version}`) over the
`Cache` returned by `provider.Cache("instances")`. LRU / size / TTL eviction is
now the **adapter's** responsibility (samber/hot and otter do this natively;
Redis/memcached via per-key TTL). The per-process keyed locks remain correct
because cross-replica exclusivity is guaranteed by ownership.

**Multi-replica safety.** Unchanged from ADR-0020: pair with a real advisory-lock
ownership (`persistence.NewAdvisoryLockOwnership`) for multi-replica. A
distributed substrate additionally introduces a failover-staleness window (a new
owner may read a previous owner's cached entry); this is **self-healing** — a
stale read leads to a `Commit` that fails `ErrConcurrentUpdate`, which evicts and
retries. TTL bounds it.

### Human-task cache — new decorator

`CachingTaskStore` wraps `humantask.TaskStore`:

- `Get(taskToken)` — **read-through** (populate on miss) + **write-through**
  (refresh the entry on `Upsert(t)` keyed by `t.TaskToken`). Cached.
- `Upsert(t)` — delegate to backing, then refresh/evict the cached entry for
  `t.TaskToken`.
- `AssignedTo(actorID)` / `ClaimableBy(actor)` — **pass through, uncached** in
  v1. These are set-wide queries whose results any `Upsert` can invalidate;
  coherent caching would require broad eviction or short-TTL staleness. Recorded
  as a future opt-in (`WithHumanTaskQueryCache` flag) — NOT built now.

**Coherence note (differs from the instance cache).** Human tasks have no
single-writer ownership. Therefore:

- **In-memory** provider: point reads are coherent only **single-replica**;
  across replicas a `Get` can be stale up to the TTL after another replica's
  `Upsert`. Default TTL is short and bounded.
- **Distributed** provider (write-through updates the shared entry): the coherent
  choice for multi-replica; a read on any replica sees the latest `Upsert`.

Mutations (`Upsert`) always hit the backing store — the cache never serves a
mutation decision — so the store remains authoritative for claim/complete.

### Wiring + sensible defaults

Primary surface is the `DurableProvider` constructors (they already assemble the
durable graph including `InstanceStore()` and `TaskStore()`). New `DurableOption`s
are additive (existing `NewDurableProvider(ctx, pool)` calls keep compiling):

```go
p, err := persistence.NewDurableProvider(ctx, pool,
    persistence.WithCacheProvider(rediscache.New(client)), // default: hotcache.New()
    persistence.WithInstanceCacheOwnership(owner),         // default: kernel.AlwaysOwn{} + one-time Warn
    persistence.WithInstanceCacheTTL(5*time.Minute),       // default: 5m
    persistence.WithHumanTaskCacheTTL(30*time.Second),     // default: short/bounded
    persistence.WithoutCache(),                            // escape hatch: disable entirely
)
```

`p.InstanceStore()` and `p.TaskStore()` return the **already cache-wrapped**
stores. Equivalent options exist on `NewMySQLDurableProvider` and
`NewSQLiteDurableProvider`.

**Zero-config default** (opinionated-on): caching **enabled**, `hotcache`
in-memory provider, instance cache gated by `kernel.AlwaysOwn{}` with the existing
one-time multi-replica `Warn`. Everything is overridable; `WithoutCache()`
disables it.

For consumers who hand-wire the single-store `Open*` path, the decorators are
exposed as public constructors:

```go
func NewCachingInstanceStore(backing InstanceStore, owner kernel.InstanceOwnership,
    provider cache.Provider, opts ...CachingInstanceStoreOption) (*CachingInstanceStore, error)
func NewCachingTaskStore(backing humantask.TaskStore,
    provider cache.Provider, opts ...CachingTaskStoreOption) (*CachingTaskStore, error)
```

## Components (units & boundaries)

| Unit | Responsibility | Depends on |
|---|---|---|
| `cache.Cache` / `cache.ValueCache` / `cache.Provider` | Neutral substrate + factory contracts | stdlib |
| `cache.Codec[V]` | Value/byte (de)serialization over a `Cache`, TTL passthrough | `cache`, `clock` |
| `hotcache` / `ottercache` | In-mem adapters implementing `Cache` **and** `ValueCache` | resp. lib |
| `rediscache` / `memcache` | Distributed adapters implementing `Cache` | resp. lib |
| `CachingInstanceStore` | Ownership-gated write-through instance cache | `cache`, `kernel`, `engine` |
| `CachingTaskStore` | Point-read human-task cache | `cache`, `humantask` |
| `DurableProvider` options | Default-on wiring of the above into the durable graph | `persistence`, `cache/hotcache` |

## Testing strategy

- **TDD strict** (CLAUDE.md): a visible failing test precedes every new exported
  symbol — the port, the `Codec`, each adapter, each decorator, and each option.
- **Adapter conformance**: one shared table-driven suite (per the `table-test`
  skill's `assert` closure form) exercised against all four adapters. In-mem
  adapters run directly; `rediscache` / `memcache` provision real servers via
  **testcontainers** (`use-testcontainers` skill), never mocked.
- **TTL/expiry**: driven by an injected `clock.Clock` fake — no wall-clock reads.
- **Instance cache**: port the existing `caching_store*_test.go` scenarios
  (ownership bypass, evict-on-conflict, keyed serialization, AlwaysOwn warn) onto
  the new substrate to prove behavior preservation.
- **Human-task cache**: point-read hit/miss, write-through refresh on `Upsert`,
  query methods bypass, single-replica coherence, TTL-bounded staleness.
- Coverage ≥ 85% per touched package; `go test ./...` and
  `golangci-lint run ./...` clean.

## Migration / blast radius

- Relocate `CachingInstanceStore` (+ its tests) from `runtime/kernel` to
  `persistence`. Repoint the ~3 real references found:
  `runtime/internal/runtimetest/constructors.go` (`MustCachingStore` helper),
  `examples/sqlite_wiring/main.go`, `examples/mysql_wiring/main.go`, plus doc
  comments in `persistence/{persistence,sqlite,mysql}.go` and
  `runtime/kernel/ownership.go`.
- `NewCachingInstanceStore` gains a `provider cache.Provider` parameter
  (breaking, pre-v0.1.0 — acceptable, documented in ADR-0099).
- New optional dependencies: `samber/hot`, `maypok86/otter`, `redis/go-redis/v9`,
  `bradfitz/gomemcache` — isolated in adapter subpackages so consumers pull only
  the deps they use.

## Consequences

**Positive**

- Caching is owned and wired by the persistence layer, per project rule.
- Cache internals are delegated to maintained libraries; a distributed path
  exists via `rediscache` / `memcache`.
- Zero-config caching (sensible default), any layer swappable via one option.
- The port makes additional adapters (e.g. two-tier, memcached alternatives)
  trivial.

**Negative / risks**

- Breaking constructor signature for `NewCachingInstanceStore` (mitigated: pre
  v0.1.0; wrapper + docs).
- Human-task in-mem cache is single-replica-coherent only; multi-replica needs a
  distributed provider or accepts TTL-bounded staleness (documented, opinionated
  short TTL).
- Four new optional dependencies (mitigated by subpackage isolation).

## Open questions (resolved)

- Distributed scope → definitions deferred; instance + human-task in scope.
- Human-task cache scope → point reads only (default); query caching deferred.
- Port shape → approach C (byte port + optional `ValueCache`).
- Adapter libraries → `samber/hot` (default) + `maypok86/otter` (in-mem);
  `go-redis` + `gomemcache` (distributed).
- Default behavior → caching on by default with `AlwaysOwn` + warn.
- Decorator placement → relocated into `persistence`.
