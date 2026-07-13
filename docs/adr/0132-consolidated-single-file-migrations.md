# 132. One consolidated migration file per database dialect

- Status: Accepted
- Date: 2026-07-13

## Context

The SQL store (`internal/persistence/store`) shipped its schema as an
incremental goose migration set per dialect, accreted feature-by-feature as the
engine grew:

- `migrations/postgres/` — 11 files (`0001_init` … `0011_timers_trigger`)
- `migrations/mysql/` — 4 files (`0001_init` … `0004_timers_trigger`)
- `migrations/sqlite/` — 3 files (`0001_init` … `0003_timers_trigger`)

The three sets had drifted out of step: MySQL and SQLite were added later, so
their `0001_init` already folded much of Postgres's early history, while later
features (human tasks, the timer trigger descriptor) landed as separate files in
each dialect. The Postgres set in particular carried a long tail of small
`ALTER`/`RENAME` migrations (`0003` adds outbox resilience columns, `0006` adds
call-link lease columns, `0009` adds `outbox.definition_ref`, `0011` renames
`timers.fire_at` → `next_run` and adds trigger columns) that patch tables
created several files earlier. Reading "what is the current schema?" meant
mentally replaying the whole chain.

Two facts make this the right moment to squash the sets to a single
authoritative file per dialect:

1. **The library is unreleased** — there are no git tags and no shipped version.
   No consumer has a database provisioned from these migrations, so there is no
   deployed goose version history (`goose_db_version`) to preserve. Squashing the
   numbered sequence loses nothing for any real database. (Once released, adding
   schema changes will resume as new numbered files on top of the consolidated
   baseline; this squash is a one-time pre-1.0 cleanup, not a new policy of
   rewriting history.)
2. **A strong equivalence guardrail already exists.** `TestMigrationParity_
   LogicalSchemaConverges` applies every migration for all three dialects and
   asserts the resulting logical schema (table names, columns, nullability, PK
   membership) converges. It compares the *final* schema, not the file count, so
   it proves a consolidated file yields exactly the schema the old chain did. The
   dialect conformance suites additionally exercise every column and query
   against the real migrated schema.

## Decision

Collapse each dialect's migration set to a **single `0001_init.sql`** that
declares the schema in **final state**, and delete the superseded incremental
files.

The consolidation is a **clean fold, not a verbatim concatenation**: columns
introduced by later `ALTER`s are declared inline on their `CREATE TABLE`, indexes
are written in their final form (e.g. Postgres's `wrkflw_outbox_unpublished_idx`
— created in `0001` then dropped in `0003` — is simply absent), and the
`wrkflw_timers.fire_at` → `next_run` rename plus the `trigger_kind`/
`trigger_payload` additions are baked directly into the `CREATE TABLE`. Each file
keeps a `-- +goose Down` section that drops every table (FK-safe order: the
`wrkflw_journal` → `wrkflw_instances` reference means journal drops first), so
`Down`/`DownTo` and the migrator lifecycle continue to work.

Documented dialect divergences are preserved: MySQL keeps the reserved-word
`trigger_` journal column and full (non-partial) indexes; SQLite keeps
`TEXT`/`INTEGER` affinities, RFC3339 UTC timestamp strings, and plain indexes.

The requirement is made executable by `TestMigrations_OneFilePerDialect`
(package `store`), which asserts each dialect directory embeds exactly one
`*.sql` file — reintroducing an incremental file fails the build.

The scope is the three engine store dialects only. The casbin authz migration
(`internal/authz/casbin/migrations/0001_casbin_rule.sql`) is already a single
file and is untouched.

## Consequences

- Each dialect now has one authoritative, readable schema file; understanding the
  current schema no longer requires replaying an `ALTER`/`RENAME` history.
- The goose head version for every dialect is now `1`. Version-coupled tests were
  updated to match: `store` (`TestMigrator_SQLiteLifecycle`,
  `TestMigrator_SQLiteUpByOneAndDown`), the public `persistence` facade
  (`TestMigrator_FacadeLifecycle_SQLite`, `TestMigrator_Postgres_Introspection`,
  `TestMigrator_MySQL_Introspection`), and the `examples/migrate` CLI test. The
  migrator's multi-step lifecycle (`UpByOne`/`Down`/`UpTo`/`DownTo`) is still
  exercised against the single migration.
- Correctness is guaranteed by the pre-existing parity guardrail plus the full
  round-trip and conformance suites, all green across Postgres, MySQL, and SQLite
  after the fold — the consolidated schema is provably identical to the old chain.
- This is safe **only because the library is pre-release**. It is not a precedent
  for rewriting migration history after a database has been provisioned in the
  field; post-1.0 schema changes will be additive numbered files on top of this
  baseline.
- `TestMigrations_OneFilePerDialect` locks the invariant so the incremental style
  cannot silently creep back before release.
