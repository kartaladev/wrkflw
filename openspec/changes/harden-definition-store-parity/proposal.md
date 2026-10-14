# Proposal

## Why

The definition store promises that Postgres, MySQL and SQLite behave identically. Four gaps from the #112 review break that promise or overstate how well it is guarded:
- MySQL treats different definition IDs as the same key.
- The definition body has a storable domain that differs by backend.
- A defensive branch rests on a driver detail that nothing in this repo pins.
- A test fixture calls itself exhaustive while covering under half the node kinds.

The MySQL gap returns another ID's definition with no error. It has to close while there is still no persisted data, because the fix edits the squashed `0001_init.sql`, and that is only possible before the first release tag.

## What Changes

- **BREAKING (MySQL only)** Definition IDs are compared exactly on every backend. The MySQL `wrkflw_definitions.def_id` column moves from the server-default `utf8mb4_0900_ai_ci` to `utf8mb4_0900_bin`, edited in place in `migrations/mysql/0001_init.sql`. Today, on MySQL, `GetDefinition`, `Lookup`, `Version` and `Latest` for `"Payroll-Approve"` return the definition stored under `"payroll-approve"`. A publish under a variant spelling is refused with `ErrDefinitionExists`, or is a silent no-op when the content matches. After this change, on MySQL as on Postgres and SQLite, the lookup returns `ErrDefinitionNotFound` and the publish stores a second row. Absorbs kartaladev/wrkflw#152.
  - Re-measured on MySQL 8.0.46. The column collation is `utf8mb4_0900_ai_ci`. It folds case, accent, width and NFC/NFD, so all four of the issue's distinctions reproduce, and it does not fold trailing spaces.
  - **Stale premise in #152:** the issue names `utf8mb4_bin` as "the only complete mitigation". Measured, `utf8mb4_bin` is `PAD SPACE`, so it would introduce a new fold, `"tw"` == `"tw "`, which the current column does not have. `utf8mb4_0900_bin` is `NO PAD` and kept all ten measured spellings distinct. See design D1.
  - The `ValidateDefinition` godoc paragraph that says the gate "CANNOT close" this, and points at a note on `PublishDefinition` that does not exist, is rewritten to describe the exact comparison.
- **Spec gap closed: every node kind round-trips through the store.** The `DefinitionStore` godoc and the fixture both claim exhaustive round-trip coverage. Re-measured: the fixture uses 8 of the 17 node constructors (`NewStart`, `NewEnd`, `NewUserTask`, `NewServiceTask`, `NewSubProcess`, `NewCallActivity`, `NewBoundary`, `NewExclusive`). It uses no start, catch or boundary timer and no retry policy. The fixture is widened to all 17 constructors, the three timer markers and a retry policy, so the claim becomes true (the issue left widening or narrowing open; design D3 says why widening). A guard test fails when a node kind is added without fixture coverage. The promise is added to the `definition-store` spec. Absorbs kartaladev/wrkflw#155.
- **Open, not specified here: the body's storable domain.** Re-measured: a NUL inside a string of the definition body is stored by MySQL 8.0.46 (it reads back as `610062`) and refused by Postgres 17 with SQLSTATE `22P05`. SQLite stores the JSON text. Whether the "publishes on one backend, publishes on all" promise extends to the body is the maintainer's decision (design, Open Questions Q1). This change does not specify it. Absorbs kartaladev/wrkflw#154, decision pending.
- **Open, not specified here: `ErrConcurrentPublish` and the pgx `sql.ErrNoRows` dependency.** Re-measured: pgx v5.11.0 defines `pgx.ErrNoRows` as a proxy over `sql.ErrNoRows`. The repo's pgx adapter passes `Scan` errors through untranslated. Whether to translate at the adapter, pin with a test, or both is design Q2. Either way the work is internal, with no spec delta. Absorbs kartaladev/wrkflw#157, decision pending.
  - **Stale premise in #157:** "no test would fail" on a pgx downgrade. The same wrapping is relied on at 14 `sql.ErrNoRows` sites in 7 store files, and Postgres-backed tests already exercise several of them, for example `TestDefinitionStoreGetNotFound/postgres`. A downgrade would fail those. What stays true: the `ErrConcurrentPublish` branch itself has no test, and nothing pins the contract at the adapter where it lives. A consumer also cannot downgrade below the `v5.11.0` the root `go.mod` requires (minimal version selection), so only a maintainer edit to `go.mod` could trigger the hazard.

