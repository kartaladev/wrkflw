# Tasks

This change starts only after `add-lent-transaction-seam` and `split-persistence-modules` are archived. Every Go task starts with `cc-skills-golang:golang-how-to` and is written test-first: write the test, watch it go red, then make it pass. Cases that share one call go in one table test (`table-test` skill). Databases come from the `use-testcontainers` helpers that `split-persistence-modules` exports (`dbtest.RunTestDatabase` for Postgres, `dbtest.RunTestMySQL` for MySQL). `T` below stands for `GOTOOLCHAIN=go1.26.9 go test -race`, run from `persistence/gorm`. Dialect rows need Docker, while SQLite rows (through `gorm.io/driver/sqlite`, cgo) do not. Scenario names refer to `specs/gorm-adapter/spec.md`.

Before starting the work that waits on an Open Question, record its answer in design.md, and add the spec rows the answer calls for. Run `openspec validate add-gorm-adapter --strict`. Task 2.2 waits for OQ1 (names only). Task 3.3 waits for OQ2. Group 5 waits for OQ3 and OQ4. Task 6.2 waits for OQ5, and task 5.4 waits for OQ6.

## 1. Module skeleton and dependency boundary

- [ ] 1.1 Create `persistence/gorm/go.mod` (module `github.com/kartaladev/wrkflw/persistence/gorm`, package `gormpersistence`). It requires `gorm.io/gorm` and wrkflw's `persistence` and `persistence/sql` modules at the pseudo-version #30 call 4 prescribes. Add it to `go.work`, and raise `MIN_MODULES` in `scripts/modules.sh` by one. Verify: `scripts/modules.sh` prints the new pattern and exits 0, and `GOTOOLCHAIN=go1.26.9 go build ./persistence/gorm/...` succeeds.
- [ ] 1.2 Red, then green: write the guard test for "Forbidden packages in the adapter's build graph" and its "Negative control, the guard sees GORM itself". It runs `go list -deps` (non-test) over the module and also builds it with `CGO_ENABLED=0`. Then extend the root's existing non-test import-graph guard (the one from `split-persistence-modules`) with the rows of "A consumer that does not import the adapter carries no GORM". Red proof: temporarily import `gorm.io/driver/sqlite` from a non-test file and watch the guard fail with the package named, then revert. Verify: `T ./... -run Deps` is green and the recorded red run is in the PR body.

## 2. From: dialect resolution and the transaction refusal

- [ ] 2.1 Red: write "Dialector names", "A nil *gorm.DB is refused" and "Transaction-bound handles are refused and plain ones accepted" as table tests. Name rows use a stub dialector; handle rows use real SQLite, plus Postgres and MySQL for the `db.Begin()` rows. Verify: `T ./... -run TestFrom` fails because `From` does not exist.
- [ ] 2.2 Green: implement `From` per design Decisions 3, 4 and 8, on `sql.FromDB`. Its sentinels are per OQ1. Godoc states that engine statements bypass GORM's logger and hooks. Verify: 2.1 is green, and the refusal rows assert `errors.Is` on the OQ1 sentinels once those exist.
- [ ] 2.3 Write "An engine over From round-trips an instance on each dialect" with GORM's own drivers. Verify: `T ./... -run RoundTrip` is green on all three dialects.

## 3. FromTx

- [ ] 3.1 Red: write "FromTx on handles in and out of a transaction" and "FromTx refuses an unsupported dialector as From does". Verify: `T ./... -run TestFromTx` fails because `FromTx` does not exist.
- [ ] 3.2 Green: implement `FromTx` on `sql.FromTx`, sharing dialect resolution with `From`. Then write "A hand-managed lend through FromTx follows the consumer's outcome" and "FromTx accepts the transaction GORM hands a nested callback". Verify: `T ./... -run 'FromTx|HandManaged|Nested'` is green on all three dialects.
- [ ] 3.3 Add `PrepareStmt` rows to the 3.1 table and implement the answer to OQ2. Verify: the table is green, with `PrepareStmt: true` on SQLite and Postgres.

## 4. SQLite classifier

