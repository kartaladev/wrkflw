# Persistence Store Unification + Dialect Abstraction + SQLite (Phase 2)

**Status:** Design approved 2026-07-02. Implementation not started.
**Predecessors:** ADR-0079 (database transaction toolkit), ADR-0080 (UTC time discipline), ADR-0073 (MySQL persistence backend).
**Supersedes at implementation:** the separate `internal/persistence/postgres` and `internal/persistence/mysql` store packages.

## Goal

Collapse the two near-duplicate dialect store packages into **one neutral store** written against the `database.Querier` + `internal/database/transaction` toolkit, driven by an explicit **`Dialect`** strategy, and prove the seam by adding a **SQLite** backend. This serves four objectives at once:

1. **Cut duplication** — the two packages are ~80% structurally parallel; unify them.
2. **Toolkit ergonomics** — every store executes through `database.Querier`; transactions go through the ambient `transaction` toolkit.
3. **Enable a 3rd driver** — SQLite (`database/sql`, pure-Go `modernc.org/sqlite`) lands in this program; adding a 4th later is cheap.
4. **Cross-store atomicity** — multiple stores participate in one ambient, context-propagated transaction.

Approach chosen: **big-bang unification** (single spec/program), de-risked by a 3-dialect conformance test suite that preserves the existing per-dialect behavior as a parity baseline.

## Context (current state)

- `internal/persistence/postgres/` and `internal/persistence/mysql/` mirror each other file-for-file (store, relay, lister, call_links, chainlink, dedup, definitions, ownership, pruner, timerstore, migrate, + pure-Go history_cap/trigger_codec/relay_backoff). Roughly ~80% is identical modulo the `$n`↔`?` placeholder and the upsert/ignore clause; genuinely divergent code is confined to relay notify, ownership locks, and the call-links leased-claim path.
- Each package defines its **own** driver-shaped `DBTX` seam and stores a concrete `*pgxpool.Pool` / `*sql.DB`; the two seams are method-incompatible (`Exec` vs `ExecContext`, `pgconn.CommandTag` vs `sql.Result`, `pgx.Row` vs `*sql.Row`).
- Transactions are begun/committed **inline** in each multi-write method (`pool.Begin`/`db.BeginTx` + `defer rollback`); the tx is threaded as an explicit `db DBTX` parameter. No closure/ambient helper is actually used. `Deduper.Seen` leaks the concrete tx type (`pgx.Tx` / `*sql.Tx`) — the only driver type in a public-ish signature.
- The `internal/database` toolkit (`Querier`, `From`, `BeginTx`, `Batcher`, `UTC`, `ProbeUTC`) and `internal/database/transaction` (`Begin`/`JoinOrBegin`/`MarkRollback`/`IsRollbackMarked`) exist and are unit-tested but are **not** used by any store; the root `persistence` facade uses only `From`+`ProbeUTC` for a fail-fast UTC probe at `Open*`.

### The misnaming, and the two-axis model

The package names `postgres`/`mysql` describe the *database*, but the real distinction is the **access mechanism**, orthogonal to the **SQL dialect**:

| Access mechanism | SQL dialect(s) it can serve |
|---|---|
| `pgx` | Postgres |
| `database/sql` | MySQL, SQLite, Postgres-via-stdlib, … |

`pgx` is Postgres-only, so `pgx ⟹ Postgres` dialect always. `database/sql` is dialect-agnostic. The design factors these two axes apart: the **access adapters already live (unexported) in `internal/database`** behind `Querier`; the **SQL dialect** becomes an explicit `Dialect` value. After unification there is no per-database store package, so nothing is misnamed — dialects are named for the SQL dialect (`Postgres`/`MySQL`/`SQLite`) and access for the mechanism (pgx / `database/sql`).

## Architecture

### Package layout

