# Proposal

## Why

A consumer whose data layer is GORM has no supported way to point wrkflw at its `*gorm.DB`, or to start an instance in the same transaction as its own rows. Once `add-lent-transaction-seam` and `split-persistence-modules` land, wrkflw will have the pieces: a lent transaction, a `Continuation`, and the `persistence/sql` path. The consumer still has to dig the `*sql.DB` and `*sql.Tx` out of GORM, pass the dialect by hand, supply an error classifier for GORM's cgo SQLite driver, and remember to call `Continuation.Resume` after GORM commits. kartaladev/wrkflw#31 (decided) settled the shape: an interop adapter on the `sql` path, not a fourth store, plus a `Transaction` wrapper that runs lend, commit and resume itself, so the consumers most likely to forget `Resume` cannot forget it.

## What Changes

Absorbs kartaladev/wrkflw#42, folded in whole: its decided surface, its deliverables and its conformance list. Builds kartaladev/wrkflw#31 (decided), calls 1–6, without reopening them. The module's place in the topology was decided in kartaladev/wrkflw#30, and the seam it adapts in kartaladev/wrkflw#22 (calls 2, 3, 4 and 6). On re-measurement against GORM v1.31.2, two premises in #42 hold but say less than they seem to (design, Context). `db.DB()` does not refuse a `*gorm.DB` that is inside a transaction. It silently returns the parent `*sql.DB`, so the refusal in `From` is required, not defensive. And `tx.Statement.ConnPool` is a `*sql.Tx` only when `PrepareStmt` is off (Open Question 2).

- **New module `persistence/gorm`** (package `gormpersistence`). Its non-test graph imports `gorm.io/gorm` and wrkflw's `persistence` and `persistence/sql` modules, and nothing from `gorm.io/driver/*`, any SQL driver, or cgo.
- **`gormpersistence.Transaction(ctx, db, fc, opts ...*sql.TxOptions) error`**, the documented path. It runs `fc` inside `db.Transaction` with a context that lends GORM's transaction through `persistence.WithTx`. After GORM commits it calls `Continuation.Resume`. When GORM rolls back, it calls `Continuation.Discard` and returns `fc`'s error unchanged. A panic in `fc` discards and re-panics.
- **`gormpersistence.From(db) (persistence.Conn, error)`**: `db.DB()` goes to `sql.FromDB`, and the dialect comes from `db.Dialector.Name()` (`postgres`, `mysql`, `sqlite`; anything else is refused). A `db` already inside a transaction is refused with an error naming `FromTx`.
- **`gormpersistence.FromTx(tx) (persistence.Tx, error)`**: the `*sql.Tx` in `tx.Statement.ConnPool` goes to `sql.FromTx`; anything that is not a `*sql.Tx` is refused. This is for consumers who manage `db.Begin()` themselves and call `WithTx`, `Resume` and `Discard` on their own.
- **An exported SQLite error classifier.** It matches SQLite's own message text (`UNIQUE constraint failed`, `database is locked`) with no cgo import. `From` and `FromTx` install it for the `sqlite` dialect through `sql.WithErrorClassifier`, so duplicate creation still yields `persistence.ErrInstanceExists`, and a busy database still yields `kernel.ErrConcurrentUpdate`, under GORM's `mattn/go-sqlite3` driver.
- **Migrations reuse the goose set** through `persistence.NewMigrator(From(db))`. The adapter never calls `AutoMigrate` on an engine table.
- **`examples/gorm_wiring`**: a consumer table and an engine instance created in one `gormpersistence.Transaction`. A returned error leaves neither row, and after a commit the instance continues. It runs on SQLite by default with no Docker, and has a Postgres variant behind `WRKFLW_TEST_POSTGRES_DSN`.
- Not BREAKING: additive, in a new module. Persisted formats: no change (the schema is the goose set; no column, migration, snapshot or journal shape changes). Default security posture: unchanged. **Consumer go.mod: a new opt-in module.** Importing it adds `gorm.io/gorm` and its transitive requirements to that consumer's graph only. A consumer who does not import it sees no change. The `examples` module gains `gorm.io/gorm`, `gorm.io/driver/sqlite` and `gorm.io/driver/postgres`, and `gorm.io/driver/sqlite` pulls in cgo `mattn/go-sqlite3` for building examples.

**Out of scope**
- The lent-transaction seam itself (`persistence.Tx`, `WithTx`, `Continuation`, savepoint per joined step): `add-lent-transaction-seam` (kartaladev/wrkflw#109).
- `persistence.Conn`, `sql.FromDB`, `sql.FromTx`, `sql.WithErrorClassifier`, the `SQLState()` Postgres classifier, `persistence.NewMigrator(Conn)`, the module skeleton, `go.work` and lockstep tags: `split-persistence-modules` (kartaladev/wrkflw#39, #30 decided).
- Recovery of a committed step whose `Resume` never ran (process death between GORM's commit and `Resume`): `persist-pending-commands` (kartaladev/wrkflw#110). Per #31 call 5, the adapter adds no durability of its own.
- A GORM peer store, GORM models for engine tables, `AutoMigrate`: rejected on #31 call 1 and call 3.
- `PublishDefinition` under a lent transaction: `join-ambient-publish` (kartaladev/wrkflw#151).
- Tagging the module's first release: `cut-v0-release` (kartaladev/wrkflw#48).

**Decisions not yet made** (design Open Questions; the specs avoid depending on them): the exported sentinels for the three decided refusals; `PrepareStmt` mode, where the transaction's pool is not a `*sql.Tx`; `Transaction` called on a `*gorm.DB` that is already inside a transaction; how `Transaction` reports a `Resume` failure after a successful commit; whether the SQLite classifier also matches `database table is locked` for parity with the built-in classifier; and what `Resume` does for a coupled step whose writes a nested GORM savepoint rolled back (owned by `add-lent-transaction-seam`).

**Coordination**
- Size: M
- Depends on: add-lent-transaction-seam, split-persistence-modules
- Blocks: cut-v0-release
- Readiness: human

## Capabilities

### New Capabilities
- `gorm-adapter`: how a consumer's `*gorm.DB` becomes an engine connection or a lent transaction. Covers dialect resolution and refusal, the `Transaction` wrapper's lend, commit, resume and discard sequence, migrations through the goose set, SQLite error classification under GORM's driver, and the module's dependency boundary.

### Modified Capabilities

## Impact

- New module `persistence/gorm` (`go.mod`, `go.work` entry, `scripts/modules.sh` floor raised by one, CI iterating it). It depends on the module layout that `split-persistence-modules` creates.
- New public API: `gormpersistence.From`, `FromTx`, `Transaction`, and the SQLite classifier, plus the sentinels Open Question 1 names.
- `examples/gorm_wiring` and the `examples` module's `go.mod`. Building examples now needs cgo, for `mattn/go-sqlite3`.
- Guard: a test that the module's non-test import graph names no `gorm.io/driver/*` package, no SQL driver, and no package that requires cgo.
