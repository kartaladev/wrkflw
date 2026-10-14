# Tasks

Every Go task starts with `cc-skills-golang:golang-how-to`, and is written test-first: write the test, watch it go red, then make it pass. Cases that share one call go in one table test (`table-test` skill). `T` below stands for `GOTOOLCHAIN=go1.26.9 go test -race`. Dialect rows need Docker (Postgres via `dbtest.RunTestDatabase`, MySQL via `dbtest.RunTestMySQL`); SQLite rows run without it. Scenario names refer to `specs/transaction-seam/spec.md`.

Groups 1–4 and 6 do not depend on the open questions, except where a task says so. Group 5 waits for Open Questions 2 and 3. Task 1.2 waits for Open Question 4 (names only). Landing at all waits for Open Question 1. Record each answer in design.md before starting the work that waits on it.

## 1. Public surface and measurements

- [ ] 1.1 Red: extend the reach of `runtime/monitor/internal_leak_test.go` with a fixture case: an exported signature whose parameter is an alias of an `internal/` type. Check whether the guard reports it. If it does not, record that in design.md (Decision 3) as the measured reason the alias is forbidden by review rather than by the guard. Verify: `T ./runtime/monitor/ -run Internal` and the recorded outcome.
- [ ] 1.2 Add `persistence.Dialect` (distinct type, `Postgres`, `MySQL`, `SQLite`), `persistence.Tx`, the two `Tx` constructors (names per Open Question 4), `persistence.WithTx`, and `Continuation` with `Resume` and `Discard`. Add the lent-transaction marker and accumulator in `runtime/kernel`. Each new exported signature names no `internal/` type. Test-first: an external-package test (`package persistence_test`) builds a `Tx` from a real `*sql.Tx` on SQLite and a `pgx.Tx` on Postgres and passes it to `WithTx`. Verify: `T ./persistence/ ./runtime/kernel/ ./runtime/monitor/` green, and the leak guard stays green.
- [ ] 1.3 Godoc on `WithTx` shows the lend, call, commit, `Resume` sequence and the rollback, `Discard` sequence. It states the unresumed-`Continuation` window (until `persist-pending-commands`), that a lent context must not be used by concurrent engine calls, and that resolver I/O runs inside the consumer's window. Add an `Example` test that runs on SQLite. Verify: `T ./persistence/ -run Example` and `scripts/check-doc-refs.sh` pass.
- [ ] 1.4 Measure G6 (hypothesis, kartaladev/wrkflw#151) for `Store.Load` before 2.3 relies on joined reads. On MySQL under REPEATABLE READ, a lent transaction reads an instance, another connection commits a newer version, and the lent transaction then calls `ApplyTrigger` on it. Record whether the result is `kernel.ErrConcurrentUpdate`, a stale read, or a lost update. Verify: the test and its recorded outcome in design.md. If it shows a lost update, stop and report it as a defect with that test as its red.

## 2. Store joins a lent transaction under a savepoint

- [ ] 2.1 Red: add the rows of "Under a lent transaction RunInTx joins and neither commits nor rolls back" to the store's `RunInTx` table test, on all three dialects. Verify: `T ./internal/persistence/store/ -run RunInTx` fails today, because RunInTx begins its own transaction.
- [ ] 2.2 Green: add an external (non-owned) handle kind in `internal/database/transaction`. Joining it brackets the unit with a savepoint: pgx uses a nested `Begin`, and `database/sql` issues `SAVEPOINT` / `RELEASE SAVEPOINT` / `ROLLBACK TO SAVEPOINT` with an engine-unique name. It never sets the rollback-only mark. `RunInTx` joins it. Verify: 2.1 green, and the existing "Callback outcome decides the unit" rows still pass unchanged.
- [ ] 2.3 Red, then green: `Store.Load` and the human-task reads on the coupled path read through the lent handle when one is present. The lister and the timer store's `ArmedTimer`/`ListArmed` keep reading through the pool. The test for this is the store-level half of "Create then trigger the same instance in one lent transaction": create, then `Load` and `Commit` on the same lent context. Verify: `T ./internal/persistence/store/...` green on all three dialects.
- [ ] 2.4 Add the store-level rows of "Consumer ignores the engine error and commits" and "A consumer's own open savepoint survives the engine's". Inject the failure through the existing timer-store fault seam. Verify: green on all three dialects.

## 3. One coupled step and the Continuation

- [ ] 3.1 Red: write "A service task is not called inside the consumer's window" with a spy action catalog, and "StartInstance then consumer rollback". Verify: `T ./service/ -run Lent` fails today, because action A is called before the consumer commits.
- [ ] 3.2 Green: under a lent marker, `deliverLoop` stops after the first joined commit. It hands the remaining queue, `armed` jobs, `cancelKeys`, the waiter sync and the step's commands to the `Continuation`. `Resume` performs the captured commands once, activates and deactivates the captured timers, and re-applies follow-up triggers through the CAS-retrying entry point. Each step after that commits in its own engine-owned transaction. Verify: 3.1 green. Also run "Lent then resumed equals unlent", "A failure after the coupled step is recorded as it is outside a lent transaction", "The coupled step's state is durable at the consumer's commit" and "The lent transaction survives every engine outcome" on all three dialects.
- [ ] 3.3 Write "Create then trigger the same instance in one lent transaction" and its negative control. Also write "Another actor moves the instance before Resume", with a spy counter asserting action A ran exactly once. Run the coupled path under `-race`, and assert that the driver issues no concurrent statement on the lent handle. Verify: `T ./service/ ./runtime/ -run Lent` green on all three dialects.
- [ ] 3.4 Write "A timer armed by the coupled step is rehydrated after a restart before Resume". Verify: green on SQLite.
- [ ] 3.5 Add `Resume`'s error behavior for a failing follow-up, per Open Question 5. Verify: a table test over one, two and three accumulated calls with the middle one failing.

## 4. Caching instance store under a lent transaction

- [ ] 4.1 Red: write "Wrapper reads follow the consumer's outcome". Verify: `T ./persistence/ -run Caching` fails on the `cache-rb` row today, because the wrapper `put`s the uncommitted state.
- [ ] 4.2 Green: under a lent handle, `CachingInstanceStore.Create` and `Commit` evict instead of `put`. Verify: 4.1 green, and the existing "Failed unit evicts the optimistic cache entry" and "Negative control, a successful unit stays readable through the wrapper" rows are unchanged.

## 5. Misuse refusals (waits for Open Questions 2 and 3)

- [ ] 5.1 Add ADDED requirements and scenarios for the answered misuse cases to `specs/transaction-seam/spec.md`, each with a negative control, and run `openspec validate add-lent-transaction-seam --strict`. Verify: validation passes.
- [ ] 5.2 Red, then green: one table test per refusal, asserting the exported sentinel with `errors.Is` and that no row was written. Verify: `T ./persistence/ ./service/ -run Lent` green.

## 6. Example on exported API only

- [ ] 6.1 Rewrite `examples/real_db_transaction` to lend a `*sql.Tx` on SQLite. It inserts a consumer row and starts an instance in one transaction, shows that a rollback leaves neither, and shows that a commit followed by `Resume` lets the instance continue. Verify: `grep -n internal examples/real_db_transaction/*.go` prints nothing, and the example runs to completion with `GOTOOLCHAIN=go1.26.9 go run ./examples/real_db_transaction`.

## 7. Integration

- [ ] 7.1 Run the whole suite per module as CONTRIBUTING.md describes (`scripts/modules.sh`), with Docker for dialect rows. Run `scripts/lint.sh` per module, `scripts/check-doc-refs.sh`, and `scripts/check-openspec.sh`. Verify: all exit 0.

## Workflow follow-up

- The PR names `add-lent-transaction-seam`. Its last commit runs `openspec archive add-lent-transaction-seam`.
- Comment on kartaladev/wrkflw#109 and close it. Tell `join-ambient-publish` and `add-gorm-adapter` that their blocker is archived.
