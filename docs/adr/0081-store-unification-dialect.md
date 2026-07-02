# 0081. Collapse per-database store packages into one neutral store + dialect abstraction

Status: **Accepted — 2026-07-02.**
Plan: `docs/plans/2026-07-02-store-unification-dialect-sqlite.md`.
Spec: `docs/specs/2026-07-02-store-unification-dialect-sqlite-design.md`.
Builds on: ADR-0079 (database transaction toolkit), ADR-0080 (UTC time discipline).
See also: ADR-0073 (MySQL persistence backend), ADR-0082 (SQLite backend).

## Context

The persistence layer grew two near-identical package trees — `internal/persistence/postgres`
and `internal/persistence/mysql` — that mirrored each other file-for-file: store, relay,
lister, call_links, chainlink, dedup, definitions, ownership, pruner, timerstore, migrate,
plus pure-Go helpers (history capping, trigger codec, relay backoff). Roughly 80–90% of
each package was structurally parallel; genuinely divergent code was confined to relay
LISTEN/NOTIFY, ownership locks, and the call-links leased-claim path.

Two problems compounded the duplication:

1. **Misnaming conflated orthogonal axes.** The package names `postgres`/`mysql` describe the
   *database*, but the real distinction is the **access mechanism** (pgx vs. `database/sql`),
   which is orthogonal to the **SQL dialect**. pgx is Postgres-only, but `database/sql` is
   dialect-agnostic; the names obscured this and made adding a third driver feel like adding
   a third complete copy.

2. **Concrete driver types leaked into signatures.** Each package defined its own
   driver-shaped `DBTX` seam. `Deduper.Seen` exposed `pgx.Tx` (Postgres) or `*sql.Tx`
   (MySQL) as explicit parameters — the only driver-concrete type that appeared in a
   public-facing signature. Transactions were begun inline in multi-write methods and
   threaded as explicit parameters; the `database.Querier` / `transaction` toolkit
   introduced in ADR-0079 was available but not yet used by any store.

A neutral store parametrized by an explicit dialect value was the natural consequence of the
two-axis framing that ADR-0079 established: access (already behind `database.Querier`) × SQL
dialect (to be made explicit). Unifying the stores also created the seam needed to add a
third dialect (SQLite, ADR-0082) cheaply.

## Decision

We collapse `internal/persistence/postgres` and `internal/persistence/mysql` into a single
**neutral `internal/persistence/store`** package driven by two orthogonal values:

- **`database.Querier`** (ADR-0079) — the access-mechanism seam, already available.
- **`dialect.Dialect`** — a new interface in the new `internal/persistence/dialect` package,
  abstracting every SQL-text and driver-error difference between backends.

### 1. The two-axis model

Access and dialect are separated explicitly:

| Access | Dialect |
|---|---|
| `*pgxpool.Pool` → `database.Querier` via `From` | `dialect.NewPostgres()` |
| `*sql.DB` → `database.Querier` via `From` | `dialect.NewMySQL()`, `dialect.NewSQLite()` |

The store, relay, lister, and all other store types accept `(conn any, d dialect.Dialect)`;
neither a concrete pool type nor a dialect name string appears in their signatures.

### 2. The `dialect.Dialect` interface

`Dialect` encapsulates every SQL-text and driver-error difference in one stateless, concurrently
safe value chosen once at startup:

- **Placeholder translation:** `Rebind(query)` — `?` → `$1`/`$2`... for Postgres; no-op
  for MySQL/SQLite.
- **Conflict/ignore clauses:** `UpsertTimer()`, `UpsertDefinition()`, `InsertIgnorePrefix()`,
  `InsertIgnoreDedup()` — ON CONFLICT variants for Postgres/SQLite vs. `INSERT IGNORE` for MySQL.
- **Reserved-word workarounds:** `JournalTriggerColumn()` — `"trigger"` (Postgres/SQLite) vs.
  `"trigger_"` (MySQL).
- **Aggregate queries:** `OutboxStatsQuery()` — FILTER(WHERE) for Postgres vs. conditional
  aggregation for MySQL/SQLite.
- **Pub/sub statement:** `NotifyStatement(channel)` — `"NOTIFY <ch>"` for Postgres, `""` for
  MySQL/SQLite (no native NOTIFY).
- **Capability flags:** `SupportsReturning()` and `SupportsSkipLocked()` — let the
  leased-claim code branch on observed capabilities rather than on dialect name.
- **Error classification:** `IsUniqueViolation(err)`, `IsRetryableConflict(err)` — each
  targeting the dialect's expected driver error type.
- **Keyset pagination:** `KeysetCursorPredicate()`, `KeysetCursorArgCount()` — row-value
  comparison for Postgres vs. OR-decomposition for MySQL/SQLite.
- **Time codec flag:** `TimestampsAsText()` — SQLite stores timestamps as RFC3339Nano TEXT;
  Postgres and MySQL bind and scan `time.Time` natively (see ADR-0080).
