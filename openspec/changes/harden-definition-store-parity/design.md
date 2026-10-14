# Design

## Context

See proposal.md, Why. Current state, re-measured on this branch (HEAD `9c6dd2c1`, equal to `main`).

**MySQL `def_id` (kartaladev/wrkflw#152).** Measured in a throwaway `mysql:8.0` container (8.0.46, the image `internal/dbtest/mysql.go` uses). The table was created with the exact DDL from `migrations/mysql/0001_init.sql`:
- The server collation is `utf8mb4_0900_ai_ci`, and the `def_id` column inherits it (`information_schema.COLUMNS`).
- `WHERE def_id = ?` matched the stored row for each of four variants:

  | Stored | Variant |
  |---|---|
  | `payroll-approve` | `Payroll-Approve` |
  | `resume` | `résumé` |
  | `A` | fullwidth `Ａ` |
  | NFC `é` | NFD `e`+U+0301 |

- `tw ` did not match `tw` (`NO PAD`).
- `INSERT ... ('Payroll-Approve',1,...)` failed with `ERROR 1062 Duplicate entry 'Payroll-Approve-1'`.

So both halves of the publish path fold at the SQL level: the insert is declined as a duplicate, and the read-back finds the other spelling's row. Through `PublishDefinition`, that gives `ErrDefinitionExists` for different content and nil for equal content, with no row written for the variant. The Go-level outcome is read off `definitions.go`, not run; task 1.1 reproduces it.

- After `ALTER ... COLLATE utf8mb4_bin`, the case, accent, width and NFD variants stopped matching, but `tw ` began matching `tw`. `information_schema.COLLATIONS` reports `utf8mb4_bin` as `PAD SPACE`, and `utf8mb4_0900_bin` and `utf8mb4_0900_ai_ci` as `NO PAD`.
- A `utf8mb4_0900_bin` table accepted all ten spellings as ten distinct primary keys. Those were the five above plus the five variants, `tw ` included.

Postgres `TEXT` under a deterministic collation, and SQLite `TEXT` under `BINARY`, compare byte-wise. Only `wrkflw_definitions.def_id` is ever compared in a query: `def_id = ?` appears only in `definitions.go`, three times. The other `def_id` columns are written and read back but never filtered.

**Definition body (kartaladev/wrkflw#154).**
- MySQL 8.0.46 stored `{"name":"a\u0000b"}` in a `JSON` column, and it reads back as hex `610062`.
- Postgres 17.10 refused it in a `JSONB` column with `22P05: unsupported Unicode escape sequence`.
- SQLite stores any `TEXT`.

`json.Marshal` writes a NUL as the six-byte escape `\u0000`, so that is the form every backend receives.

**`ErrConcurrentPublish` (kartaladev/wrkflw#157).**
- pgx `v5.11.0` (root `go.mod`) defines `ErrNoRows = newProxyErr(sql.ErrNoRows, ...)` (`conn.go:108` in the module cache).
- `internal/database/adapter_pgx.go` `pgxRow.Scan` returns `r.row.Scan(dest...)` untranslated.
- There are 14 `sql.ErrNoRows` checks in 7 non-test files under `internal/persistence/store`, and several of them have Postgres-backed not-found tests.
- The `ErrConcurrentPublish` branch itself has no test. Its godoc explains why: no deletion path exists.

**Fixture (kartaladev/wrkflw#155).**
- `definition/{event,activity,gateway}` export 17 node constructors, which is also `knownKindCount = 17` in `definition/kinds/nodetype_guard_test.go`. `richConformanceDefinition` uses 8 of them.
- The fixture sets none of `WithStartTimer`, `WithCatchTimer`, `WithBoundaryTimer` or `WithRetryPolicy`.
- `definition/testdata/golden_definition.json` carries all 17 kind names.

## Goals / Non-Goals

**Goals:**
- One meaning of "same definition ID" on all three backends and in the in-memory registries: code-point equality.
- A round-trip promise whose coverage is enforced, not claimed.

**Non-Goals:**
- Changing any MySQL column other than `wrkflw_definitions.def_id`. See proposal, Out of scope.
- Normalising IDs (NFC folding, case folding) in Go. Exact comparison is the rule, so two spellings are two definitions everywhere.
- Deciding Q1 and Q2 (below).

## Decisions

### D1. `utf8mb4_0900_bin` on `wrkflw_definitions.def_id`

The column becomes `def_id VARCHAR(255) COLLATE utf8mb4_0900_bin NOT NULL`. The collation implies the `utf8mb4` charset, so no `CHARACTER SET` clause is needed. That matters because `internal/atrest`'s `columnType` stops at `COLLATE` but not at `CHARACTER`, and a `CHARACTER SET` clause would make it report the type as `VARCHAR(255) CHARACTER SET utf8mb4`.

Rejected:
- **`utf8mb4_bin`**, which the issue names. It is `PAD SPACE`, and measured, it makes `tw` and `tw ` one key. That is a fold the column does not have today, so it trades one parity gap for another.
- **`VARBINARY(1020)`.** It is byte-exact, but the column would bound bytes, not characters. `MaxDefinitionIDRunes` documents a 255-character bound derived from `VARCHAR(255)`, so the gate's derivation would change. The `atrest` type census would also see a type change on a key column, and nothing gains from it over D1.
- **A Go-side gate.** #112 established, and the measurement confirms, that the fold is a property of a pair of IDs. No per-value check can see it.
- **Normalising IDs in Go (NFC plus case fold) on every backend.** It makes the three backends agree by making Postgres and SQLite fold too. That widens cross-ID confusion instead of closing it, and it changes stored IDs.

### D2. Edit the squashed `0001_init.sql` in place

`TestMigrations_OneFilePerDialect` enforces one migration file per dialect "while pre-release", and `git tag` lists no `v*` tag. kartaladev/wrkflw#44 records the stance that there is no persisted data before the first tag. It plans the same in-place edit for its enum columns. This follows that stance (Q3 asks the maintainer to confirm it for #152).

Rejected:
- **A `0002` migration.** It breaks the one-file policy and its guard test, for data the project says does not exist.
- **A startup check that refuses a MySQL database whose `def_id` collation is not binary.** It adds a new refusal and sentinel for pre-release databases only. It is listed as Q3 option (b), not chosen.

goose does not re-run an applied version (`Migrator.Version` reads `GetDBVersion`), so a MySQL database migrated by an earlier build keeps folding. The migration plan below names the one-statement repair.

### D3. Widen the fixture rather than narrow its claim (#155)

The issue leaves the choice open ("Either is fine. Leaving the claim as-is is not."). Widening is chosen because two places promise exhaustive round-trip, and the spec now says the same: the `DefinitionStore` godoc and the immutability scenario's "rich conformance definition" row. Narrowing would leave 9 node kinds, 3 timer markers and retry policies with no store round-trip test on any backend.

Rejected: narrowing the claim. It is honest, but it leaves the spec's new round-trip requirement with no evidence for over half the node kinds.

Shape:
- Keep `richConformanceDefinition` as one valid definition if all 17 kinds fit the authoring gate. Otherwise use a small family of valid definitions that together cover them.
- `golden_definition.json` already carries all 17 kinds. It is the first candidate for a member of that family, if it passes `ValidateDefinition`; that is to be checked in task 3.1, not assumed.

If widening exposes a field that does not survive a store round trip, that is a defect. It gets its own failing test first and is reported, not folded silently into the fixture.

### D4. Coverage is enforced by a guard, not by the comment

A store-package test scans the `NodeKind` ordinals the same way `definition/kinds/nodetype_guard_test.go` does (`kindScanLimit`). It fails when a registered kind is not carried by any round-trip definition. That is what keeps "exhaustive" true when kind 18 is added.

Rejected: a hard-coded count. It is the same honesty problem at one remove.

### D5. In-memory registries need no code change for exact IDs

`MemDefinitionRegistry` and `MapDefinitionRegistry` key a Go map by `model.Qualifier`, and Go string equality is byte equality. The spec rows for the in-memory registry are characterization rows. They pin the parity so it cannot regress.

## Risks / Trade-offs

- [A MySQL deployment that relied on folded lookups (for example, mixed-case IDs typed by an operator) now gets `ErrDefinitionNotFound`.] → BREAKING note. v0.x and pre-release. The lookup was silently returning a different ID's definition.
- [A pre-release MySQL database keeps the old collation, because goose skips applied version 1.] → The migration plan gives the repair statement (Q3 decides whether code detects it).
- [MySQL 8.0.0–8.0.16 has no `utf8mb4_0900_bin`, so the migration fails there.] → It fails loudly at `Migrate`, not silently. Q4 raises the documented floor to 8.0.17. The test suite runs `mysql:8.0`, which resolves to 8.0.46.
- [The widened fixture is a larger valid definition that future validation rules can break.] → The fixture's header already lists the structural properties that are load-bearing. D4's guard makes a dropped kind fail loudly.

## Migration Plan

1. Land the `0001_init.sql` edit while no `v*` tag exists (before `cut-v0-release`).
2. A pre-release MySQL database migrated by an earlier build is repaired with `ALTER TABLE wrkflw_definitions MODIFY def_id VARCHAR(255) COLLATE utf8mb4_0900_bin NOT NULL;`. This cannot fail on a duplicate, because a binary collation never merges keys the folding collation kept apart: measured, the `ALTER` succeeded over five distinct folded keys. Alternatively, drop and recreate the database. The repair goes in the PR description and the MySQL section of `runtime/README.md`.
3. Rollback is a revert of the migration edit. A database already created with the binary collation keeps it until recreated. Rows that are distinct only under binary comparison would then collide on an `ALTER` back. That is the rollback hazard to name in the PR.

## Cross-change coupling

- **`encode-persisted-enums-by-name` (kartaladev/wrkflw#44, wave 3) shares the persisted-format window.** Both edit the squashed `0001_init.sql` in place, which is only legitimate while no release tag exists.
  - Files: #44 edits all three dialects (`status`, `kind`, `trigger_kind` to `TEXT`). This change edits only `migrations/mysql/0001_init.sql`, the `wrkflw_definitions` block.
  - Landing order: this change lands first, because it is wave 1. #44 rebases onto it. The hunks are in different tables, so the rebase is textual only.
  - Window: both must land before `cut-v0-release`, which closes the window by tagging. This change therefore lists `cut-v0-release` under Blocks, and #44's proposal should do the same.
  - Whichever lands second re-runs `TestMigrationParity_LogicalSchemaConverges`, `TestMigrations_OneFilePerDialect` and the `internal/atrest` census against the merged file.
  - The window is owned by `cut-v0-release`. Neither change owns the other's columns.
- **`make-definition-registration-idempotent` (active).** Both modify the `definition-store` capability.
  - Spec: that change MODIFIES "In-memory registration keeps the first registration of a version" and "Message-start names are unique across globally registered definitions". This change MODIFIES "Latest resolves to the highest version on every registry" and ADDS two requirements. No requirement block is shared, so the deltas merge independently in either archive order.
  - Code: both edit godoc in `runtime/kernel/mem_definition_registry.go` (that change edits `Register`/`ErrDefinitionExists`, this change edits `ValidateDefinition`) and in `internal/persistence/store/definitions.go`. That change moves `normaliseDefinition` to a new internal package, and this change edits the `DefinitionStore` and `ErrConcurrentPublish` godoc. No landing order is required; whichever lands second rebases.
  - Its identity rule ("identical" content) is orthogonal to this change's key rule (exact ID).
- **`join-ambient-publish` (kartaladev/wrkflw#151).** It changes the querier `PublishDefinition` and its read-back run on. If Q2 is answered "translate at the adapter", the translation must cover the pgx transaction querier as well as the pool. The `pgxRow` type is shared, so it does.
- **`split-persistence-modules` (kartaladev/wrkflw#39)** moves the migration files. It should land after this change, or carry the edit.
- **`refuse-unrecognized-wire-values`** changes how trigger objects decode. The widened fixture carries only recognized trigger kinds, so it is unaffected, and its timer rows then guard that change's decoder on every backend too.

## Open Questions

These are decisions for the maintainer. Readiness is human until Q1–Q3 are answered.

**Q1 (#154). Does the "publishes on one backend, publishes on all" promise extend to the definition body?**
- (a) Enforce it. `ValidateDefinition` refuses a NUL in any string of the definition with a new sentinel wrapped in `ErrInvalidDefinition`. Applied to both registration and publish, it keeps the "same gate" promise.
- (b) State the limit as a spec'd divergence. Postgres refuses with its driver error and the other two store it, and a sentinel is exported for the Postgres refusal.
- (c) Keep the godoc-only interim (status quo).

Recommendation: (a). The issue's cost argument, "walking the whole definition on every publish", is a hypothesis: `PublishDefinition` already marshals the whole body. Registration does not, and a reflective or per-kind walk there is the open cost. A naive byte scan of the marshalled JSON for `\u0000` is wrong, because it misreads an escaped backslash (`\\u0000`). No spec is written until this is decided.

**Q2 (#157). Translate at the adapter, pin with a test, or both?**
- (a) `pgxRow.Scan` maps `pgx.ErrNoRows` to an error matching `sql.ErrNoRows`. This repo then owns the contract.
- (b) An adapter test on both adapters asserts that a no-row `QueryRow(...).Scan` matches `sql.ErrNoRows`.
- (c) Both.

Recommendation: (c). Translation is two lines. The test makes the contract this repo's own, and it fails if a driver stops wrapping. No spec delta: the observable outcomes are already spec'd as `ErrDefinitionNotFound`.

**Q3 (#152). Migration story for pre-release MySQL databases.**
- (a) In-place edit plus a documented repair statement (D2).
- (b) (a) plus a check at MySQL store open that refuses a folding `def_id` with a named error.
- (c) A `0002` migration.

Recommendation: (a), per kartaladev/wrkflw#44's stance. Needs confirmation, because #152's thread leaves it open. Answering (b) adds a requirement and tasks.

**Q4. Raise the documented MySQL floor from "8.0+" to "8.0.17+".**

Recommendation: yes; it follows from D1.

**Finding, not a question.** Other MySQL identifier columns likely fold the same way. This is a hypothesis, unmeasured here. File it as an intake issue.
