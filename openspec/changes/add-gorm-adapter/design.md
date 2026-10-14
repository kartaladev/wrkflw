# Design

## Context

See proposal.md, Why. kartaladev/wrkflw#31 (decided) fixed the shape, kartaladev/wrkflw#30 (decided) fixed the module, and kartaladev/wrkflw#22 (decided) fixed the seam. None of them is reopened here. Before relying on any premise from kartaladev/wrkflw#42 or #31, it was measured: GORM's source at v1.31.2 (the version #31 cites, in the module cache), its drivers `gorm.io/driver/{postgres,mysql,sqlite}` at v1.6.0, and `main` at `eebe55ba`.

- `db.Transaction(fc)` commits after `fc` returns nil and offers no post-commit hook (`finisher_api.go:637-676`). When `db.Statement.ConnPool` is already a `TxCommitter`, it opens a savepoint `sp<hash>` and **does not commit** (`:640-655`). **Holds**, and this is the reason for Open Question 3.
- `db.DB()` (`gorm.go:426-445`) on a transaction-bound `*gorm.DB` does **not** fail. It reaches into the `*sql.Tx` with reflection and returns the parent `*sql.DB`. **Holds, and matters more than #42 says**: without the refusal #31 decided, `From` on a transaction-bound handle would silently build an engine that writes outside the consumer's transaction. The spec pins the refusal for this reason.
- Inside `Begin` and `Transaction`, `tx.Statement.ConnPool` is the `*sql.Tx` that `BeginTx` returns (`finisher_api.go:700-707`). **Holds only with `PrepareStmt` off.** With `PrepareStmt: true`, `PreparedStmtDB.BeginTx` wraps it in a `*gorm.PreparedStmtTX` (`prepare_stmt.go:88-107`), whose embedded `Tx` is the `*sql.Tx`. That case is Open Question 2.
- `Dialector.Name()` returns `postgres`, `mysql` (`DefaultDriverName`) and `sqlite` for the three GORM drivers, and `sqlserver` for the SQL Server driver. **Holds.**
- `gorm.io/driver/sqlite` v1.6.0 requires `github.com/mattn/go-sqlite3` v1.14.22 (cgo). That driver's `Error.Error()` is SQLite's own `sqlite3_errmsg` text, such as `UNIQUE constraint failed: t.c` or `database is locked`. **Holds.**
- The built-in SQLite classifier on main (`internal/persistence/dialect/sqlite.go:106-123`) matches only the `modernc` error type. It treats `SQLITE_CONSTRAINT_UNIQUE` and `SQLITE_CONSTRAINT_PRIMARYKEY` as unique violations, and `SQLITE_BUSY` **and `SQLITE_LOCKED`** as retryable. **Holds.** A mattn error therefore classifies as neither today. SQLite reports a PRIMARY KEY collision with the same `UNIQUE constraint failed` text, so the text match covers both codes. `SQLITE_LOCKED` is Open Question 5.
- The store maps a retryable conflict to `kernel.ErrConcurrentUpdate` (`internal/persistence/store/store_core.go:339-344`), and duplicate creation to `ErrInstanceExists` (instance-store spec). **Holds.**
- None of `persistence.Conn`, `sql.FromDB`, `sql.FromTx`, `sql.WithErrorClassifier`, `persistence.NewMigrator(Conn)`, `persistence.Tx`, `persistence.WithTx` or `Continuation` exists on main. Today `persistence/migrator.go` has only per-dialect migrator constructors. **Holds**, so this change cannot start before both dependencies land.
- `persistence.ErrNilDependency` exists on main (`persistence/migrator.go`), documented for a nil connection or pool. **Holds**, and it is reused for a nil `*gorm.DB`.
- The workspace has two modules today (`go.work`: `.`, `./examples`), and `scripts/modules.sh` enforces that the `go.work` set agrees with the `go.mod` files on disk. **Holds.** A new module needs a `go.work` entry and a raised `MIN_MODULES`.

## Goals / Non-Goals

**Goals:** a GORM consumer reaches every behavior in `specs/gorm-adapter/spec.md` on SQLite, Postgres and MySQL with exported API only, and importing the adapter adds no SQL driver and no cgo to their graph.

**Non-Goals:** routing engine statements through GORM's callback chain, logger or plugins. Engine statements run on the underlying `*sql.DB` or `*sql.Tx`, so GORM's `Logger`, hooks and `TranslateError` never see them; the godoc says so. Also out: SQL Server, a GORM store, GORM models for engine tables, and durability for a `Resume` that never runs (`persist-pending-commands`).

