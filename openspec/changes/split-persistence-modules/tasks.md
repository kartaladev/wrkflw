# Tasks

Apply waits for the rulings on design Q1 to Q5 and for `add-lent-transaction-seam`, `remove-casbin` and `unify-http-routes-as-data` to be archived. Groups 3 and 5 assume Q1 (a), and group 8 assumes Q2 (a). Re-measure every baseline figure in design *Context* on the base you start from, in a fresh worktree, before relying on it. Run commands per module as `CONTRIBUTING.md` describes. A bare `./...` covers only the root module.

## 1. MigrationSets re-anchor and the #220 defect (first, before any move)

- [ ] 1.1 Write the failing test for #220 §1 with a **hand-built** four-key schema, not `schemasWith`: a table present only under `"cockroach"` must be reported by `uncrossCheckedTables`. Watch it go red. Verify: `GOTOOLCHAIN=go1.26.9 go test ./internal/persistence/store/ -run TestUncrossCheckedTables`.
- [ ] 1.2 Write the failing test for #220 §2: a `MigrationSets` entry declaring `Dialects: {"postgress"}` must make `LoadSchemas` fail with an error naming the dialect. Watch it go red, which turns the hypothesis into a proven defect, or record it as not reproduced. Verify: `go test ./internal/atrest/ -run LoadSchemas`.
- [ ] 1.3 Apply the remedy chosen in Q3 to `uncrossCheckedTables` **and** `schemasWith`, and/or to `reconcileMigrationSets`. Keep the discriminating row: a covered table under a legitimate dialect is still not reported. Record in the test the input on which the remedy was tested *not* to move the fail-open. Verify: 1.1 and 1.2 go green and `go test ./internal/atrest/ ./internal/persistence/store/` passes.
- [ ] 1.4 Re-anchor `MigrationSets` keys relative to the module root that will exist after the move. Keep the fail-closed reconcile unchanged. Verify: `discover_test.go`'s census counts (re-measure them first; #207 recorded 87/79/79 before `remove-casbin`) and the cross-check pass.

## 2. Severing the fixture and moving the codec

- [ ] 2.1 `internal/dbtest` stops importing the store, and callers apply migrations themselves (Decision 7, Q5). Verify: `go list -deps ./internal/dbtest` names no `internal/persistence/store`, and every `dbtest` caller's tests pass.
- [ ] 2.2 Capture golden journal rows (kind and payload bytes) for all 16 trigger kinds from the **current** store, and add the exhaustiveness test against the kind vocabulary. Verify: instance-store scenarios "Golden journal rows decode and re-encode unchanged" and "Every trigger kind has a golden row" pass before the move.
- [ ] 2.3 Write the unknown-kind test, export its sentinel (design *Findings*), then move the codec to `runtime/kernel` under the Q4 names. Verify: instance-store "An unknown kind is refused" plus 2.2's scenarios pass unchanged after the move, and `go list -deps ./runtime/kernel` gains no third-party module.

## 3. The persistence module

- [ ] 3.1 Create `persistence/go.mod`, requiring root at a pushed-commit pseudo-version with no `replace`. Add it to `go.work` and raise `MIN_MODULES`. Move `internal/persistence/{store,dialect}`, `internal/database{,/transaction}` and `internal/atrest` (renamed per Q4) under `persistence/internal/`, and rewrite the imports (re-measure: 104 files on `eebe55ba`). Verify: `scripts/modules.sh` exits 0 and per-module `go build` and `go vet` pass.
- [ ] 3.2 In the same commit as 3.1, re-anchor `scripts/check-extraction.sh` (`module`, `toolkit_dir`) and rename its CI job and required check from `extraction constraint (internal/database)` to the new path. Verify: the script is green on the new tree, and it goes red when a wrkflw import is planted in the toolkit (ablation).
- [ ] 3.3 Relocate the back-edge test files per Q1. Re-measure that the root module's tests import nothing under `persistence/`. Verify: `go list -test -deps` over root names no `…/persistence` package, and each moved test passes in its new module, with Docker for the Postgres and MySQL rows.
- [ ] 3.4 Point `runtime/monitor/internal_leak_test.go` and `service/vendorfree_test.go` at the new paths, and check the leak guard ranges every new module. Verify: each guard goes red on a planted leak and green without it.

## 4. Connection seam and driver modules

