# MySQL persistence backend — design

Date: 2026-06-28
Status: **Approved** (maintainer greenlit 2026-06-28). Tech-stack change recorded in ADR-0073.
Relates to: ADR-0004 (no `pkg/` prefix), ADR-0017/0025 (transactional outbox + atomic write),
ADR-0020 (advisory-lock ownership), ADR-0027 (timer rehydration), ADR-0031 (call-link lease),
ADR-0059/0061 (elector + heartbeat), ADR-0072 (Option A leadership re-arm).

## Goal

Add **MySQL 8.0+** as an alternative SQL backend alongside PostgreSQL 17, behind the *same*
`runtime.*` persistence ports, so a library consumer can run the engine on either database with no
engine/model/runtime changes. PostgreSQL remains the primary/reference backend.

## Decisions (settled with maintainer)

1. **Parallel tree.** New `internal/persistence/mysql/` mirrors `internal/persistence/postgres/`.
   The Postgres package is **not modified** — zero regression risk to battle-tested paths. Some
   orchestration logic is duplicated across the two trees; this is accepted in exchange for isolation
   and because the dialect differences are pervasive enough that a shared abstraction would leak.
2. **MySQL 8.0+ floor.** Enables `FOR UPDATE SKIP LOCKED`, the native `JSON` type, CTEs, and
   `RELEASE_ALL_LOCKS()` so the MySQL impl can mirror the Postgres claim/lease/relay logic closely.
3. **Phased delivery.** One spec, one plan, executed and merged in 5 vertical-slice phases, each
   green before the next.

## Architecture

- **Package:** `internal/persistence/mysql/` (non-exported, per ADR-0004 consumers reach it through
  the root `persistence` facade only).
- **Driver:** stdlib `database/sql` + `github.com/go-sql-driver/mysql`. DSN must set
  `parseTime=true&loc=UTC` so `DATETIME(6)` round-trips as UTC `time.Time`.
- **Driver seam:** a `mysql`-local `DBTX` interface over `database/sql` types
  (`ExecContext`/`QueryContext`/`QueryRowContext`, `BeginTx`), satisfied by `*sql.DB`, `*sql.Tx`,
  `*sql.Conn` — the analog of postgres's pgx-typed `dbtx.go`.
- **Public facade:** parallel constructors added to the root `persistence` package, each taking
  `*sql.DB` and returning the **same interface types** the Postgres constructors return:
  `OpenMySQL`, `MigrateMySQL`, `NewMySQLRelay`, `NewMySQLLister`, `NewMySQLCallLinkStore`,
  `NewMySQLTimerStore`, `NewMySQLChainLinkStore`, `NewMySQLAdvisoryLockOwnership`,
  `NewMySQLDefinitionStore`, `NewMySQLDeduper`, `NewMySQLPruner`. Scheduling adds
  `scheduling.WithMySQLTimerElector(db, opts...)`.

## Dialect translation reference

| Concern | PostgreSQL | MySQL 8.0 |
|---|---|---|
| Placeholders | `$1,$2` | `?,?` |
| Insert-returning-id | `RETURNING` | none → `LastInsertId()` (auto-inc) or `SELECT` after write |
| Claim-and-read | `UPDATE … RETURNING` | `SELECT … FOR UPDATE SKIP LOCKED` (tx) then `UPDATE` by PK |
| Upsert | `INSERT … ON CONFLICT (k) DO UPDATE` | `INSERT … ON DUPLICATE KEY UPDATE` |
| Insert-or-ignore | `INSERT … ON CONFLICT DO NOTHING` | `INSERT IGNORE` |
| JSON storage | `JSONB` | `JSON` |
| JSON introspection (lister incident count) | `jsonb_array_length`, `jsonb_typeof` | `JSON_LENGTH`, `JSON_TYPE`, `JSON_EXTRACT` |
| Timestamps | `TIMESTAMPTZ` | `DATETIME(6)` storing UTC |
| Auto-increment PK | `BIGSERIAL` | `BIGINT AUTO_INCREMENT` |
| Set membership | `id = ANY($2)` (`[]int64`) | `id IN (?,…)` expanded at call time |
| Session advisory lock | `pg_try_advisory_lock`, `pg_advisory_unlock_all` | `GET_LOCK(name,0)`, `RELEASE_ALL_LOCKS()` |
| Relay/notifier wake | `LISTEN/NOTIFY` | **none → poll-only** |
| Optimistic-CAS signal | `WHERE version=? ` + SQLSTATE `40001` | `WHERE version=?` + `RowsAffected()==0` |
| Transient-conflict retry | serialization failure `40001` | deadlock `1213`, lock-wait-timeout `1205` |
| Unique violation | SQLSTATE `23505` | error `1062` |
| Row-skip locking | `FOR UPDATE SKIP LOCKED` | same (8.0+) |
| Batch | pgx `Batch`/`SendBatch` | prepared-statement loop / multi-row `INSERT` |

## Component notes

### Store (Phase 1)
`Create`/`Load`/`Commit`/`Entries`. Single `*sql.Tx` writes snapshot + journal + outbox (+ timer +
call-link rows where applicable), mirroring ADR-0025. **CAS:** `Commit` runs
`UPDATE wrkflw_instances SET version=version+1, snapshot=?, updated_at=? WHERE instance_id=? AND
version=?`; if `RowsAffected()==0` → `runtime.ErrConcurrentUpdate`. Deadlock (`1213`) and lock-wait
timeout (`1205`) are wrapped to `ErrConcurrentUpdate` so the engine's existing retry loop handles
them. Timer upsert uses `ON DUPLICATE KEY UPDATE` keyed on `(instance_id, timer_id)`.