## Decisions

1. **Interop adapter on the `sql` path, not a store** (decided, #31 call 1). `From` and `FromTx` delegate to `sql.FromDB` and `sql.FromTx`. Rejected: a peer store on an ORM for a one-JSONB-blob-per-row schema, which would be a fourth copy of the store with no consumer.

2. **`Transaction` owns lend, commit, resume and discard** (decided, #31 call 2). It calls `db.WithContext(ctx).Transaction(...)`. Inside, it builds `FromTx(tx)`, calls `persistence.WithTx(ctx, t)`, and runs `fc(lentCtx, tx)`. If `db.Transaction` returns nil, it calls `cont.Resume(ctx)`. Otherwise it calls `cont.Discard()` and returns the error unchanged. A deferred `recover` discards and re-panics. Rejected: documenting the sequence and leaving it to the consumer, because #22 call 8 accepted the forgettable `Resume` and this population is the one most likely to forget it.

3. **Dialect from `Dialector.Name()`, exact match on three names** (decided, #31 call 1 and #42). #22 call 3 rejects inference from `db.Driver()` because wrapped drivers break it. The GORM dialector name is the consumer's explicit choice of dialect, so this is not that inference. Any other name, including case and whitespace variants, is refused (spec Examples), in line with the project's rule that an out-of-vocabulary value is refused and never defaulted.

4. **`From` refuses a transaction-bound `*gorm.DB`** (decided, #42). The test is whether `db.Statement.ConnPool` is a `gorm.TxCommitter`, which is the same test GORM uses to decide on nesting. The measurement above shows that `db.DB()` would otherwise escape the transaction silently.

5. **Migrations through the goose set only** (decided, #31 call 3). `persistence.NewMigrator(From(db))`. The adapter never calls `AutoMigrate` on an engine table.

6. **SQLite classifier by message text, no cgo** (decided, #31 call 4). It uses `strings.Contains` on `err.Error()`, so it sees through `%w` wrapping, and it matches exact case, since SQLite's messages are fixed English text. It is installed by `From` and `FromTx` for the `sqlite` dialect through `sql.WithErrorClassifier`, and exported, so a consumer who builds its own `sql.FromDB` can pass it. Text matching also covers the `modernc`-based GORM SQLite drivers, because the message comes from SQLite itself. Rejected: importing `mattn/go-sqlite3` for its error type, which would put cgo into every adapter consumer's graph (#31 call 6).

7. **Own module, `gorm.io/gorm` alone** (decided, #31 call 6 and #30). Test-only requirements (`gorm.io/driver/*`, `mattn/go-sqlite3`, `testcontainers`) stay in the module's `go.mod`, where pruning keeps them out of consumers' builds (#30 call 5). A guard test checks the non-test closure, with `gorm.io/gorm` as its positive control, and the module builds with `CGO_ENABLED=0`.

8. **Reuse `persistence.ErrNilDependency` for a nil `*gorm.DB`.** It is an existing sentinel whose documented meaning is exactly "the connection passed is nil". Rejected: a new adapter-local nil sentinel, which would duplicate it.

## Cross-change coupling

| Change | Coupling | Owner |
|---|---|---|
| `add-lent-transaction-seam` | `Transaction` calls `persistence.WithTx`, `Continuation.Resume` and `Discard`. The engine's savepoint names must nest inside GORM's `sp<hash>` savepoints (seam Decision 2 already names this). The seam's misuse refusals (its Open Question 2) apply unchanged under the wrapper. Open Question 6 belongs to it. Landing order: the seam first. | `add-lent-transaction-seam` |
| `split-persistence-modules` | Provides `persistence.Conn`, `persistence/sql` (`FromDB`, `FromTx`, `WithErrorClassifier`), `persistence.NewMigrator(Conn)`, the `SQLState()` Postgres classifier GORM's pgx/stdlib driver needs, `go.work` with module CI, and the `persistence/gorm` slot in the topology (#30). Shared files: `go.work`, `scripts/modules.sh` (`MIN_MODULES`), `.github/workflows/ci.yml`. Landing order: the split first. | `split-persistence-modules` |
| `persist-pending-commands` | A process death between GORM's commit and `Resume` leaves the coupled step committed and unperformed. The adapter adds nothing (#31 call 5), and the spec pins that the state is exactly the seam's. | `persist-pending-commands` |
| `cut-v0-release` | kartaladev/wrkflw#42 is a release blocker; the lockstep release script must tag `persistence/gorm/vX.Y.Z`. | `cut-v0-release` |

## Risks / Trade-offs

- [Building `examples` now needs cgo, for `mattn/go-sqlite3` through `gorm.io/driver/sqlite`] → CI runners have a C toolchain. The adapter module itself builds with `CGO_ENABLED=0` (spec), and only its tests and the example need cgo. If a cgo-free example is wanted later, swapping to a modernc-based GORM SQLite driver needs no adapter change (Decision 6).
- [A long consumer transaction holds SQLite's write lock while `fc` runs] → this is inherent to the lent contract (seam design, Risks). It is documented on `Transaction`.
- [`Resume` runs on the request goroutine after commit, so action latency lands on the caller] → this is the decided trade (#31 call 2). It is documented, and consumers who want otherwise use `FromTx` and run `Resume` themselves.
- [The text classifier is coupled to SQLite's English message strings] → SQLite has kept these strings stable for many years. The conformance test runs them against real mattn errors, so a change in wording turns the build red.

## Migration Plan

Additive, in a new module. No persisted-format change and no migration. Consumers opt in by requiring `github.com/kartaladev/wrkflw/persistence/gorm`. Until the first tag (`cut-v0-release`), nested requires use a pseudo-version, as #30 call 4 describes. Rollback is deleting the module.

## Open Questions

Neither #31 nor #42 decides these. The specs avoid depending on them, and Readiness is human until they are answered.

1. **Exported sentinels for the three decided refusals.** A `From` on a transaction-bound handle, an unsupported dialector name, and a `FromTx` on a handle with no transaction are each specified as "an error naming X", because no sentinel exists to name. That wording is recorded here as a finding: export a sentinel for each. **Recommendation:** reuse the unsupported-dialect sentinel `split-persistence-modules` defines for `sql.FromDB`, if it defines one, and otherwise add `gormpersistence.ErrUnsupportedDialect`. Add `gormpersistence.ErrInTransaction` (its message names `FromTx`) and `gormpersistence.ErrNoTransaction`. Answering this rewrites three spec rows to `errors.Is`, and adds no requirement.
2. **`PrepareStmt` mode.** With `gorm.Config{PrepareStmt: true}`, the transaction's pool is a `*gorm.PreparedStmtTX` wrapping the `*sql.Tx`. Read literally, #42's rule ("not a `*sql.Tx` is an error") makes `FromTx`, and so `Transaction`, unusable for these consumers, and #31's measurement did not cover the mode. (a) Refuse it and document. (b) Unwrap `PreparedStmtTX.Tx` when it is a `*sql.Tx`. **Recommendation: (b)**, since `PrepareStmt` is a common production setting and the unwrapped handle is the same transaction. This amends a decided call, so it needs the maintainer. It adds one spec row under either answer.
3. **`Transaction` on a `*gorm.DB` already inside a transaction.** GORM then opens a savepoint and does not commit, so calling `Resume` when `db.Transaction` returns would run the engine's deferred work before the real commit, which breaks #22 call 7. (a) Refuse before calling `fc`, with the sentinel from question 1 (`ErrInTransaction`). (b) Join the outer transaction and hand the `Continuation` back to the caller. **Recommendation: (a)**, because (b) gives the wrapper a second, forgettable contract. It adds one requirement with a negative control.
4. **Reporting a `Resume` failure after a successful commit.** The consumer's rows are committed, but `Transaction` returns non-nil. (a) Return `Resume`'s error as it is. (b) Wrap it so `errors.Is(err, gormpersistence.ErrResumeFailed)` tells the caller that the commit happened. **Recommendation: (b)**, so a caller does not retry a committed unit. It adds one scenario.
5. **`database table is locked` (`SQLITE_LOCKED`).** The built-in classifier treats it as retryable, and #31 named only the two codes it needed. Parity is a contract, so without it a shared-cache GORM SQLite consumer would see raw driver text where the built-in store yields `kernel.ErrConcurrentUpdate`. **Recommendation:** match it too, which adds one row to "The classifier's vocabulary".
6. **`Resume` for a coupled step a consumer savepoint rolled back** (owned by `add-lent-transaction-seam`). A GORM nested transaction that rolls back after an engine call undoes the step's writes, but the `Continuation` still holds its deferred work. The spec pins only that no instance row persists. The seam's spec has the same gap for its "A consumer's own open savepoint survives the engine's" scenario. **Recommendation:** the seam decides it, and should make `Resume` perform nothing for a step whose committed version it cannot find. This change then adds the `Resume` row to "Engine call inside a GORM nested transaction".
