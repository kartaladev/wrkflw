# 0099. Persistence caching refactor: neutral `cache` port, four adapters, default-on `DurableProvider` wiring

Status: **Accepted — 2026-07-06.**
Spec: `docs/specs/2026-07-06-persistence-caching-refactor.md`.
Plan: `docs/plans/2026-07-06-persistence-caching-refactor.md`.
Follows: [ADR-0081](0081-store-unification-dialect.md) (neutral store + dialect), [ADR-0098](0098-service-coherent-graph-refactor.md) (durable `TaskStore`). Closes the caching-placement gap noted in [ADR-0020](0020-owned-instance-state-cache.md) (ownership-gated instance cache) and [ADR-0054](0054-graceful-shutdown-and-health.md) (graceful shutdown, health handlers, and `AlwaysOwn` caching guard).

## Context

Caching for process instances was hand-rolled in `runtime/kernel/caching_store.go` using a `container/list` LRU, manual TTL tracking, and per-instance keyed locks. This had two problems:

1. **Wrong ownership / placement.** The project rule is that *all caching mechanisms are part of the persistence layer*. The instance cache lived in `runtime/kernel` — adjacent to the engine core, not the persistence façade — and was only partially surfaced through the `persistence` façade. The newly added SQL `TaskStore` (ADR-0098) had no cache at all.
2. **Reinvention and maintenance burden.** The eviction/TTL machinery was hand-rolled with `container/list` + `sync`. Delegating to well-maintained cache libraries and adding a **distributed** path (Redis, Memcached) required a neutral port and swappable adapters — neither existed.

The project's *sensible-default* principle (opinionated out-of-the-box, fully overridable) applied here: zero-config should give a working in-memory cache; every piece must be replaceable via a `DurableOption`.

## Decision

### D1 — Neutral `persistence/cache` port: `Cache`, `ValueCache`, `Provider`, `Codec[V]`

Package `persistence/cache` is pure (stdlib + `cache` only; no engine/humantask/kernel dependency):

- `Cache` — a 3-method byte-oriented substrate every adapter implements (`Get`/`Set`/`Delete`).
- `ValueCache` — an *optional* capability that in-process adapters may implement to store live values without serialization. `Codec[V]` type-asserts it and, when present, skips (un)marshaling on the hot path. Mirrors the existing `dialect.Notifier`/`Locker` optional-capability pattern (ADR-0081).
- `Provider` — a factory a consumer supplies via `DurableOption`; a store calls `Provider.Cache(namespace)` once per cache-kind (e.g. `"instances"`, `"humantasks"`) to obtain its `Cache`.
- `Codec[V any]` — a generic typed layer over a `Cache`. When the underlying cache implements `ValueCache` (in-memory adapters), `Codec` stores/returns live cloned values (zero serialization). Otherwise it marshals/unmarshals using caller-supplied functions. Decorators supply a `clone` func to prevent aliasing.

### D2 — Four adapter subpackages (dep isolation)

Each adapter lives in its own subpackage so its library dependency is optional (consumers pull only what they use). All four satisfy `cache.Cache`; in-memory adapters additionally satisfy `cache.ValueCache`:

| Subpackage | Library | Substrate | Implements `ValueCache` | Notes |
|---|---|---|---|---|
| `persistence/cache/hotcache` | `github.com/samber/hot` v0.13.0 | in-memory | yes | **default** |
| `persistence/cache/ottercache` | `github.com/maypok86/otter/v2` v2.3.0 | in-memory | yes | in-memory alternative |
| `persistence/cache/rediscache` | `github.com/redis/go-redis/v9` v9.21.0 | distributed | no | byte-only |
| `persistence/cache/memcache` | `github.com/bradfitz/gomemcache` | distributed | no | byte-only |

### D3 — `CachingInstanceStore` relocated from `runtime/kernel` into `persistence`

`CachingInstanceStore` moves from `runtime/kernel/caching_store.go` to `persistence/caching_instance_store.go` and its storage substrate changes from the hand-rolled `container/list` LRU to a `Codec[instanceEntry]` over the `Cache` returned by `provider.Cache("instances")`. All correctness-bearing behavior is preserved:

- **Ownership gate** (`kernel.InstanceOwnership`): only owned instances are cached/served; non-owned instances bypass the cache.
- **Per-instance refcounted keyed locks**: `Load`-populate and `Commit` for the same instance are serialized so a concurrent write cannot interleave a stale entry.
- **Evict on `ErrConcurrentUpdate`**: a stale-version write evicts the entry so the next load re-reads the backing store.
- **One-time `Warn` for `AlwaysOwn`**: `NewCachingInstanceStore` logs a single structured warning when constructed with `kernel.AlwaysOwn{}` (single-replica footgun, ADR-0054).
- **`Release` evicts before delegating** to `owner.Release` so the cache entry is gone even if `Release` itself errors.

LRU capacity, size limits, and TTL eviction are now the adapter's responsibility. Multi-replica safety is unchanged from ADR-0020: pair with `persistence.NewAdvisoryLockOwnership` (real advisory-lock lease) for multi-replica. A distributed substrate introduces a failover-staleness window that is self-healing — a stale read leads to a `Commit` that fails `ErrConcurrentUpdate`, which evicts and retries; the TTL bounds the window.