Impact flags:
- **Persisted format: YES.** It edits the MySQL `0001_init.sql` in place, under the squashed one-file-per-dialect policy (`TestMigrations_OneFilePerDialect`). No release tag exists (`git tag` lists no `v*`). goose does not re-run an applied version, so a MySQL database migrated by an earlier build keeps the folding collation until it is recreated (design D2, Q3). The edit is to a file that `encode-persisted-enums-by-name` also edits (see design, Cross-change coupling).
- **Default security posture: tightened.** It closes cross-ID content confusion on MySQL. Nothing is loosened.
- **Consumer `go.mod`: unchanged.**
- **Supported MySQL floor:** `utf8mb4_0900_bin` exists from MySQL 8.0.17. The docs say "MySQL 8.0+", so the floor rises to 8.0.17 (design Q4).

Out of scope:
- The same `utf8mb4_0900_ai_ci` folding on the other MySQL identifier columns (`instance_id`, `task_id`, `message_id`, `dedup_key` and the rest). `instance_id = ?` appears in 15 store queries and orders the lister keyset. This is a hypothesis, not measured here, and no change owns it. It should be filed as a new intake issue. The other `def_id` columns (`wrkflw_instances`, `wrkflw_timers`, `wrkflw_call_links.parent_def_id`) are never compared in a query, so their collation is not observable today.
- Identical in-memory re-registration: `make-definition-registration-idempotent`.
- Publishing inside a caller's transaction: `join-ambient-publish` (kartaladev/wrkflw#151).
- Enum columns in `0001_init.sql`: `encode-persisted-enums-by-name` (kartaladev/wrkflw#44).
- Refusing unrecognized trigger kinds on decode: `refuse-unrecognized-wire-values`.
- Moving the migration files into a separate module: `split-persistence-modules`.

Decisions this change cannot be fully specified without:
- Q1 (#154): whether body parity is enforced or documented as a named limit. Unresolved. No spec is written for it.
- Q2 (#157): whether to translate at the adapter or pin with a test. Unresolved. No spec delta either way.
- Q3 (#152): the migration story for pre-release MySQL databases. Recommended: in-place edit, following the stance recorded on kartaladev/wrkflw#44 that there is no persisted data before the first tag. Needs confirmation, because #152's own thread leaves it open. The spec describes a store migrated by this version, so it does not depend on Q3.
- Q4: raising the MySQL floor to 8.0.17. Recommended, and needs confirmation.

**Coordination**
- Size: M
- Depends on: establish-baseline-specs
- Blocks: cut-v0-release, `encode-persisted-enums-by-name`
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `definition-store`:
  - "Latest resolves to the highest version on every registry" loses its exception for IDs that differ only by case, accent, width or normalisation.
  - New requirement: definition IDs are compared exactly on every backend.
  - New requirement: a published definition reads back unchanged on every backend, for every node kind, timer marker and retry policy.

## Impact

- `internal/persistence/store/migrations/mysql/0001_init.sql`: the `def_id` column of `wrkflw_definitions` gains `COLLATE utf8mb4_0900_bin`.
- `internal/persistence/store/definitions_conformance_test.go`: the fixture is widened, a coverage guard is added, and new exact-ID conformance rows run on all three dialects.
- Godoc:
  - `runtime/kernel/mem_definition_registry.go`: the `ValidateDefinition` collation paragraph and the body-scope paragraph, the latter depending on Q1.
  - `internal/persistence/store/definitions.go`: the `DefinitionStore` exhaustive-coverage claim and the `ErrConcurrentPublish` mechanism note, the latter depending on Q2.
- `internal/database/adapter_pgx.go` and its tests, depending on Q2.
- `runtime/README.md`, `examples/mysql_wiring`, `examples/chaining_wiring`: the MySQL version floor (Q4).
- Consumers on MySQL that relied, knowingly or not, on folded lookups get `ErrDefinitionNotFound` for a variant spelling.