- [ ] 4.1 Write failing tests for `pgx.FromPool` and `sql.FromDB`, covering the dialect vocabulary, nil handles and the wrapped driver, with exported sentinels per design *Findings*. Verify: they fail to compile or go red first. Then add `persistence.Conn`, the exported querier seam (#22 call 10) and the `persistence/pgx` and `persistence/sql` modules until persistence-connection "Each driver path yields a working connection", "A nil handle is refused", "Dialect vocabulary" and "A wrapped driver works when the dialect is given" pass on all four rows, with Docker for Postgres and MySQL.
- [ ] 4.2 Write the Postgres-over-`database/sql` duplicate-create test, watch it go red, then classify Postgres through `SQLState()` and drop the `pgconn` import from the dialect layer. Verify: instance-store "Duplicate and fresh IDs" passes on all 8 rows, and module-dependencies "pgx is absent from the database/sql path" passes.
- [ ] 4.3 Add `WithErrorClassifier`. Verify: persistence-connection "Unrecognised unique-violation error" (both rows) and "Negative control, a known driver needs no classifier" pass.
- [ ] 4.4 Replace the 52 driver-typed signatures with `New...(Conn)` constructors and keep `DurableProvider` as the bundle. Move the pgx-only facades (`NewAdvisoryLockOwnership`, `WithListenNotify`, pool stats, ping) to `persistence/pgx`, and the `*sql.DB` ones to `persistence/sql`. Rename `add-lent-transaction-seam`'s `Tx` constructors to `pgx.FromTx`/`sql.FromTx`. Verify: no exported signature in `persistence` names `*pgxpool.Pool` or `*sql.DB`, and the existing `persistence` suites pass on the new constructors.
- [ ] 4.5 Add advisory-lock ownership over `*sql.Conn` for Postgres, on the MySQL twin's pattern. Verify: persistence-connection "Ownership on each Postgres path" and "Closing an ownership frees its instances" pass (Docker).
- [ ] 4.6 Verify the relay: event-delivery "Postgres drains on notification before the poll interval", "MySQL and SQLite still drain by polling", "Postgres over database/sql drains by polling" and "Negative control, a pgx relay without listening still drains by polling" pass (Docker).
- [ ] 4.7 Update godoc and `CONTRIBUTING.md` for the per-module commands and the new constructors. Verify: `scripts/check-doc-refs.sh` passes and every documented command runs as written.

## 5. Scheduler electors

- [ ] 5.1 Move the Postgres elector to `persistence/pgx` and the MySQL elector to `persistence/sql` behind `scheduler.Elector`, with their tests. Delete `scheduler/backend/{postgres,mysql}`. Verify: the moved elector tests pass (Docker), and `go list -deps ./scheduler/...` names no driver.

## 6. Cache provider modules

- [ ] 6.1 Create the cache provider modules per Q2, and move `cachetest` to its test-only location. Verify: module-dependencies "Remote cache clients stay out of persistence" and "Negative control, a provider carries its client" pass, and each provider's conformance suite passes (Docker).

## 7. HTTP binding modules

- [ ] 7.1 Make `transport/http/gin` and `transport/http/fiber` modules (the Fiber v2 module is created later by `add-fiber-v2-adapter`), and place `transport/http/parity` per Q1. Verify: module-dependencies "A binding carries no other framework" passes, and every adapter suite and the parity suite pass unchanged.

## 8. Root import guard and integration

- [ ] 8.1 Add the root import guard test, which enumerates importable root packages. Verify: module-dependencies "A denied module is absent from every importable root package's build", "The package set is enumerated, not listed by hand" and "Negative control, the adapter module does carry its driver" pass, and planting a pgx import in a temporary root package turns it red.
- [ ] 8.2 Review `scripts/check-module-drift.sh` and `scripts/check-lint-modules.sh` against the new modules, adding a lint step per module. Verify: both scripts pass, and each still goes red on its planted self-test.
- [ ] 8.3 Rewrite `examples/` on the `Conn` constructors (11 files import `persistence`; `examples/real_db_transaction` imports `internal/database`). Verify: `go build ./examples/...` and `go test ./examples/...` pass.
- [ ] 8.4 Full integration check. Verify: CI's build, test (race), lint, tidy-per-module, drift, govulncheck and openspec jobs pass across every module listed by `scripts/modules.sh`.

## Workflow follow-up

- Before the move commit, drain or close the open Dependabot PRs (#104, #105, #228–#231).
- Hand `cut-v0-release` the BREAKING and consumer-go.mod lines from the proposal, and note that the nested pseudo-versions must be rewritten at the first tag.
- After merge, comment on and close kartaladev/wrkflw#39, #207 and #220. Tell `add-gorm-adapter` that its blocker is archived.