- **Incident count expression:** `IncidentCountExpr()` — dialect-specific JSON array-length
  SQL embedded in the lister query.

### 3. Optional capability interfaces

Divergences that are not SQL text — and that are absent in some dialect/access combinations —
are modeled as **optional capabilities**, type-asserted from the `Dialect` value at construction
time and injected into the stores that need them:

- **`Notifier`** — `Listen(ctx, channel) (<-chan struct{}, func(), error)`. Only the
  (pgx, Postgres) combination provides a real implementation; all others return
  `ErrUnsupported`. The relay polls on a timer when no `Notifier` is available.
- **`Locker`** — `TryLock(ctx, key) (bool, error)` / `Unlock(ctx, key) error`. Postgres uses
  session-scoped `pg_try_advisory_lock`; MySQL uses connection-scoped `GET_LOCK`; SQLite
  returns `ErrUnsupported` (see ADR-0082). `ErrUnsupported` is the sentinel in
  `internal/persistence/dialect`; callers match with `errors.Is`.

### 4. Transaction discipline

All multi-write paths in the neutral store route through `transaction.JoinOrBegin(ctx, conn)`,
eliminating manual `pool.Begin` / `defer rollback` inline blocks. Read-only paths call
`database.From(conn)` to obtain a pool-level querier. This aligns the stores with the ambient
transaction model introduced in ADR-0079 and enables cross-store atomicity without threading
driver handles by hand.

### 5. Deduper interface unification (breaking change)

The former `Deduper.Seen(ctx, tx, subscriber, messageID)` took an explicit driver-typed
transaction parameter (`pgx.Tx` for Postgres, `*sql.Tx` for MySQL). The separate `MySQLDeduper`
interface (with `*sql.Tx`) existed solely to carry this parameter; it is removed.

The unified `Deduper.Seen(ctx, subscriber, messageID)` joins the ambient transaction stored
in ctx by the transaction toolkit, so the dedup record commits or rolls back with the
surrounding business unit. When no ambient transaction is present, `Seen` begins and commits
its own leaf transaction.

**Migration:** replace explicit `pool.Begin` / `d.Seen(ctx, tx, sub, id)` / `tx.Commit`
sequences with a `transaction.Begin(ctx, conn)` call that stores the ambient transaction in
the derived context; then call `d.Seen(derivedCtx, sub, id)`. A bare `d.Seen` with no ambient
transaction transparently begins its own leaf.

### 6. Deletion of per-database packages

`internal/persistence/postgres` and `internal/persistence/mysql` are deleted in full. The
neutral `internal/persistence/store` package is the single implementation, confirmed parity
by a **3-dialect conformance suite** — shared test tables and assertions run against all
three dialect/connection combinations (Postgres via testcontainers, MySQL via testcontainers,
SQLite in-process). The conformance suite is the gate: any behaviour divergence surfaces as a
test failure before a dialect package is removed.

### 7. Observability and lineage preserved

`ChainLineageReader` and `CallLineageReader` capabilities, observability instrumentation
(spans, meters), and relay backoff helpers are ported unchanged into the neutral store; none
are removed or degraded.

## Consequences

- **One store to maintain.** Adding a new backend requires a `Dialect` implementation and
  optional capability stubs; no new store package is needed.
- **Cross-store atomicity is now ergonomic.** Multiple stores share one ambient transaction
  via context; no driver handles are threaded through intermediate layers.
- **Behavior-preserving.** The 3-dialect conformance suite gates the collapse; any observable
  difference in stored state, error semantics, or timestamp handling is a test failure before
  merge.
- **`MySQLDeduper` removed (breaking).** Code that type-asserted to `MySQLDeduper` or passed
  `*sql.Tx` to `Seen` must migrate to the new ambient-transaction signature (see §5).
- **`ErrUnsupported` is the contract for optional capabilities.** Callers that require
  `Notifier` or `Locker` must guard with `errors.Is(err, dialect.ErrUnsupported)` and
  choose a fallback (poll for relay, skip ownership-dependent paths for SQLite).
- **ADR-0080 UTC discipline is preserved.** The `TimestampsAsText()` flag is the single
  decision point for time (de)serialization; all scan sites call `.UTC()` as defence-in-depth
  per ADR-0080.
- **ADR-0079 extraction constraint is unaffected.** `internal/persistence/store` and
  `internal/persistence/dialect` are ordinary consumers that may import `runtime`, `model`,
  etc.; only `internal/database` and `internal/database/transaction` are bound by the
  extraction check.
- **SQLite capability gaps are explicit.** SQLite's `Locker` returns `ErrUnsupported`
  (fail-loud); SQLite deployments that call ownership-dependent flows will receive a clear
  error rather than silent degradation. See ADR-0082 for the full SQLite backend scope.
