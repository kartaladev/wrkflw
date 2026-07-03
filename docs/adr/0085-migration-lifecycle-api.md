# 85. Migration lifecycle API: public Migrator facade

- Status: Accepted
- Date: 2026-07-03

## Context

`wrkflw` has always exposed a one-shot `persistence.Migrate(ctx, pool)` helper
that applies all pending Postgres migrations in a single call — good enough for
"migrate on startup" usage but insufficient for:

- **Introspection**: a consumer cannot ask "which migrations are applied?" or
  "is there anything pending?" without reaching into internal packages.
- **Selective up/down**: operators cannot apply migrations one at a time, up to
  a target version, or roll back a bad migration without writing their own
  goose wiring.
- **Multi-dialect parity**: the single-call helpers existed for Postgres;
  MySQL and SQLite consumers had no equivalent surface.

The internal `store.Migrator` type (Task 1) already implements the full
lifecycle (`Up`/`UpByOne`/`UpTo`/`Down`/`DownTo`/`Version`/`Status`/`HasPending`)
per dialect, but it is in an `internal/` package and exposes goose's concrete
`StatusRow` type. Consumers must not import `internal/` paths, and goose types
must not leak into the public API.

A reference CLI for operators (Task 3) also needs a stable, dialect-agnostic
migration surface to build against — it cannot import `internal/persistence/store`.

## Decision

We will expose a public `persistence.Migrator` interface and a
`persistence.MigrationStatus` DTO that wrap the internal `store.Migrator` without
leaking goose types.

Specifically:

1. **`persistence.Migrator` interface** — declares `Up`, `UpByOne`, `UpTo`,
   `Down`, `DownTo`, `Version`, `Status`, and `HasPending`. The destructive
   `Down`/`DownTo` methods carry prominent DATA-LOSS warnings in their godoc
   so no operator can invoke them without reading the warning.

2. **`persistence.MigrationStatus` DTO** — a plain struct (`Version int64`,
   `Source string`, `Applied bool`, `AppliedAt time.Time`) that is the public
   projection of `store.StatusRow`. goose's types stop at the facade boundary.

3. **Per-dialect constructors** — `NewPostgresMigrator(*pgxpool.Pool)`,
   `NewMySQLMigrator(*sql.DB)`, and `NewSQLiteMigrator(*sql.DB)`, each returning
   `(Migrator, error)`. Nil inputs are rejected with a wrapped
   `store.ErrNilDependency`, consistent with ADR-0083.

4. **Back-compat `Migrate*` wrappers retained** — `persistence.Migrate` (Postgres
   Up-only) is not removed; existing call sites compile unchanged.

5. **Internal stateless provider-per-call** — the `store.Migrator` constructs a
   fresh goose `Provider` for each operation (see Task 1 implementation). The
   public `migrator` adapter is a thin wrapper; it adds no extra state and is
   safe to call concurrently.

6. **Reference CLI lives in `examples/`** — a standalone operator CLI (Task 3)
   that surfaces these methods as subcommands will live in `examples/`, consistent
   with the library-first principle: we do not ship a daemon or a binary; the
   consumer assembles one from the provided pieces.

## Consequences

- **Consumers can introspect and roll back**: any embedding application can
  wire `persistence.NewPostgresMigrator(pool)` into its admin surface and expose
  migration status to operators without importing internal packages.
- **goose types do not leak**: the `pressly/goose/v3` import stays inside
  `internal/persistence/store`; swapping goose for another migration tool is
  still a one-package change.
- **Destructive rollback is the operator's responsibility**: `Down`/`DownTo`
  are exposed deliberately but are clearly marked DATA-LOSS in godoc and in this
  record. No production safety interlock is wired into the library itself; that
  is a consumer concern.
- **The reference CLI (Task 3) has a stable build target**: `examples/` tooling
  can import only `persistence.Migrator` and never needs to reference internal
  migration code.
- **Existing `persistence.Migrate` call sites are unaffected**: no breaking
  change to existing consumers on this ADR boundary.
