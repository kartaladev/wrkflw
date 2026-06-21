# Performance / Caching — Design Spec

- Status: Accepted (deferred-backlog track #4 — Performance/caching)
- Date: 2026-06-22
- Supersedes: nothing. Implements three follow-ups deferred by the Persistence
  sub-project (`docs/specs/2026-06-21-persistence-postgres-design.md` §6, §8):
  the owned-instance state cache (#1), the history/snapshot cap (#2), and the
  optional LISTEN/NOTIFY relay trigger (#3).
- Related ADRs (new): 0020 (owned-instance single-writer cache + Ownership port),
  0021 (open-visit-preserving history cap), 0022 (LISTEN/NOTIFY relay trigger).
  Builds on 0002 (pure core), 0003 (clock port), 0006–0008 (persistence shape /
  transactional Store / façade-over-internal), 0017 (relay poison isolation).

## 1. Problem & scope

The Persistence sub-project shipped a correct, strongly-consistent Postgres
`Store` but deliberately deferred three performance items as too risky to bundle
with the foundational work. This track lands them, each behind an opt-in seam so
existing consumers see no behavior change unless they ask for it.

1. **Owned-instance state cache.** Every `Runner.Deliver` reloads the full
   instance snapshot from Postgres (`runtime/runner.go:265`). For a workflow that
   parks and resumes repeatedly (human task → claim → complete → timer-fire), each
   resume is a separate `Deliver` and therefore a separate DB read. Caching
   mutable instance state is unsafe in general (spec §6: a stale read drives a
   gateway decision and fires side-effects *before* the version-CAS rejects the
   write), so the cache must be gated by a **single-writer-per-instance**
   guarantee.

2. **History / snapshot cap.** The snapshot JSONB grows with inline
   `InstanceState.History` (`[]NodeVisit`). A long-running or looping instance
   bloats its row, amplifying the per-transition TOAST rewrite. The `wrkflw_journal`
   table is the unbounded audit source; the inline history is a convenience copy
   that can be bounded — but only safely (see §4).

3. **Optional LISTEN/NOTIFY relay trigger.** The outbox `Relay` polls on a fixed
   interval (`internal/persistence/postgres/relay.go`, `WithPollInterval`, default
   1s). A Postgres `LISTEN`/`NOTIFY` push wakes the relay the moment an event is
   committed, cutting end-to-end latency and idle DB polling, layered on top of the
   poll fallback.

**In scope:** the three features above, each opt-in, plus their ADRs, tests, and
the runtime/persistence wiring. **Out of scope:** a full lease-column ownership
lifecycle with renewal/expiry (the advisory-lock impl is the v1 multi-process
path — see §3.4); CDC/Debezium; per-aggregate strict relay ordering; the
`Store.Load`/`Commit` span/metric instrumentation deferred by Observability
follow-up #7 (separately tracked).

## 2. Invariants this track must not violate

1. **Pure core untouched.** `engine` and `model` gain no new imports and no new
   behavior. All three features live in `runtime/` (pure ports + decorators) and
   `internal/persistence/postgres` + `persistence/` (Postgres specifics). The
   `engine.Step` contract, determinism, and purity are unchanged.
2. **Opt-in, behavior-preserving by default.** A consumer who does not configure
   any of the three features gets exactly today's behavior: no cache (direct
   `Store`), unbounded inline history, poll-only relay.
3. **Strong consistency preserved.** The cache never weakens the linearizable
   token engine. Its correctness rests on single-writer ownership; the version-CAS
   remains the backstop. The history cap is provably behavior-preserving (§4).
4. **Façade/internal layering (ADR-0008).** Postgres-specific pieces (advisory-lock
   ownership, history cap, NOTIFY emission) stay in `internal/persistence/postgres`
   and are reached only through `persistence/` root constructors/options returning
   stable interface types. The pure pieces (`CachingStore`, `Ownership` port,
   `AlwaysOwn`) live in `runtime/` alongside `CachingDefinitionRegistry`.

## 3. Feature 1 — Owned-instance single-writer state cache (ADR-0020)

### 3.1 Why the win is across Delivers, not within one

`Runner.Deliver` calls `store.Load` once (`runner.go:265`), then holds `st`/`token`
in memory across the entire `deliverLoop` (no per-step reload; `Commit` returns the
next token, kept in memory). So caching saves nothing *within* a single Deliver.
The savings are **across separate `Deliver` calls for the same instance** — every
park/resume cycle (`HumanClaimed`, `HumanCompleted`, `TimerFired`,
`SignalReceived`, …) is its own `Deliver` with its own fresh `Load`. The cache
turns each such Load from a DB round-trip into a memory read **when this process
owns the instance and holds its latest committed state**.

### 3.2 The safety argument (load-bearing)

A stale cached read is a correctness bug, not a perf blemish: it can drive an
exclusive-gateway branch or fire an action before the CAS on the *next* commit
discovers the staleness. The cache is safe **iff** there is a single writer per
instance:

- With single-writer ownership, after this process's last `Commit` the cache holds
  exactly what is in the DB (write-through). No other writer exists, so the cached
  read cannot be stale.
- The version-CAS remains defense-in-depth: if ownership is ever violated, the next
  `Commit` fails CAS → evict + reload + the caller's existing retry path runs. The
  *primary* safety mechanism is ownership; CAS is the backstop.

### 3.3 `runtime.CachingStore` — the decorator

A `Store` decorator that mirrors `CachingDefinitionRegistry` stylistically (pure,
`runtime/`, clock-injected, compile-time `var _ Store` assertion). It wraps any
backing `runtime.Store` and an `Ownership`:

```go
// CachingStore is a write-through, single-writer cache in front of a Store.
// It is correct ONLY when each cached instance has exactly one writing process,
// which the Ownership port guarantees.
type CachingStore struct {
    backing Store
    owner   Ownership
    clk     clock.Clock
    ttl     time.Duration
    // per-instance keyed locks + bounded LRU entry map (state + token + expiry)
}

func NewCachingStore(backing Store, owner Ownership, clk clock.Clock, opts ...CachingStoreOption) *CachingStore
```

Behavior:

- **`Create`** — delegate to `backing.Create`; on success, acquire ownership for the
  new instance and write-through `(state, token)` into the cache.
- **`Load`** — `owner.Acquire(id)`; if **not owned**, bypass the cache and delegate
  to `backing.Load` (safe, unaccelerated). If **owned**: under the per-instance
  keyed lock, serve the cached `(state, token)` on a fresh hit (within TTL); on a
  miss, delegate to `backing.Load` and populate.
- **`Acquire` must be sticky and fast.** Calling `Acquire` on every `Load` cannot
  cost a DB round-trip, or it would just trade a Load round-trip for an Acquire
  round-trip and erase the win. Ownership is therefore **sticky**: `Acquire` is
  idempotent and O(1) for an already-owned instance. The advisory-lock impl
  memoizes its held instance IDs in an in-memory set — only the *first* `Acquire`
  for an instance issues `pg_try_advisory_lock`; subsequent calls return `true`
  from the set without touching the DB. Ownership only changes on explicit
  `Release` (or process death), so the verdict is stable to cache.
- **`Commit`** — under the per-instance keyed lock, delegate to `backing.Commit`. On
  success, write-through the new `(state, newToken)`. On `ErrConcurrentUpdate`,
  **evict** the entry (its cached token is stale) and propagate the error so the
  caller's existing reload/retry path runs.
- **Coherence:** **per-instance keyed serialization** (chosen approach — Temporal's
  one-in-flight-task-per-instance model) serializes concurrent Delivers to the same
  instance, keeping the cache coherent and avoiding wasted CAS-failure churn.
- **Bounding:** LRU cap on entry count + TTL expiry (clock-injected), so the cache
  never grows unbounded for a process that owns many parked instances. TTL expiry of
  an *owned* instance just forces one reload (still correct); LRU eviction likewise.
- **`JournalReader` pass-through:** if the backing also implements `JournalReader`,
  `CachingStore` forwards `Entries` unchanged (journal is not cached).

`CachingStore` imports no vendor and no Postgres — it is a pure `runtime` port
decorator, exactly like `CachingDefinitionRegistry`.

### 3.4 `runtime.Ownership` port + implementations

```go
// Ownership decides whether this process is the single writer for an instance,
// and therefore whether its state may be cached and served from memory.
type Ownership interface {
    // Acquire reports whether this process owns instanceID (taking ownership if
    // available). owned=false means another process owns it: do not cache.
    Acquire(ctx context.Context, instanceID string) (owned bool, err error)
    // Release relinquishes ownership, triggering eviction.
    Release(ctx context.Context, instanceID string) error
}
```

- **`runtime.AlwaysOwn` (in-process, pure).** `Acquire` always returns `true`;
  `Release` is a no-op. For single-replica or sticky-routed deployments where the
  consumer guarantees this process is the sole writer. Correct and free; this is the
  default impl paired with `CachingStore` for single-process embedding.
- **Postgres advisory-lock ownership (`internal/persistence/postgres` +
  `persistence`).** `Acquire` runs `pg_try_advisory_lock(hashtextextended(id))` on a
  dedicated long-lived session connection; success ⇒ owned. `Release` runs
  `pg_advisory_unlock`. One session holds many advisory locks (one per owned
  instance). **Fencing:** if the process dies, its session connection drops and
  Postgres auto-releases every advisory lock it held, so a new owner can take over;
  the version-CAS rejects any in-flight stale `Commit` from the dead/usurped owner.
  Exposed via `persistence.NewAdvisoryLockOwnership(ctx, pool) (runtime.Ownership, io.Closer)`.
  A full lease-column lifecycle (heartbeat renewal, expiry) is **out of scope** —
  the advisory lock is the lighter v1 multi-process path (per the track decision).

**Tradeoff noted:** advisory-lock ownership pins one connection for the lifetime of
ownership and holds a session lock per owned (possibly long-parked) instance.
Acceptable for v1 (Postgres handles thousands of advisory locks per session); a
lease-column alternative that does not pin a connection is the documented follow-up.

### 3.5 Wiring

`CachingStore` is opt-in: a consumer composes `runtime.NewCachingStore(pgStore,
owner, clk, ...)` and passes the result as the `Runner`'s `Store`. No `Runner`
signature change — `CachingStore` *is* a `Store`. The `persistence` façade gains the
advisory-lock ownership constructor; `runtime` gains `CachingStore`, `Ownership`,
`AlwaysOwn`, and the `CachingStoreOption`s (`WithCacheTTL`, `WithCacheMaxEntries`).

## 4. Feature 2 — Open-visit-preserving history cap (ADR-0021)

### 4.1 The correctness finding

`engine.Step` reads `InstanceState.History` in exactly two places —
`setVisitActor` (`engine/step.go:1390`) and `closeVisit` (`engine/step.go:1427`) —
and **both match only _open_ visits** (`NodeVisit.LeftAt == nil`). A visit is closed
(`LeftAt` set) when its token leaves the node; **closed visits are never read again
by the engine** — they are pure audit. Therefore:

- A naive "keep the last N entries" cap is **unsafe**: it can drop an old-but-open
  visit (e.g. a human-task token parked for days whose open visit sits behind many
  short closed visits). Dropping it makes the eventual `closeVisit` a no-op (a
  dangling-open visit / lost `LeftAt`) and loses the `ActorID` record.
- The **safe** cap retains **all open visits** plus only the **most recent N closed
  visits**, preserving relative order. Because the engine only ever matches open
  visits, this is **behavior-preserving**: the reloaded state drives identical
  decisions.

### 4.2 Where and how

A pure projection applied at the **persistence marshal boundary** in
`internal/persistence/postgres/store.go`, to a copy of the state just before
`json.Marshal(step.State)` in both `Create` and `Commit`:

```go
// capHistory returns a copy of st whose History retains every open visit
// (LeftAt == nil) and at most the most recent n closed visits, preserving order.
// n <= 0 means "no cap" (return st unchanged).
func capHistory(st engine.InstanceState, n int) engine.InstanceState
```

- Engine and runtime are **untouched** (the pure core never learns about storage
  caps). The `wrkflw_journal` table remains the unbounded audit source — capping the
  inline copy loses nothing recoverable.
- **Opt-in, default unbounded** (chosen): `persistence.OpenPostgres(..., WithHistoryCap(n))`.
  Unset ⇒ `n <= 0` ⇒ exact current behavior, no surprise audit-data loss for existing
  consumers.
- The in-memory `st` held across the `deliverLoop` keeps full history; only the
  persisted snapshot is capped. After a reload (the next `Deliver`), in-memory and
  persisted converge on the capped form — still correct, because only closed visits
  were dropped.

## 5. Feature 3 — Optional LISTEN/NOTIFY relay trigger (ADR-0022)

### 5.1 Write side — transactional NOTIFY

When `Store.Commit`/`Create` inserts one or more outbox rows, emit
`NOTIFY wrkflw_outbox` **inside the same transaction**, so the notification is
delivered iff the tx commits (a rolled-back step notifies nothing). Skip the NOTIFY
entirely when the step produced no outbox events (the common case), keeping the
hottest write path untouched. Opt-in via `persistence.OpenPostgres(..., WithOutboxNotify())`.

### 5.2 Read side — listener wakeup with poll fallback

`Relay.Run` (opt-in `WithListenNotify()`) acquires a dedicated connection from the
pool, runs `LISTEN wrkflw_outbox`, and in a goroutine loops `conn.WaitForNotification(ctx)`,
feeding a wakeup channel into the existing `select`:

```
for {
    select {
    case <-ctx.Done():   return ctx.Err()
    case <-ticker.C:     drainUntilEmpty(ctx)   // existing poll FALLBACK, unchanged
    case <-notifyCh:     drainUntilEmpty(ctx)   // new: woken by NOTIFY
    }
}
```

- The `ticker` poll **stays as fallback** — covers missed notifications (listener
  reconnects), restarts, and the case where one relay worker holds the listen
  connection while peers poll. NOTIFY is a latency optimization layered on top, never
  a correctness dependency.
- On wakeup, drain until `DrainOnce` returns 0 (coalesce a burst of notifications
  into one drain sweep). `DrainOnce`'s per-row poison isolation (ADR-0017) is
  unchanged.
- Listener resilience: on a dropped listen connection, log, re-acquire, re-`LISTEN`;
  the poll fallback covers the gap.

### 5.3 Low coupling

The two sides are independently safe. NOTIFY without a listener ⇒ ignored. Listener
without NOTIFY ⇒ relay falls back to polling. Enabling **both** yields the latency
win. Neither weakens at-least-once delivery or the DLQ semantics.

## 6. Package layout

| Package | Adds |
|---|---|
| `runtime/` | `CachingStore` (`Store` decorator) + `CachingStoreOption`s; `Ownership` port; `AlwaysOwn` in-process impl. All pure (no vendor, no Postgres), beside `CachingDefinitionRegistry`. |
| `internal/persistence/postgres/` | `capHistory` projection wired into `Store.Create`/`Commit` marshaling; transactional `NOTIFY` emission; advisory-lock `Ownership` impl; relay listener loop + `drainUntilEmpty`. |
| `persistence/` (root façade) | `WithHistoryCap(n)`, `WithOutboxNotify()` options on `OpenPostgres`; `WithListenNotify()` `RelayOption`; `NewAdvisoryLockOwnership(ctx, pool) (runtime.Ownership, io.Closer)` returning the stable port type. |
| `engine/`, `model/` | **Nothing.** Purity guard stays green. |

## 7. Risks & follow-ups

- **Advisory-lock connection pinning** — one session connection held per owning
  process, one advisory lock per owned (possibly long-parked) instance. Acceptable
  v1; a lease-column ownership (no pinned connection, heartbeat + expiry) is the
  documented escalation for very high parked-instance counts.
- **Cache TTL vs liveness** — TTL/LRU eviction of an owned instance forces one
  reload (always correct, never stale). TTL is a memory bound, not a consistency
  knob; document that lowering it trades hit-rate for memory.
- **History cap and external audit consumers** — anything reading inline history off
  the snapshot (vs the journal) sees only retained visits when a cap is set; the
  journal remains complete. Documented in the `WithHistoryCap` godoc.
- **NOTIFY payload size** — `NOTIFY` carries no payload here (bare channel wakeup);
  the relay still claims via `FOR UPDATE SKIP LOCKED`, so the 8000-byte NOTIFY payload
  limit is irrelevant. No per-row notification storm: one bare NOTIFY per committing
  step that produced events.
- **Multi-worker relay + listener** — only workers holding a listen connection get
  push wakeups; others rely on the poll. Strict per-worker push fairness is out of
  scope; the poll fallback guarantees liveness for all.

## 8. Verification

- `go test -race ./...` green; ≥85% line coverage on every touched package
  (`runtime`, `internal/persistence/postgres`, `persistence`).
- New unit tests: `CachingStore` hit/miss/write-through/evict-on-CAS/keyed-lock
  coherence (table-driven, `assert` closures, `t.Context()`, fake clock); `AlwaysOwn`;
  `capHistory` open-visit-preservation (a long-parked open visit behind > N closed
  visits survives; closed visits trimmed to N; n<=0 is a no-op).
- New testcontainers Postgres tests: advisory-lock ownership (two "processes" / two
  connections contend; loser gets `owned=false`; connection-drop releases the lock);
  history cap round-trips through a real JSONB column; NOTIFY/LISTEN wakes the relay
  faster than the poll interval (assert a drain occurs well before one `pollInterval`).
  Run the Postgres package with limited container parallelism (`go test -p 1`).
- `golangci-lint run ./...` clean. No `watermill`/`casbin`/`gocron`/`clockwork`
  import in production code; engine/model purity guard (`TestCorePurityNoOTel` and
  the import checks) still green; `engine`/`model` unchanged.
- The full engine+runtime e2e suite passes with a `CachingStore` wrapping the Postgres
  `Store` (proving the decorator is behavior-preserving), and with `WithHistoryCap`
  set (proving capped reload drives identical execution to uncapped).