The old type in `runtime/kernel` is removed; all references updated.

### D4 — New `CachingTaskStore` in `persistence`

`CachingTaskStore` in `persistence/caching_task_store.go` is a new point-read decorator over `humantask.TaskStore`:

- `Get(taskToken)` — **read-through**: on a cache miss it reads the backing store and populates the cache (keyed by `taskToken`). Errors, including `humantask.ErrTaskNotFound`, are never cached.
- `Upsert(t)` — delegates to the backing store, then **write-through** refreshes the cache entry for `t.TaskToken` so a subsequent `Get` returns the updated snapshot.
- `AssignedTo(actorID)` / `ClaimableBy(actor)` — **pass through, uncached**. These are unbounded set-wide queries; coherent invalidation would require broad eviction or short-TTL staleness. Query caching is deferred as a future opt-in (`WithHumanTaskQueryCache` — not built in this iteration).

Human tasks have no single-writer ownership. With an **in-memory** provider, point reads are coherent only within a single replica. With a **distributed** provider (write-through updates the shared entry), any replica sees the latest `Upsert` — the correct choice for multi-replica deployments.

### D5 — Default-on wiring via `DurableOption` on all three `DurableProvider` constructors

All three constructors (`NewDurableProvider`, `NewMySQLDurableProvider`, `NewSQLiteDurableProvider`) now accept `...DurableOption` and apply caching by default. The default configuration is opinionated: `hotcache` in-memory provider for both stores, `kernel.AlwaysOwn{}` ownership with the one-time `Warn`, instance TTL 5m, human-task TTL 30s.

New `DurableOption`s:

| Option | Purpose |
|---|---|
| `WithCacheProvider(p)` | Set the substrate for BOTH instance and human-task caches |
| `WithInstanceCacheProvider(p)` | Override only the instance-cache substrate |
| `WithHumanTaskCacheProvider(p)` | Override only the human-task-cache substrate |
| `WithDurableInstanceCacheOwnership(o)` | Override the ownership gate |
| `WithDurableInstanceCacheTTL(d)` | Override the instance-cache TTL (default 5m) |
| `WithDurableHumanTaskCacheTTL(d)` | Override the human-task-cache TTL (default 30s) |
| `WithoutCache()` | Escape hatch: disable caching entirely; stores returned unwrapped |

`p.InstanceStore()` and `p.TaskStore()` return the already cache-wrapped stores; consumers see no difference.

For consumers hand-wiring the `Open*` path, the decorators are exposed as public constructors:

```go
func NewCachingInstanceStore(backing kernel.InstanceStore, owner kernel.InstanceOwnership,
    provider cache.Provider, opts ...CachingInstanceStoreOption) (*CachingInstanceStore, error)

func NewCachingTaskStore(backing humantask.TaskStore,
    provider cache.Provider, opts ...CachingTaskStoreOption) (*CachingTaskStore, error)
```

### D6 — Definition cache deferred

The existing `runtime/kernel.CachingDefinitionRegistry` and `kernel.NewMemDefinitionRegistry` are unchanged. Definitions are immutable per `(defID, version)`; a distributed definition cache is a natural but non-urgent follow-up.

## Consequences

**Positive**

- Caching is owned and wired entirely by the persistence layer, satisfying the project rule.
- Cache internals (LRU, eviction, TTL) are delegated to maintained libraries; consumers no longer depend on hand-rolled eviction code.
- A distributed path exists via `rediscache` / `memcache` adapters; swapping the substrate is a single `WithCacheProvider` call.
- Zero-config caching (sensible default on all three `DurableProvider` constructors); every piece is replaceable; `WithoutCache()` provides a clean escape hatch.
- Adding new adapters (e.g. two-tier, alternative memcached) is trivial — implement `Cache` (and optionally `ValueCache`), ship in a new subpackage.

**Negative / risks**

- **Breaking constructor signature.** `persistence.NewCachingInstanceStore` now requires a `cache.Provider` argument (previously `runtime/kernel.NewCachingStore` / `kernel.NewCachingInstanceStore` did not). The type also moved packages. Pre-v0.1.0 — no shims required; all call sites updated.
- **Human-task in-memory cache is single-replica-coherent only.** With the default `hotcache` in-memory provider, a `Get` on one replica can observe a stale value up to the TTL after another replica's `Upsert`. Multi-replica deployments needing full coherence should pass a distributed provider via `WithHumanTaskCacheProvider`. The default TTL is short and bounded (30s).
- **Four new optional dependencies.** Mitigated by subpackage isolation — each library is imported only by its adapter subpackage; a consumer who uses only the default `hotcache` path (or `ottercache`) never transitively imports `go-redis` or `gomemcache`.
- **Instance cache under a distributed substrate.** A distributed instance cache is self-healing (stale read → `ErrConcurrentUpdate` → evict-and-retry) but offers no cross-replica *performance* benefit under the ownership model — the owning replica always has the authoritative entry. The in-memory default is the norm; the distributed path exists for failover-window reduction.

Next free ADR: 0100.