### TimerStore (Phase 1)
`ListArmed` — straight `SELECT`, dialect-trivial. Write side is fused into Store.

### Relay (Phase 2)
**Poll-only** (no `LISTEN/NOTIFY`). `DrainOnce` claims a batch in a tx:
`SELECT id,… FROM wrkflw_outbox WHERE status=? AND next_attempt_at<=? ORDER BY id
FOR UPDATE SKIP LOCKED LIMIT ?`, publishes via `runtime.Publisher`, then `UPDATE` status/retry. `Run`
loops on the poll interval (the existing `RelayOption` knobs carry over; the NOTIFY-wake option is a
no-op/absent on MySQL). DLQ `ListDeadLettered` + `Redrive` reuse the logic with `IN (?,…)`.

### Deduper (Phase 2)
`INSERT IGNORE` on the dedup unique key; consumer idempotency unchanged.

### CallLinkStore (Phase 3)
Lease mode via `SELECT … FOR UPDATE SKIP LOCKED` then `UPDATE … SET claimed_at=?,claimed_by=?`
(two statements in a tx, since no `RETURNING`). Plain mode and the other reads are dialect-trivial.

### Ownership (Phase 3)
`MySQLAdvisoryLockOwnership` holds a dedicated `*sql.Conn`; `Acquire` → `GET_LOCK(key,0)` (returns 1
on success), `Release` → `RELEASE_LOCK(key)`, `Close` → `RELEASE_ALL_LOCKS()` + conn close. **GET_LOCK
names are capped at 64 chars in MySQL 8.0**, so the instance ID is hashed (e.g. SHA-256 hex truncated,
or a stable short encoding) to a ≤64-char key. Sticky in-memory owned-set mirrors
`AdvisoryLockOwnership`.

### ChainLinkStore / Lister (Phase 3)
ChainLink: trivial SQL. Lister: keyset pagination `(started_at DESC, instance_id DESC)` identical;
incident-count aggregation uses `JSON_LENGTH(JSON_EXTRACT(snapshot,'$.Incidents'))` with a
`JSON_TYPE` guard for the null/absent case.

### DefinitionStore / Pruner / health (Phase 4)
DefinitionStore: `ON DUPLICATE KEY UPDATE` upsert keyed `(def_id, version)`; `Lookup`/`Get` reads.
Pruner: `DELETE … WHERE <ts> < ?` per table. Health: a MySQL `PingCheck` (`db.PingContext`).

### MySQLElector (Phase 5)
`gocron.Elector` backed by `GET_LOCK(leader-key,0)` on a dedicated `*sql.Conn`, heartbeat via
`conn.PingContext` (ADR-0061 step-down), `Close` via `RELEASE_ALL_LOCKS()`. Option A's
`WithOnLeadershipAcquired` callback machinery is reused verbatim (it is driver-agnostic). Facade:
`scheduling.WithMySQLTimerElector(db, opts...)`.

## Migrations

goose (already the Postgres tool, supports MySQL) with an embedded FS at
`internal/persistence/mysql/migrations/`. Same logical schema as Postgres (instances, journal, outbox,
definitions, processed_message, call_links, timers, chain_links) expressed in MySQL DDL: `JSON`,
`DATETIME(6)`, `BIGINT AUTO_INCREMENT`, appropriate indexes. `MigrateMySQL(ctx, *sql.DB)` runs them
with goose's `mysql` dialect.

## Testing

- **Helper:** `database.RunTestMySQL(t *testing.T, opts ...TestOption) *sql.DB` in the owning module's
  testutils (modeled exactly on `database.RunTestDatabase`, per the use-testcontainers skill): MySQL
  8.0 testcontainer, DSN with `parseTime=true&loc=UTC`, migrations applied, returned `*sql.DB`.
- **Per-port black-box `_test.go`** mirroring the Postgres behavioral tests (same assertions, MySQL
  handle). `<file>.go` ↔ `<file>_test.go` pairing per project convention.
- `go test -race`, `goleak` where goroutines exist (relay, ownership, elector), **≥85% line coverage
  per package**, `golangci-lint` clean.

## Phasing

1. **Foundation** — deps + ADR-0073 + tech-stack table update + `RunTestMySQL` + migrations + `DBTX`
   seam + `Store` (CAS) + `TimerStore` + facade `OpenMySQL`/`MigrateMySQL`/`NewMySQLTimerStore`.
2. **Relay** — poll outbox drain + DLQ/redrive + `Deduper` + facade `NewMySQLRelay`/`NewMySQLDeduper`.
3. **Correlation** — `CallLinkStore` + `Ownership` + `ChainLinkStore` + `Lister` + facades; verify
   `CallNotifier` works with the MySQL call-link store.
4. **Definitions/ops** — `DefinitionStore` + `Pruner` + MySQL health `PingCheck` + facades.
5. **Scheduling** — `MySQLElector` + `scheduling.WithMySQLTimerElector` + `examples/` MySQL wiring.

## Out of scope / non-goals

- No change to PostgreSQL code or behaviour.
- No dialect-abstraction refactor (explicitly rejected).
- No MySQL 5.7 support (EOL; lacks `SKIP LOCKED`).
- No MariaDB-specific tuning (8.0 `RETURNING` differences mean we target MySQL semantics; MariaDB may
  work but is untested).
- No `LISTEN/NOTIFY` equivalent (poll-only is accepted).

## Consequences

- The library gains a second supported backend with identical engine semantics; consumers select it
  via the `*MySQL*` facade constructors. Postgres remains default.
- Timer latency on MySQL relay/notifier is bounded by the poll interval (no push wake).
- The two parallel trees must be kept behaviourally in sync; the shared port test expectations are the
  guardrail (a future refactor could extract a shared conformance test suite — noted, not done here).
