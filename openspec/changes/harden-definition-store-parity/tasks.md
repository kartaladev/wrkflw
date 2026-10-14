# Tasks

Every Go task starts with `cc-skills-golang:golang-how-to`. Cases that share one call go in one table test (`table-test` skill). `T` below stands for `GOTOOLCHAIN=go1.26.9 go test -race`. Dialect rows need Docker (Postgres via `dbtest.RunTestDatabase`, MySQL via `dbtest.RunTestMySQL`); SQLite rows run without it.

Groups 1–3 do not depend on the open questions. Group 4 waits for Q1 and group 5 for Q2 (design, Open Questions). Do not start either until the maintainer's answer is recorded in design.md.

## 1. Exact definition IDs on every backend (#152)

- [ ] 1.1 Red: add a store conformance table test for every row of "A variant spelling publishes as its own definition", "IDs at the length limit that differ only in their last rune are distinct" and "Concurrent publishes of variant spellings do not contend for one key". Add the durable-store rows of "A variant spelling of a stored ID is a miss on every registry". Use unique IDs per row. Verify: `T ./internal/persistence/store/ -run '<new tests>'` fails only on the mysql rows, which return `ErrDefinitionExists`, nil-without-a-row, or the other spelling's definition. The trailing-space and leading-space rows pass on mysql (it is NO PAD today), and every postgres and sqlite row passes.
- [ ] 1.2 Add the negative-control rows of "The exact spelling is still the same key", and the in-memory rows of "A variant spelling of a stored ID is a miss on every registry" (characterization, design D5). Verify: they pass before and after 1.3.
- [ ] 1.3 Green: in `internal/persistence/store/migrations/mysql/0001_init.sql`, declare `def_id VARCHAR(255) COLLATE utf8mb4_0900_bin NOT NULL` on `wrkflw_definitions` with no `CHARACTER SET` clause (design D1). Verify: `T ./internal/persistence/store/...` green, including every 1.1 and 1.2 row, `TestMigrationParity_LogicalSchemaConverges`, `TestMigrations_OneFilePerDialect` and the `internal/atrest` tests (`T ./internal/atrest/...`).
- [ ] 1.4 Add the rows of "Exact comparison survives reopening the store" (close, re-run `Migrate`, reopen over the same database). Verify: green on all three dialects.
- [ ] 1.5 Re-run the existing "Latest resolves to the highest version on every registry" scenarios (`TestDefinitionStoreLookupByQualifier`, `TestMemDefinitionRegistryLatestIsHighestVersion`, `TestMapDefinitionRegistryLatestResolvesHighestVersion`, the lookup-miss tests) against the modified requirement. Verify: green unchanged.
- [ ] 1.6 Document:
  - Rewrite the `ValidateDefinition` collation paragraph in `runtime/kernel/mem_definition_registry.go`. It currently says the gate "CANNOT close" the fold and points at a `PublishDefinition` note that does not exist. The new text states that IDs compare exactly on every backend because the MySQL column is binary, and cites `openspec/specs/definition-store`.
  - Raise the MySQL floor to 8.0.17 in `runtime/README.md`, `examples/mysql_wiring/main.go` and `examples/chaining_wiring/main.go` (Q4).
  - Add the pre-release repair statement from the Migration Plan to the MySQL section of `runtime/README.md`.

  Verify: `scripts/check-doc-refs.sh` and `scripts/lint.sh` pass.

## 2. Round-trip coverage made true (#155)

- [ ] 2.1 Red: add the guard for "A node kind with no round-trip coverage is detected". It scans `NodeKind` ordinals as `definition/kinds/nodetype_guard_test.go` does, and fails when a registered kind is carried by no round-trip definition. Verify: `T ./internal/persistence/store/ -run '<guard>'` fails naming the 9 uncovered kinds: `intermediateCatchEvent`, `intermediateThrowEvent`, `compensationThrowEvent`, `receiveTask`, `sendTask`, `businessRuleTask`, `parallelGateway`, `inclusiveGateway`, `eventBasedGateway`.
- [ ] 2.2 Widen the round-trip fixture to all 17 node kinds, the start, catch and boundary timers, and a service task retry policy (design D3). It stays one valid definition if the authoring gate allows that, and becomes a small family otherwise. First check whether `definition/testdata/golden_definition.json` passes `ValidateDefinition` and can serve as a member. Verify: the 2.1 guard is green, and every fixture member passes `kernel.ValidateDefinition`.
- [ ] 2.3 Run every row of "Every node kind and marker round-trips through the store" and "The scoped-action catalog is the only part not read back" on all three dialects, through `GetDefinition`, `Version` and `Latest`. Verify: green. If any field fails to round-trip, stop. Write a failing test that isolates it, report it as a defect, and fix it test-first in its own task. Do not weaken the fixture to pass.
- [ ] 2.4 Confirm that the existing "Republishing identical content is a no-op on every backend" rows (`TestDefinitionStorePublishImmutable`) pass with the widened fixture. Verify: green on all three dialects.
- [ ] 2.5 Document: replace the "validates this exhaustively" sentence on `DefinitionStore` and the fixture's header with a statement of what is covered and that the guard enforces it, citing `openspec/specs/definition-store`. Verify: `scripts/check-doc-refs.sh` and `scripts/lint.sh` pass.

## 3. Integration for groups 1–2

- [ ] 3.1 Run the full suite for every module as CONTRIBUTING.md describes, with Docker. Verify: all modules green on go1.26.9, and `openspec validate harden-definition-store-parity --strict` and `scripts/check-openspec.sh` pass.

## 4. Body storable domain (#154) — waits for Q1

- [ ] 4.1 Record the maintainer's Q1 answer in design.md, then add the matching requirement and scenarios to `specs/definition-store/spec.md` or `definition-validation`. Every backend gets a row, including a NUL in a node name, a flow condition and a nested subprocess, plus a negative control with an escaped backslash followed by `u0000`. Verify: `openspec validate harden-definition-store-parity --strict` passes.
- [ ] 4.2 Red, then green, for the 4.1 scenarios, following the chosen option. Verify: `T ./runtime/kernel/... ./internal/persistence/store/...` red before and green after. Then update the `ValidateDefinition` "Scope" paragraph to match.

## 5. `sql.ErrNoRows` contract at the adapter (#157) — waits for Q2

- [ ] 5.1 Record the maintainer's Q2 answer in design.md. For (b) or (c), add a table test in `internal/database` that asserts a no-row `QueryRow(...).Scan` matches `sql.ErrNoRows` on the pgx pool, the pgx transaction and the `database/sql` adapters. Verify: `T ./internal/database/...` passes on the current pgx (it pins today's behavior). Show it can fail by temporarily stubbing the pgx error to a non-wrapping one, then revert the stub.
- [ ] 5.2 For (a) or (c), make `pgxRow.Scan` return an error matching `sql.ErrNoRows` whenever pgx reports no rows. Then update the `ErrConcurrentPublish` godoc to say the branch rests on the adapter's contract. Verify: `T ./internal/database/... ./internal/persistence/store/...` green, and `scripts/check-doc-refs.sh` passes.

## Workflow follow-up

- The PR names this change. Its last commit runs `openspec archive harden-definition-store-parity`.
- After merge, comment on and close kartaladev/wrkflw#152, #154, #155 and #157 with the change name.
- File a new intake issue for the hypothesis that the other MySQL identifier columns fold too (design, Finding).