```
internal/database/              (unchanged) access layer: Querier, From, BeginTx,
                                  Batcher, UTC, ProbeUTC; pgx + database/sql adapters.
internal/database/transaction/  (unchanged) ambient, context-propagated tx.
internal/persistence/store/      NEW, neutral — the single set of store implementations
                                  (Store, Relay, Lister, TimerStore, CallLinkStore,
                                   ChainLinkStore, Deduper, DefinitionStore, Pruner,
                                   Ownership) over database.Querier + dialect.Dialect.
                                  Pure-Go helpers (history capping, trigger codec,
                                   relay backoff) live here exactly once.
internal/persistence/dialect/    NEW — Dialect interface + Postgres / MySQL / SQLite
                                  implementations + capability interfaces (Notifier, Locker).
internal/persistence/postgres/   DELETED
internal/persistence/mysql/      DELETED
```

The `store` and `dialect` packages are ordinary consumers — they may import `runtime`, `model`, etc. Only `internal/database` + `internal/database/transaction` are bound by the extraction constraint (ADR-0079), and this work does not touch them, so the CI `extraction` check is unaffected.

### The Dialect abstraction

`dialect.Dialect` factors out every difference that is *SQL text or driver-error classification*:

```go
type Dialect interface {
    Name() string

    // Rebind converts a query written with ? placeholders into this dialect's
    // placeholder style ($1.. for Postgres; ? left as-is for MySQL/SQLite).
    Rebind(query string) string

    // Divergent SQL fragments / statements.
    UpsertTimer() string          // ON CONFLICT..DO UPDATE | ON DUPLICATE KEY UPDATE | ON CONFLICT (SQLite)
    UpsertDefinition() string
    InsertIgnoreDedup() string     // ON CONFLICT DO NOTHING | INSERT IGNORE | INSERT OR IGNORE
    JournalTriggerColumn() string  // "trigger" (PG/SQLite) vs "trigger_" (MySQL reserved word)
    OutboxStatsQuery() string      // FILTER(WHERE) vs conditional aggregation
    NotifyStatement(channel string) string // "NOTIFY <ch>" (Postgres) | "" (MySQL/SQLite)

    // Capability flags that pick a code path.
    SupportsReturning() bool       // leased-claim: UPDATE..RETURNING vs SELECT..FOR UPDATE SKIP LOCKED + UPDATE

    // Error classification against the dialect's expected driver.
    IsUniqueViolation(err error) bool
    IsRetryableConflict(err error) bool // serialization failure / deadlock / SQLITE_BUSY
}
```

Per-dialect error mapping targets each dialect's **expected** driver:
- **Postgres** ⟵ `pgconn.PgError`: `23505` unique, `40001` serialization.
- **MySQL** ⟵ `go-sql-driver` `mysql.MySQLError`: `1062` unique, `1213`/`1205` deadlock/lock-wait.
- **SQLite** ⟵ `modernc.org/sqlite` codes: `SQLITE_CONSTRAINT_UNIQUE`, `SQLITE_BUSY`.

(Postgres-over-`database/sql` is *designed for* by the two-axis model but **not wired** in this program, so `pq`/stdlib error mapping is out of scope here.)

### Capability interfaces

Divergences that are not merely SQL text — and do not exist in every dialect — are modeled as optional capabilities injected into the stores that need them:

```go
type Notifier interface {  // receive side of LISTEN/NOTIFY — (pgx + Postgres) only
    Listen(ctx context.Context, channel string) (<-chan struct{}, func(), error)
}
type Locker interface {    // Postgres advisory locks | MySQL GET_LOCK | SQLite: ErrUnsupported
    TryLock(ctx context.Context, key string) (bool, error)
    Unlock(ctx context.Context, key string) error
}
```

The three divergent islands resolve as:

