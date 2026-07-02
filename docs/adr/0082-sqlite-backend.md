# 0082. SQLite backend (lightweight / single-node / test)

Status: **Accepted — 2026-07-02.**
Plan: `docs/plans/2026-07-02-store-unification-dialect-sqlite.md`.
Spec: `docs/specs/2026-07-02-store-unification-dialect-sqlite-design.md`.
Depends on: ADR-0081 (store unification + dialect abstraction), ADR-0079 (database transaction toolkit), ADR-0080 (UTC time discipline).

## Context

The two-axis model introduced in ADR-0081 (access mechanism × SQL dialect) makes adding a
third SQL dialect a matter of implementing `dialect.Dialect` plus optional capability stubs,
with no new store package required. A lightweight backend has been a standing need for three
use-cases:

1. **Local / developer / CI testing** — spinning up a Postgres or MySQL container for every
   unit test is expensive. A zero-Docker, in-process store lets developers run the full test
   suite with no daemon dependency.
2. **Single-node embedded deployments** — consumers who embed the engine in a standalone
   Go binary (e.g. a CLI tool or a small sidecar) should not require a separate database
   process for low-throughput workflows.
3. **Integration smoke tests** — running the 3-dialect conformance suite (ADR-0081 §6)
   requires a third real SQL backend, not just a mock.

SQLite 3.35+ supports `UPDATE … RETURNING`, WAL journal mode for concurrent readers, and
`INSERT … ON CONFLICT DO NOTHING`; it is widely used as an embedded SQL engine. The
`modernc.org/sqlite` driver provides a **pure-Go** implementation with no cgo dependency,
keeping the module's build graph clean across all target platforms.

## Decision

We add SQLite as a first-class third dialect, accessible via `persistence.OpenSQLite` and
`persistence.MigrateSQLite`, using `modernc.org/sqlite` as the driver.

### 1. Driver and DSN

`modernc.org/sqlite` (pure-Go, no cgo) is registered under the `"sqlite"` driver name via
`database/sql`. The DSN passed to `sql.Open` enables:

- `_journal_mode=WAL` — write-ahead log for concurrent read access while a write is in progress.
- `_busy_timeout=5000` — 5-second busy-wait on locked pages before returning `SQLITE_BUSY`.
- `_foreign_keys=on` — referential-integrity enforcement.

### 2. Single-writer constraint

`SetMaxOpenConns(1)` is applied to every SQLite `*sql.DB`. This serializes all writes through
a single connection, eliminating `SQLITE_BUSY` write–write conflicts and making the
`FOR UPDATE SKIP LOCKED`-equivalent claim paths safe without row-level locking. This is
appropriate for the single-node / test-oriented use-cases; multi-node production deployments
must use Postgres (advisory locks, NOTIFY) or MySQL (GET_LOCK).

### 3. RETURNING and the leased-claim path

SQLite ≥ 3.35 supports `UPDATE … RETURNING`, so `dialect.NewSQLite()` returns `true` from
`SupportsReturning()`. Because SQLite has no `FOR UPDATE SKIP LOCKED`, it returns `false`
from `SupportsSkipLocked()`. The leased-claim path in `CallLinkStore` branches on these two
flags:

- Postgres (both true): `UPDATE … FROM (SELECT … FOR UPDATE SKIP LOCKED) … RETURNING`.
- MySQL (Returning=false, SkipLocked=true): `SELECT … FOR UPDATE SKIP LOCKED` + `UPDATE`.
- SQLite (Returning=true, SkipLocked=false): `UPDATE … WHERE child_instance_id IN (SELECT … [LIMIT n]) … RETURNING`.

The single-writer constraint makes the SQLite path safe without row locking.

### 4. Timestamp storage (ADR-0080 compliance)

SQLite has no native `TIMESTAMP` column type. All timestamp columns are declared as `TEXT`
and store values in RFC3339Nano format. `dialect.NewSQLite().TimestampsAsText()` returns
`true`; all store and lister sites gate their time bind/scan logic on this flag. This
preserves full UTC-instant fidelity (ADR-0080) without introducing `julianday()` or integer
epoch representations, and keeps the values human-readable in the database file.

