# 20. Owned-instance single-writer state cache and Ownership port

- Status: Accepted
- Date: 2026-06-22

## Context

Every `Runner.Deliver` reloads the full instance snapshot from the `Store`
(`runtime/runner.go:265`), then holds `st`/`token` in memory across the whole
`deliverLoop` (no per-step reload). So the redundant DB reads are not *within* one
Deliver — they are *across* separate Delivers for the same instance: every
park/resume cycle (`HumanClaimed`, `HumanCompleted`, `TimerFired`,
`SignalReceived`, …) is its own Deliver with its own fresh `Load`. CLAUDE.md
requires "hot-path data must be cached to avoid overloading the DB."

The Persistence sub-project (ADR-0006/0007, spec §6) deliberately **refused** to
cache mutable instance state in v1: a stale cached read drives a gateway/routing
decision and fires its side-effects *before* the optimistic version-CAS on the
next `Commit` can discover the staleness — a correctness bug for a linearizable
token engine, not a perf blemish. It identified the one safe cure: a **single
writer per instance** (Temporal's sticky-execution / instance-lease model), and
deferred it as Persistence follow-up #1.

## Decision

Add an opt-in, write-through, single-writer state cache as a pure `runtime`
decorator over the `Store` port, gated by a pluggable ownership seam.

- **`runtime.CachingStore`** implements `runtime.Store`, wrapping a backing `Store`
  and an `Ownership` (mirrors `CachingDefinitionRegistry`: pure, clock-injected,
  `var _ Store` compile-time guard). `Create`/`Commit` write the post-step
  `(state, token)` through into the cache; `Load` serves it from memory on a fresh
  hit. The cache is **bounded** (LRU on entry count + TTL via the injected
  `clock.Clock`) and **evicts on `ErrConcurrentUpdate`**, propagating the error to
  the caller's existing reload/retry path.
- **Per-instance keyed serialization** (Temporal's one-in-flight-task-per-instance
  model) serializes concurrent Delivers to the same instance, keeping the cache
  coherent and avoiding wasted CAS-failure churn.
- **`runtime.Ownership`** port — `Acquire(ctx, id) (owned bool, err error)` /
  `Release(ctx, id) error` — is the safety gate. `CachingStore` caches/serves an
  instance only while owned; a non-owned instance bypasses the cache and reads the
  backing `Store` every time (safe, unaccelerated). **Ownership is sticky:**
  `Acquire` is idempotent and O(1) for an already-owned instance, so it never costs
  a DB round-trip on the hot path (otherwise it would merely trade a Load round-trip
  for an Acquire round-trip).
- **Two impls.** `runtime.AlwaysOwn` (in-process, pure) always owns — correct and
  free for single-replica / sticky-routed deployments where the consumer guarantees
  this process is the sole writer. A Postgres **advisory-lock** impl
  (`internal/persistence/postgres`, exposed via `persistence.NewAdvisoryLockOwnership`)
  takes `pg_try_advisory_lock(hashtextextended(id))` on a dedicated session
  connection, memoizing held IDs in-memory; process death drops the connection and
  Postgres auto-releases the locks (natural fencing), with the version-CAS as the
  backstop against any stale in-flight `Commit`.

The safety argument: with a single writer, after this process's last `Commit` the
cache holds exactly what is in the DB, so a cached read cannot be stale. Ownership
is the **primary** correctness mechanism; the CAS is defense-in-depth. A full
lease-column lifecycle (heartbeat renewal, expiry) is rejected for v1 in favour of
the lighter advisory-lock path.

## Consequences

**Easier:** repeated park/resume Delivers for an owned instance become memory reads
instead of DB round-trips — the hot-path win CLAUDE.md asks for, without weakening
the linearizable engine. The `Runner` is unchanged (`CachingStore` *is* a `Store`);
the feature is fully opt-in (a consumer that does not wrap the `Store` sees today's
behavior). The pure/`internal` layering holds: the decorator and `AlwaysOwn` import
no vendor; only the advisory-lock impl touches pgx, behind the `persistence` façade.

**Harder / trade-offs:** advisory-lock ownership pins one session connection per
owning process and holds one advisory lock per owned (possibly long-parked)
instance — acceptable for v1 (Postgres handles thousands of advisory locks per
session) but a memory/connection cost; a lease-column ownership that does not pin a
connection is the documented escalation. Correctness now depends on the consumer
honoring single-writer routing when using `AlwaysOwn`; misuse (two replicas both
`AlwaysOwn` for the same instance) reintroduces the stale-read hazard, caught only
after the fact by the CAS. TTL/LRU eviction of an owned instance forces one reload
(always correct, never stale) — TTL is a memory bound, not a consistency knob.