- **Relay notify** splits by axis. The **send** side (`NOTIFY`) is Postgres *SQL* via `Dialect.NotifyStatement`, run inside the commit tx (works over any Postgres access; empty for MySQL/SQLite). The **receive** side (`LISTEN` + wait) needs pgx and is the `Notifier` capability, present only for (pgx, Postgres). When no `Notifier` is injected, the relay **polls** (today's MySQL/SQLite behavior).
- **Ownership** requires a `Locker`. Postgres uses `pg_try_advisory_lock(hashtextextended(...))`; MySQL uses `GET_LOCK(sha256(...))`. **SQLite has no lock primitive → its `Locker.TryLock` returns `(false, ErrUnsupported)`**; ownership-dependent flows must guard/skip under SQLite (documented in `OpenSQLite` godoc + ADR). This is deliberately fail-loud to prevent silent multi-node misuse on SQLite.
- **Leased-claim** is chosen by `Dialect.SupportsReturning()`: PG/SQLite use `UPDATE…RETURNING`; MySQL uses `SELECT…FOR UPDATE SKIP LOCKED` + a separate `UPDATE`.

### Store construction & transaction integration

Each store holds the raw conn (to begin transactions / do pool reads), its `Dialect`, and any injected capabilities:

```go
type Store struct {
    conn    any             // *pgxpool.Pool | *sql.DB
    dialect dialect.Dialect
    notify  Notifier        // optional
}
```

A small internal helper routes every operation through the ambient transaction when one is present in `ctx`:

- **Reads** — `q := s.querier(ctx)` returns the ambient-tx `Querier` if `ctx` carries one, else `database.From(s.conn)`; the query text is passed through `s.dialect.Rebind`.
- **Multi-write methods** (`Create`, `Commit`, relay `DrainOnce`) — `q, err := transaction.JoinOrBegin(ctx, s.conn)` yields a `Querier` that is either the **joined** ambient tx (its `Commit` is a no-op — the outer owner commits) or a **fresh leaf owner**. This is what delivers cross-store atomicity: a caller runs `transaction.Begin(ctx, pool)` then invokes `Store.Commit`, `Deduper.Seen`, etc. on that one transaction.

`Deduper.Seen` loses its concrete-tx parameter and becomes `Seen(ctx context.Context, …)`: the caller begins the ambient transaction (via `transaction.Begin`), and `Seen` joins it through `ctx` exactly like the other stores. This removes the only place a driver tx type currently leaks into a public signature.

The hot `Store.Commit` path (snapshot CAS + journal append + outbox insert + notify), unified:

```go
q, err := transaction.JoinOrBegin(ctx, s.conn)
// CAS UPDATE, journal INSERT (s.dialect.JournalTriggerColumn()), outbox INSERT — via q + Rebind.
if stmt := s.dialect.NotifyStatement("wrkflw_outbox"); stmt != "" {
    _, _ = q.Exec(ctx, stmt) // NOTIFY inside the tx; "" for non-Postgres.
}
// on conflict: classify via s.dialect.IsRetryableConflict / IsUniqueViolation; transaction.MarkRollback(ctx).
return q.Commit(ctx) // real commit if owner, no-op if joined.
```

### SQLite specifics

- **Driver:** `modernc.org/sqlite` (pure-Go, no CGo), reached via `database/sql` → `database.From(*sql.DB)`.
- **Concurrency:** single-writer. Connections open with `journal_mode=WAL` and a `busy_timeout`; `IsRetryableConflict` maps `SQLITE_BUSY`. Intended for lightweight / single-node / test use — **not** high-concurrency production. Documented in `OpenSQLite` godoc + the SQLite ADR.
- **Ownership:** `ErrUnsupported` (above).
- **Migrations:** a new SQLite-compatible migration set under the goose SQLite dialect (no `TIMESTAMPTZ`; `DATETIME`/text time; no advisory-lock objects). Authoring + validating this schema is a distinct sub-task.
- **RETURNING:** SQLite ≥3.35 supports it (modernc is current), so leased-claim uses the RETURNING path; concurrency is effectively serialized.

## Public API & migration

- Facade signatures stay stable to minimize consumer churn: `persistence.OpenPostgres(ctx, pool, …)` and `persistence.OpenMySQL(ctx, db, …)` unchanged; **add** `persistence.OpenSQLite(ctx, db, …)`. Each wires `database.From` + the correct `Dialect` + capabilities, then constructs the neutral store.
- `runtime.Store`, `runtime.InstanceLister`, `runtime.TimerStore`, `runtime.CallLinkStore`, `runtime.ChainLinkStore`, and the facade-local `Store`/`Relay`/`DefinitionStore` interfaces are **unchanged** — the neutral store satisfies them.
- Options are reconciled onto the neutral store. Capability-specific options (e.g. `WithListenNotify`) become **no-ops** when the dialect/access cannot honor them; the facade keeps type aliases where practical.
- `internal/persistence/{postgres,mysql}` are **deleted**; all imports, examples, and wiring updated.

## Testing / conformance strategy

The parity safety net for a big-bang rewrite:

- One **parametrized conformance suite** runs the same behavioral assertions against all three dialects. A dialect fixture supplies a live conn: **PG** via `dbtest.RunTestDatabase` (testcontainers), **MySQL** via `dbtest.RunTestMySQL` (testcontainers), **SQLite** via a new `dbtest.RunTestSQLite` (in-memory/file, no container).
- The existing `postgres_test` / `mysql_test` assertions are folded into the shared suite as the baseline so behavior cannot regress. Capability-gated cases branch: SQLite ownership asserts `ErrUnsupported`; LISTEN/NOTIFY receive-side runs only for (pgx, Postgres); everything else runs on all three.
- Gates: ≥85% coverage per new package, `go test -race` clean, `golangci-lint` clean, and the `internal/database` extraction check still green (unaffected).
- SQLite migrations are validated by running migrate + the conformance suite against a fresh SQLite database.

## Non-goals / deferred

- **Postgres-over-`database/sql`** — enabled by the model, not wired here (would need pq/stdlib error mapping).
- **`database.Rebind` in the toolkit** — rebinding lives in `Dialect.Rebind`; not promoted into `internal/database` unless a second consumer needs it.
- **Savepoints / nested real transactions** — flat-join semantics only, as in the toolkit.
- High-concurrency SQLite; multi-node SQLite ownership.

## Plan sequencing (kept green throughout)

1. `dialect` package — interface + Postgres/MySQL/SQLite implementations + capability interfaces (+ unit tests).
2. SQLite enablement — add `modernc.org/sqlite`, `dbtest.RunTestSQLite`, author + validate SQLite migrations.
3. Neutral `store` package — port the ~80% (store/lister/timerstore/chainlink/definitions/dedup/pruner) onto `Querier`+`Dialect`+`transaction`, standing up the 3-dialect conformance suite as each store ports.
4. Divergent islands — relay (notify-send dialect + listen capability + poll fallback), ownership (`Locker`; SQLite `ErrUnsupported`), leased-claim (`SupportsReturning`).
5. Facade — rewire `OpenPostgres`/`OpenMySQL`, add `OpenSQLite`, reconcile options.
6. Delete `internal/persistence/{postgres,mysql}`; update all call sites/examples; full verification.
7. ADRs — dialect abstraction + store unification (+ rename), and SQLite backend.

## Risks

- **Hot-path rewrite.** `Store.Commit`/`Create` are the core state-machine persistence path. Mitigation: conformance suite folds in the existing per-dialect assertions before porting; port store-by-store with the suite green at each step.
- **Notify capability spans both axes** (needs pgx access *and* Postgres dialect). Mitigation: send side is dialect SQL, receive side is the `Notifier` capability; relay degrades to polling without it.
- **SQLite schema divergence.** Hand-authored SQLite migrations must match the semantics of the PG/MySQL schema. Mitigation: conformance suite runs the full store behavior against SQLite.
- **Large blast radius** (delete two packages, update ~all persistence call sites/examples). Mitigation: facade API kept stable; compile-gate (`go test -run '^$' ./...`) + full suite before merge.

## Global constraints

- Go 1.25.
- Error sentinels use the `workflow-<package>:` prefix.
- TDD strict; black-box tests (`store_test`, `dialect_test`); the project `table-test`, `use-testcontainers`, `use-mockgen` skills apply.
- Real databases via testcontainers (PG, MySQL) / in-process SQLite — never mocked.
- `internal/database` + `internal/database/transaction` remain extraction-clean; the CI `extraction` check must stay green.
- Writes keep `time.Now().UTC()`; reads keep UTC normalization (ADR-0080).