The `database.ProbeUTC` called in `OpenSQLite` uses the SQLite-specific probe expression to
verify that the round-trip instant is preserved before the store enters service.

### 5. Migration

A single consolidated Goose migration file (`store/migrations/sqlite/0001_init.sql`) creates
all wrkflw tables in one step. Consumers call `persistence.MigrateSQLite(ctx, db)` before
first use.

### 6. Advisory locking (unsupported — fail-loud)

SQLite provides no session-level or named advisory lock. `dialect.NewSQLite()` does not
implement `dialect.Locker`; `store.NewSQLiteLocker()` returns `dialect.ErrUnsupported` from
both `TryLock` and `Unlock` (fail-loud). The facade constructor `NewSQLiteOwnership` surfaces
this: it returns an `AdvisoryLockOwnership` that reports `dialect.ErrUnsupported` on the
first `Acquire` call.

Ownership-dependent flows (multi-replica timer exclusivity, leader-election patterns) must
guard with `errors.Is(err, dialect.ErrUnsupported)` and skip or substitute when running on
SQLite. SQLite is explicitly not designed for multi-node deployments.

### 7. LISTEN/NOTIFY (absent — poll fallback)

SQLite has no native pub/sub mechanism. `dialect.NewSQLite()` does not implement
`dialect.Notifier`. The relay falls back to timer-based polling when no `Notifier` is
injected, which is the same fallback used for MySQL. `NotifyStatement` returns `""`.

### 8. Public facade

Two functions are added to the root `persistence` package:

- `persistence.OpenSQLite(ctx, db) (*SQLiteStore, error)` — runs `ProbeUTC` as a fail-fast
  check, applies `SetMaxOpenConns(1)`, and wires the neutral store with `dialect.NewSQLite()`.
- `persistence.MigrateSQLite(ctx, db) error` — runs the consolidated Goose migration.

### 9. Extraction constraint (ADR-0079)

Test helpers that import `modernc.org/sqlite` (e.g. `dbtest.RunTestSQLite`) live in the
`internal/dbtest` package, which already falls outside the extraction boundary. The
`internal/database` and `internal/database/transaction` packages do not import
`modernc.org/sqlite`, so the `go list -deps` extraction check (ADR-0079 §6) remains green.
A `database.SQLite` dialect probe constant is added to `internal/database` to enable
dialect-aware routing at the access layer without importing the SQLite driver.

## Consequences

- **Zero-Docker test baseline.** The full conformance suite and unit tests run with no
  container daemon. Developer iteration time falls for tests that previously required
  Postgres or MySQL containers.
- **Single-node embedded deployments are supported.** Consumers can wire `OpenSQLite` to
  embed the engine in a Go binary with no external database process.
- **Not for multi-node or high-concurrency.** The single-writer constraint and absent advisory
  locks mean SQLite is unsuitable for multi-replica deployments, high-write throughput, or any
  flow that requires distributed leader election. Deployments that need those properties must
  use Postgres (preferred) or MySQL.
- **Pure-Go dependency added.** `modernc.org/sqlite` is a new module dependency. It is
  pure-Go (no cgo), which avoids cross-compilation issues, but adds ~10 MB to the compiled
  binary size for consumers that import the SQLite facade.
- **`dialect.ErrUnsupported` is the contract for unsupported capabilities.** Code that calls
  `Acquire` on a SQLite-backed `Ownership` will receive `dialect.ErrUnsupported` on the first
  call. Consumers must guard and degrade gracefully; silent pass-through is intentionally
  absent.
- **ADR-0080 UTC discipline holds.** The `TimestampsAsText()` flag, RFC3339Nano encoding, and
  `ProbeUTC` at `Open` time enforce the same UTC-instant guarantee as Postgres and MySQL.
- **Relay wake-up is poll-based.** Without NOTIFY, the relay wakes on a fixed poll interval.
  This is acceptable for the single-node / test use-cases; it is another reason SQLite is
  not suitable for latency-sensitive multi-replica production flows.