- [ ] 4.1 Red: write "The classifier's vocabulary" and "A nil error is classified as neither" against the exported classifier. Verify: `T ./... -run Classif` fails because the classifier does not exist.
- [ ] 4.2 Green: implement the text classifier (design Decision 6), and have `From` and `FromTx` install it for `sqlite` through `sql.WithErrorClassifier`. Verify: 4.1 is green.
- [ ] 4.3 Red, then green: write "Engine sentinels under GORM's SQLite driver" and "A real busy database maps to the retryable conflict" against real mattn errors. For red, run both with the classifier left uninstalled and see raw driver text. Then write "Postgres and MySQL under GORM keep their own classification". Verify: `T ./... -run 'Sentinel|Busy|KeepOwn'` is green on all three dialects.

## 5. Transaction wrapper

- [ ] 5.1 Red: write "Callback outcome decides both rows", "The engine's own failure inside the callback rolls back the consumer's row" and "A panic in the callback rolls back both and re-panics". Verify: `T ./... -run TestTransaction` fails because `Transaction` does not exist.
- [ ] 5.2 Green: implement `Transaction` per design Decision 2 (`WithTx`, then `Resume` after GORM's commit, `Discard` on rollback, discard and re-panic on panic). Then write "Deferred work follows the commit and only the commit", "Several engine calls in one Transaction resume in call order" and "Transaction returns every connection it borrowed". Verify: `T ./... -run 'Transaction|Deferred|InCallOrder|Borrowed'` is green on all three dialects.
- [ ] 5.3 Write "Concurrent Transactions resume only their own work" under `-race`, and "A process that stops between GORM's commit and the deferred work leaves the step committed and unperformed". The second uses a subprocess that exits from a test-only hook placed after GORM's commit. Verify: both are green, the first on Postgres and MySQL, the second on SQLite.
- [ ] 5.4 Write "Engine call inside a GORM nested transaction". Once OQ6 is answered on `add-lent-transaction-seam`, add the `Resume` row for a nested rollback. Verify: `T ./... -run NestedTransaction` is green on all three dialects.
- [ ] 5.5 Add the requirements and scenarios for OQ3 (an already-transaction-bound `db`, with a negative control) and OQ4 (`Resume` failure after commit), then make them pass. Verify: `openspec validate add-gorm-adapter --strict` passes and `T ./... -run 'AlreadyInTx|ResumeFailed'` is green.
- [ ] 5.6 Godoc: the package doc and `Transaction`'s doc show the wrapper first. Then come `From` and `FromTx` with `WithTx`, `Resume` and `Discard` for hand-managed transactions, the unresumed-`Continuation` window (until `persist-pending-commands`), and SQLite's write lock held through `fc`. Add an `Example` test for `Transaction` on SQLite. Verify: `T ./... -run Example` passes, and `scripts/check-doc-refs.sh` exits 0.

## 6. Migrations

- [ ] 6.1 Write "Migrator over From matches the plain migrator" and "The adapter creates no table by itself". Verify: `T ./... -run Migrat` is green on all three dialects.
- [ ] 6.2 Add the `database table is locked` row to "The classifier's vocabulary" and implement it, if OQ5 says to. Verify: the classifier table is green.

## 7. Example

- [ ] 7.1 Add `examples/gorm_wiring` (and the `examples/go.mod` requirements). It creates a consumer table and an engine instance in one `gormpersistence.Transaction`. A returned error leaves neither row, and after a commit the instance continues to completion. It runs on SQLite by default, and on Postgres when `WRKFLW_TEST_POSTGRES_DSN` is set. Migrations go through `persistence.NewMigrator(From(db))`, with no `AutoMigrate` on engine tables. Verify: `GOTOOLCHAIN=go1.26.9 go run ./examples/gorm_wiring` exits 0, `grep -n 'internal\|AutoMigrate' examples/gorm_wiring/*.go` finds no import of an `internal/` package and no `AutoMigrate` of an engine model, and the Postgres variant exits 0 with the DSN set.

## 8. Integration

- [ ] 8.1 Run every module's tests as CONTRIBUTING.md describes (`scripts/modules.sh`), with Docker for the dialect rows. Also run `scripts/lint.sh` per module, `scripts/check-lint-modules.sh`, `scripts/check-module-drift.sh`, `scripts/check-doc-refs.sh` and `scripts/check-openspec.sh`. Confirm CI iterates the new module for test, lint and vulncheck. Verify: everything exits 0, and the PR's CI shows the `persistence/gorm` module in each iterated job.

## Workflow follow-up

- The PR names `add-gorm-adapter`. Its last commit runs `openspec archive add-gorm-adapter --yes`.
- Tell `cut-v0-release` that the release script must tag `persistence/gorm/vX.Y.Z`.
