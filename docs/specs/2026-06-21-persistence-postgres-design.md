# Persistence (PostgreSQL 17) — Design Spec

- Status: Accepted (productionization sub-project #1)
- Date: 2026-06-21
- Supersedes: nothing. Implements the persistence ports promised by
  `docs/specs/2026-06-20-engine-core-design.md` §4/§6.
- Related ADRs: 0006 (storage shape), 0007 (transactional unit + port redesign),
  0008 (consumer-facing façade over internal impl). Builds on 0002 (pure core),
  0003 (clock port), 0004 (flat root layout), 0005 (Runner options).

## 1. Problem & scope

The engine core is pure: `Step(def, state, trigger) -> (state, commands)`. The
reference `runtime` driver persists through three in-memory ports — `StateStore`
(Load/Save a full `InstanceState`), `Journal` (append applied triggers), and
`OutboxWriter` (record domain events). This sub-project replaces those fakes with
a **PostgreSQL 17**-backed implementation that is:

- **Atomic per applied trigger** — snapshot update + journal append + outbox
  writes commit together (transactional outbox), or not at all.
- **Concurrency-safe** — a row can be loaded, advanced by the pure core, and
  saved without losing a concurrent update.
- **Broker-agnostic** — a relay drains the outbox; *no watermill, no broker
  import* here (that is the Eventing sub-project). Persistence must not know how
  events are ultimately shipped.
- **Hot-path aware** — reads of in-flight instances may be cached, but only where
  it is provably safe for a strongly-consistent token engine.
- **Library-first** — a consumer wires it through a module-root constructor; the
  SQL/migration/relay plumbing stays in `internal/`.

**In scope:** the Postgres `Store` (snapshot+journal+outbox), optimistic
concurrency, the outbox relay loop (broker-agnostic), migrations, a read cache,
testcontainers tests, and the runtime refactor that makes the three writes
atomic. **Out of scope (later sub-projects):** watermill relay binding
(Eventing), gocron durable timers (Scheduling), casbin (Authorization), REST/gRPC
(Transports), retry/backoff executor.

## 2. The transaction-boundary problem (the load-bearing decision)

Today the three writes are scattered across one `deliverLoop` iteration in
`runtime/runner.go`:

```
jnl.Append(id, t)            // BEFORE Step
res := engine.Step(def, st, t)
store.Save(res.State)        // AFTER Step
for c in res.Commands: perform(c)   // out.Write(...) happens HERE, inside perform,
                                    // only for CompleteInstance / FailInstance
```

A transactional outbox requires all three (journal, snapshot, outbox) to commit
**atomically per applied trigger**. Two constraints shape the solution:

1. **`perform()` performs external I/O** — `InvokeAction` calls a consumer's
   service, `ThrowSignal` fans out, `ScheduleTimer` registers a waiter. We must
   **never hold a DB transaction open across that I/O** (long transactions, locks
   held during slow/failing network calls). Therefore the atomic unit is **one
   Step**, not the whole cascade-draining loop.
2. **The engine is pure and version-agnostic.** Concurrency tokens must live in
   the persistence layer, never inside `InstanceState` (which is deterministic
   and deep-cloned on every Step; adding a version would pollute determinism and
   `cloneState`).

### Decision: per-Step atomic `Commit`, optimistic version, outbox events derived before the tx

The `deliverLoop` is restructured to:

```
res := engine.Step(def, st, t)                       // pure, first
events := outboxEventsFor(res.Commands)              // pure helper (see §4)
token, err = store.Commit(ctx, token, AppliedStep{   // ONE short tx:
    State:   res.State,                              //   UPDATE snapshot (CAS on token)
    Trigger: t,                                      //   INSERT journal row
    Events:  events,                                 //   INSERT outbox rows
})                                                   // COMMIT
st = res.State
syncWaiters(st)
for c in res.Commands:
    if isOutboxOnly(c) { continue }                  // already persisted in the tx
    perform(c)                                       // external I/O, OUTSIDE the tx
```

- **Optimistic concurrency:** `Commit` does `UPDATE instances SET snapshot=$, version=version+1, ... WHERE instance_id=$ AND version=$expected`. Zero rows affected ⇒ `ErrConcurrentUpdate`; the whole `Deliver` aborts and the caller reloads + retries with **bounded exponential backoff + jitter**. The in-memory `st`/`token` are held across the loop, so we **never re-`SELECT` per step** — only the CAS guards against a racing writer. This is the textbook low-contention case (per-instance writes are usually serialized, rarely concurrent), where optimistic CAS costs nothing on the happy path and `FOR UPDATE` would needlessly serialize the whole read→advance→save window behind a held lock. A genuine DB-level serialization failure (`pgconn.PgError` SQLSTATE `40001`) is treated identically to a CAS miss. (`pg_try_advisory_xact_lock(instance_id)` / `FOR NO KEY UPDATE` — weaker than `FOR UPDATE`, so it doesn't block the FK-check `FOR KEY SHARE` that the journal→instances FK takes — are the escalation path for *measured-hot* instances only; not v1.)
- **Outbox events move out of `perform`.** `CompleteInstance` ⇒ `instance.completed`, `FailInstance` ⇒ `instance.failed` are computed by the pure helper `outboxEventsFor` and written *inside* the tx. `perform` no longer touches the outbox. This is the runtime change that makes the outbox truly transactional.
- `engine.EmitEvent` was specced but never implemented; we **do not** add it to the engine's sealed command set. Outbox topics stay a runtime concern (`outboxEventsFor`), keeping the engine untouched. (If first-class in-definition events are wanted later, that is a separate engine ADR.)

## 3. Port redesign (runtime)

`StateStore`, `Journal`, `OutboxWriter` collapse into one transactional `Store`,
because they must share a transaction. The three old interfaces remain as
**read/value types** where useful (`OutboxEvent`, `JournalReader`).

```go
// Token is an opaque optimistic-concurrency token (Postgres: a bigint version).
type Token int64

// OutboxEvent is one domain event to relay.
type OutboxEvent struct {
    Topic   string
    Payload map[string]any
}

// AppliedStep is the atomic persistence unit for exactly one applied trigger.
type AppliedStep struct {
    State   engine.InstanceState
    Trigger engine.Trigger
    Events  []OutboxEvent
}

// Store is the transactional persistence port the Runner depends on.
type Store interface {
    // Create inserts a brand-new instance from its first applied step.
    Create(ctx context.Context, step AppliedStep) (Token, error)
    // Load returns the current snapshot and its concurrency token.
    Load(ctx context.Context, id string) (engine.InstanceState, Token, error)
    // Commit atomically persists one applied step (snapshot CAS + journal + outbox).
    // Returns ErrConcurrentUpdate if expected is stale.
    Commit(ctx context.Context, expected Token, step AppliedStep) (Token, error)
}

// JournalReader exposes recorded trigger history for replay/audit (unchanged role).
type JournalReader interface {
    Entries(ctx context.Context, id string) ([]engine.Trigger, error)
}
```

`NewRunner`'s three persistence positionals (`store, jnl, out`) collapse to one
`Store` positional (ADR-0007 amends ADR-0005's persistence parameters). The
`Mem*` fakes become a single `MemStore` satisfying `Store` + `JournalReader`,
with `Events()`/`Entries()` accessors for tests; its `Commit` buffers and applies
on success so a failing step does not half-apply.

**Trigger (de)serialization.** The journal stores triggers; the sealed `Trigger`
set must round-trip through Postgres (JSONB + a `kind` discriminator). A small
`engine`-level codec (`MarshalTrigger`/`UnmarshalTrigger`, or a runtime-side
registry keyed by the sealed variants) is required — covered as its own plan
task. The same codec serves journal replay.

## 4. Storage shape (ADR-0006)

**Hybrid: full snapshot as `JSONB` + projected indexed columns.** The pure core
already treats `InstanceState` as one aggregate that is wholesale deep-cloned per
Step; normalizing it into tokens/tasks/timers/scopes tables would fight that
model and impose heavy mapping + migration churn for little v1 value.

```sql
CREATE TABLE wrkflw_instances (
    instance_id  TEXT PRIMARY KEY,
    def_id       TEXT        NOT NULL,
    def_version  INT         NOT NULL,
    status       SMALLINT    NOT NULL,        -- engine.Status (projected for admin queries)
    snapshot     JSONB       NOT NULL,        -- the full InstanceState (source of truth)
    version      BIGINT      NOT NULL,        -- optimistic concurrency token
    started_at   TIMESTAMPTZ NOT NULL,
    ended_at     TIMESTAMPTZ,
    updated_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX wrkflw_instances_status_idx ON wrkflw_instances (status) WHERE ended_at IS NULL;

CREATE TABLE wrkflw_journal (
    instance_id TEXT        NOT NULL REFERENCES wrkflw_instances(instance_id),
    seq         BIGINT      NOT NULL,         -- per-instance monotonic
    kind        TEXT        NOT NULL,         -- sealed Trigger discriminator
    trigger     JSONB       NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (instance_id, seq)
);

CREATE TABLE wrkflw_outbox (
    id           BIGSERIAL PRIMARY KEY,       -- global relay order
    instance_id  TEXT        NOT NULL,
    topic        TEXT        NOT NULL,
    payload      JSONB       NOT NULL,
    dedup_key    TEXT        NOT NULL UNIQUE,  -- idempotency for at-least-once relay
    created_at   TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ                   -- NULL until relayed
);
CREATE INDEX wrkflw_outbox_unpublished_idx ON wrkflw_outbox (id) WHERE published_at IS NULL;
```

- **Why projected columns:** admin monitoring ("monitor all processes") queries
  `status`/`def_id`/`updated_at` without parsing JSONB; the snapshot stays the
  single writer of truth. JSONB GIN indexing on `snapshot` is available if
  variable-level queries are needed later (not v1).
- **Why plain columns, not generated:** a `GENERATED ALWAYS AS (snapshot->>'status') STORED` column re-indexes whenever the snapshot changes (every transition); plain **engine-written** columns (`status`, `ended_at`) keep their indexes churning only when the value truly changes. So the runtime writes the projected columns explicitly alongside the snapshot.
- **TOAST write amplification (accepted, tuned):** the snapshot changes on every
  transition, so it never benefits from Postgres's "unchanged out-of-line value"
  TOAST discount — each commit rewrites the row + TOAST pages and leaves dead
  tuples. v1 mitigations: keep snapshots small (history-cap follow-up), lower the
  table `fillfactor` to favour HOT same-page updates, and watch autovacuum on the
  instance table *and* its TOAST table. Documented as a tuning follow-up, not a
  blocker.
- **History growth (spec §4 consequence):** the snapshot grows with `History`.
  v1 accepts this; a later option may cap inline history (the journal remains the
  unbounded audit source). Not built now.
- **Migrations:** embedded `.sql` via `embed.FS`, run by the consumer through
  `persistence.Migrate(ctx, db)` — **never auto-run on import** (the consumer
  chooses when). Tooling: **`pressly/goose`** with `SetBaseFS` (cleanest embed
  fit; `goose_db_version` gives idempotency). Finalized in ADR-0006.

## 4b. Definition persistence (required for recovery)

On recovery the runtime loads a snapshot carrying `(DefID, DefVersion)` and must
fetch the matching `ProcessDefinition` to call `Step`. If definitions live only in
`MapDefinitionRegistry` (in-memory), a restart loses them and in-flight instances
cannot resume. So a **Postgres-backed definition store is in scope**, replacing
the in-memory registry as the durable source:

```sql
CREATE TABLE wrkflw_definitions (
    def_id      TEXT        NOT NULL,
    version     INT         NOT NULL,
    definition  JSONB       NOT NULL,   -- the marshalled model.ProcessDefinition
    created_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (def_id, version)
);
```

Definitions are **immutable per `(def_id, version)`**, which makes them the safe,
high-value hot-path cache target (§6): a read-through, TTL'd, single-flight cache
in front of the store eliminates the per-`Step` definition load without any of the
staleness hazards of caching mutable instance state. The runtime's
`DefinitionRegistry` port is satisfied by `store → cache` composition.

## 5. Outbox relay (broker-agnostic)

A `Relay` drains `wrkflw_outbox` and hands each event to a consumer-supplied
`Publisher` (an in-repo interface — **not** watermill):

```go
type Publisher interface {
    Publish(ctx context.Context, ev OutboxEvent) error  // at-least-once; idempotent downstream
}
type Relay struct { /* poll interval, batch size, backoff */ }
func (r *Relay) Run(ctx context.Context) error           // until ctx cancel
```

- **Claiming:** `SELECT ... WHERE published_at IS NULL ORDER BY id FOR UPDATE SKIP LOCKED LIMIT $batch` — multiple relay workers cooperate without double-publishing.
- **Delivery:** **at-least-once**; `dedup_key` lets downstream dedupe. Per-instance ordering preserved via `ORDER BY id` (BIGSERIAL is commit-ordered enough for v1; strict per-aggregate ordering noted as a follow-up if needed).
- **Polling** for v1 (simple, robust). `LISTEN/NOTIFY` is an optional latency
  optimization tracked as a follow-up; CDC/Debezium is explicitly out.
- The Eventing sub-project later provides a watermill-backed `Publisher`; this
  layer never imports watermill.

## 6. Hot-path cache — definitions & compiled `expr`, NOT instance state

CLAUDE.md requires "hot-path data must be cached to avoid overloading the DB."
The research is emphatic about *which* data: **do not cache mutable instance
state in v1.** A version-CAS protects the *write* (a stale cached token loses the
CAS and reloads), but the stale *read* has already driven a gateway/routing
decision and fired its side-effects before the CAS rejects it — a correctness bug
for a linearizable token engine, not a perf blemish. The single safe way to cache
mutable state is single-writer instance ownership (Temporal's sticky-execution
pattern: lease each instance to one owning node), which is a much larger change
deferred out of this sub-project.

The data that actually overloads the DB on the hot path is **read-mostly and
effectively immutable**, and is safe to cache aggressively:

- **Process definitions** — every `Step` needs the `ProcessDefinition`; today the
  `DefinitionRegistry` loads it. Add a **read-through, TTL'd, single-flight**
  cache (stampede-safe) in front of the registry / a Postgres-backed definition
  store. This is the real hot-path win.
- **Compiled `expr` programs** — `expreval` already memoises compilation; we
  confirm/extend it so gateway/condition compilation is not repeated.

The instance `Store` therefore ships **no instance-state cache in v1** (the DB is
the linearizable source of truth). `WithCache` applies only to the definition
layer. This is recorded as a deliberate scope decision; an owned-instance cache
is a tracked follow-up.

## 7. Package layout (ADR-0008)

- `internal/persistence/postgres/` — concrete impl: SQL, pgx wiring, row mapping,
  migrations, relay loop internals, trigger codec. Consumers never import it.
- `persistence/` (module root) — the consumer-facing façade:
  `OpenPostgres(ctx, *pgxpool.Pool, ...Option) (*Store, error)`,
  `Migrate(ctx, pool) error`, the `Relay`/`Publisher`/`Cache` types and options,
  re-exported sentinels (`ErrConcurrentUpdate`, `ErrInstanceNotFound`). Honors
  CLAUDE.md ("concrete persistence in internal/") while keeping every feature
  reachable through a root package (library-first).
- `runtime/` — the `Store`/`JournalReader` ports + `MemStore` fake + the
  `deliverLoop`/`outboxEventsFor` refactor.

**Go specifics:**

- **Driver:** `pgx` v5 native interface + `pgxpool` (no `database/sql` shim) —
  required for `LISTEN`/`NOTIFY` (relay wakeups), batch, advisory locks, native
  JSONB, all absent through `database/sql`. Postgres 17 is locked (ADR), so the
  portability argument for `*sql.DB` does not apply.
- **Querier seam:** repos accept a minimal **`DBTX` interface**
  (`Exec/Query/QueryRow/Begin/SendBatch`) that `*pgxpool.Pool`, `*pgx.Conn`, and
  `pgx.Tx` all satisfy — so the same repo code runs against a pool *or* an
  in-flight `Tx`, which is exactly how `Commit` makes snapshot+journal+outbox
  share one transaction, and lets a consumer pass their own tx.
- **JSONB scanning:** `pgtype` `JSONBCodec` (via `encoding/json`) for typed rows
  and `map[string]any` process variables. **Gotcha:** `map[string]any` decodes
  numbers as `float64`; if variable numeric fidelity matters, use
  `json.Decoder.UseNumber()` in the codec. Covered by a round-trip test.
- **Testing:** testcontainers-go Postgres 17 via the mandated
  `database.RunTestDatabase(t, opts...)` helper (create it if absent, per the
  `use-testcontainers` skill); use Snapshot/Restore between tests, give
  `t.Parallel()` tests their own DB, and **never name the DB `postgres`** (Restore
  drops the connected DB).

## 8. Risks & follow-ups

- **Trigger codec completeness** — every sealed `Trigger`/`Command`-carried type
  (incl. `authz.Actor`, payload maps) must round-trip; a missing variant silently
  breaks replay. Mitigation: an exhaustiveness test over the sealed set.
- **Retry idempotency** — on `ErrConcurrentUpdate` the caller redelivers the same
  trigger; the journal `(instance_id, seq)` PK and outbox `dedup_key` must make a
  redelivered step idempotent. Designed for; full retry/backoff executor is a
  later sub-project.
- **Strict per-aggregate relay ordering** — BIGSERIAL global order is sufficient
  for v1; if a consumer needs strict per-instance ordering under concurrent
  relay workers, partition claiming by `instance_id` (follow-up).
- **Snapshot bloat** — long/looping instances; optional history cap deferred.
  Pairs with the TOAST/fillfactor/autovacuum tuning noted in §4.
- **Owned-instance state cache** — a single-writer (instance-leased) cache of
  mutable state is the only safe way to cache it; deferred (see §6).
- **Relay latency** — v1 polls; `LISTEN/NOTIFY` wakeups are a tracked latency
  optimization layered on the poll fallback.

## 9. Verification

- `go test -race ./...` green; ≥85% coverage on `persistence/`, `internal/persistence/...`, touched `runtime/`.
- testcontainers Postgres 17 integration tests for `Store` (create/load/commit/CAS-conflict), the relay (`SKIP LOCKED`, at-least-once, dedup), and migrations.
- `golangci-lint run ./...` clean. No watermill/casbin/gocron import anywhere under `persistence/` or `internal/persistence/`.
- The full engine+runtime e2e suite passes against the Postgres `Store` (not just `MemStore`), proving the port redesign is behavior-preserving.
