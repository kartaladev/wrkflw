# Persistence / Migration Ops (P1-C) — Design

- **Date:** 2026-07-03
- **Status:** Approved (brainstorming)
- **Track:** P1-C from `docs/plans/2026-06-30-production-readiness-backlog.md`
- **ADRs to be written:** 0085 (migration lifecycle API), 0086 (opt-in `WarnUnsafeConfig`)
- **Next free ADR before this work:** 0085

## Problem

The production-readiness audit (2026-06-30) flagged the persistence/migration surface as
operationally thin. Since that audit the store-unification program (ADR-0081) collapsed the
former `internal/persistence/{postgres,mysql}` packages into ONE neutral store over a `dialect`
abstraction, and added SQLite (ADR-0082). The migration sets now live under
`internal/persistence/store/migrations/{postgres,mysql,sqlite}` and are driven by
`github.com/pressly/goose/v3` `Provider`. That reframes the original gaps but does not close them:

1. **No migration lifecycle beyond `Up`.** The facade exposes only `Migrate(ctx, pool)` /
   `MigrateMySQL(ctx, db)` / `MigrateSQLite(ctx, db)`, each calling `provider.Up` and nothing else.
   There is no way for a consumer to introspect the applied version, list migration status, or
   roll back — even though goose's `Provider` already supports all of it and the `-- +goose Down`
   blocks are present and substantive in every `.sql` file.
2. **Migration-set divergence with no guardrail.** Postgres has 9 incremental files, MySQL 2,
   SQLite 1 consolidated. Cross-dialect **conformance tests** already prove behavioral parity of
   the store, but nothing asserts the three migration sets converge to the same *logical schema*,
   so a future edit to one dialect can silently drift.
3. **Silently-unsafe-if-forgotten opt-ins.** Multi-replica call-link lease (exactly-once child
   notification), `WithHistoryCap` (JSONB/JSON/TEXT snapshot bloat), and the pruning cron
   (unbounded table growth) are all opt-in with no consumer-facing production checklist and no way
   to be reminded.
4. **`PruneTimers` gap.** `internal/persistence/store.Pruner` already implements
   `PruneTimers(ctx, cutoff)` (pruner.go:182) but it is absent from the public
   `persistence.Pruner` interface, so a consumer holding the interface cannot prune timer rows and
   they leak.

Connection-pool / statement-timeout / isolation-level configuration is fully delegated to the
consumer with no guidance — this is a documentation gap folded into (3).

## Non-goals / YAGNI

- **No shipped migration daemon or binary.** `wrkflw` is a library (see CLAUDE.md "Library-first").
  The migration *CLI* is `examples/` reference wiring only, never a product binary.
- **No auto-migrate on import or on `Open*`.** Migration stays an explicit consumer call.
- **No physical-type parity.** JSONB vs JSON vs TEXT, TIMESTAMPTZ vs DATETIME(6) vs TEXT are
  legitimate per-dialect differences; the guardrail compares *logical* structure only.
- **No automatic startup warnings.** The library cannot know deployment topology (single- vs
  multi-replica), so a blanket `slog.Warn` would be noisy or wrong. Warnings are opt-in.
- **No new runtime dependency.** goose v3 is already vendored; the CLI uses stdlib `flag`.

## Design

### Sub-project 1 — Migration lifecycle API (core, ADR-0085)

**Internal (`internal/persistence/store`).** Refactor the three `migrate_{postgres,mysql,sqlite}.go`
Up-only helpers into a reusable internal `Migrator` that wraps a `goose.Provider` for a given goose
dialect + embedded sub-`fs.FS`. The `Migrator` is **stateless w.r.t. the provider**: it holds the
connection (`*pgxpool.Pool` for Postgres, `*sql.DB` for MySQL/SQLite), the goose dialect constant,
and the embedded sub-FS, and constructs a fresh `goose.Provider` inside each method call. This
mirrors the current per-call construction (safe for parallel tests, no shared mutable state) and
avoids leaking a `Close()` lifecycle onto the consumer. For Postgres the internal `stdlib.OpenDBFromPool`
shim is opened and closed per call exactly as today (closing the shim never closes the pool).

Internal per-dialect constructors: `newPostgresMigrator(pool)`, `newMySQLMigrator(db)`,
`newSQLiteMigrator(db)` → `*Migrator`. Methods wrap goose:

| Method | goose call | Notes |
|---|---|---|
| `Up(ctx) error` | `Provider.Up` | apply all pending |
| `UpByOne(ctx) error` | `Provider.UpByOne` | apply next only |
| `UpTo(ctx, v int64) error` | `Provider.UpTo` | apply through `v` |
| `Down(ctx) error` | `Provider.Down` | **data loss** — rolls back one |
| `DownTo(ctx, v int64) error` | `Provider.DownTo` | **data loss** — rolls back to `v` |
| `Version(ctx) (int64, error)` | `Provider.GetDBVersion` | current applied version |
| `Status(ctx) ([]MigrationStatus, error)` | `Provider.Status` | mapped to our DTO |
| `HasPending(ctx) (bool, error)` | `Provider.HasPending` | any unapplied? |

Existing `MigratePostgres` / `MigrateMySQL` / `MigrateSQLite` free funcs become thin wrappers →
`newXMigrator(...).Up(ctx)` (behavior-preserving; their tests continue to pass).

**Public facade (`persistence`).**

```go
// Migrator drives schema migrations for one backend. Construct via the
// dialect-specific NewXMigrator; every method is safe to call after construction.
type Migrator interface {
    Up(ctx context.Context) error
    UpByOne(ctx context.Context) error
    UpTo(ctx context.Context, version int64) error
    Down(ctx context.Context) error                 // DATA LOSS: see godoc
    DownTo(ctx context.Context, version int64) error // DATA LOSS: see godoc
    Version(ctx context.Context) (int64, error)
    Status(ctx context.Context) ([]MigrationStatus, error)
    HasPending(ctx context.Context) (bool, error)
}

// MigrationStatus is one migration's applied state, decoupled from goose's type.
type MigrationStatus struct {
    Version   int64     // migration version (filename numeric prefix)
    Source    string    // migration file base name
    Applied   bool      // whether it is applied to the DB
    AppliedAt time.Time // zero if not applied
}

func NewPostgresMigrator(pool *pgxpool.Pool) (Migrator, error)
func NewMySQLMigrator(db *sql.DB) (Migrator, error)
func NewSQLiteMigrator(db *sql.DB) (Migrator, error)
```

- Constructors follow ADR-0083: return `(Migrator, error)` and reject a nil `pool`/`db` with a
  wrapped `store.ErrNilDependency` (reuse the existing sentinel; typed-nil guarded via the existing
  `isNilDep` reflect check).
- `Down`/`DownTo` godoc carries a prominent, unmissable **data-loss** warning (chosen guardrail
  level: expose plainly + strong godoc — the operator owns their DB).
- `Status` maps `[]*goose.MigrationStatus` → `[]MigrationStatus`; goose's `MigrationStatus.State`
  (`StatePending`/`StateApplied`) maps to `Applied bool`; `Source.Version`/`Source.Path` →
  `Version`/`Source` (base name).
- Back-compat `Migrate`/`MigrateMySQL`/`MigrateSQLite` remain.

**Reference CLI — `examples/migrate/main.go`.** stdlib `flag` only. Shape:

```
migrate -dialect=postgres -dsn=... up
migrate -dialect=mysql    -dsn=... status
migrate -dialect=sqlite   -dsn=... downto 3
```

Subcommands `up | upto <v> | down | downto <v> | status | version`. It opens the connection,
constructs the matching `NewXMigrator`, calls the method, prints results (a small table for
`status`), and exits non-zero on error. Thin wiring; all logic lives in the library.

### Sub-project 2 — Schema-parity guardrail

One cross-dialect test (in `internal/persistence/store`, testcontainers-gated for PG+MySQL,
in-memory `modernc.org/sqlite` for SQLite):

1. Migrate each backend to head via the internal migrator.
2. Introspect each into a normalized **logical schema**
   `map[table]map[column]columnFacts{ nullable bool; primaryKey bool }`:
   - Postgres/MySQL: query `information_schema.columns` + `information_schema.table_constraints`/
     `key_column_usage` for PK membership.
   - SQLite: `sqlite_master` for tables + `PRAGMA table_info(<t>)` (`notnull`, `pk`).
3. Exclude goose's bookkeeping table (`goose_db_version`) and any non-`wrkflw_` tables.
4. Assert all three logical schemas are equal: same table set, same column set per table, same
   `nullable` and `primaryKey` per column. Physical types are intentionally NOT compared.

A mismatch fails with a readable diff (which table/column diverged and how). This is the drift
backstop for the 9-vs-2-vs-1 file-count divergence. Uses `database.RunTestDatabase` /
`RunTestMySQL` per the testcontainers skill; skips cleanly when Docker is unavailable exactly as
the existing conformance tests do.

### Sub-project 3 — Production-safety docs + opt-in warn helper (ADR-0086)

**Docs — `docs/production-checklist.md` (new).** Sections:

- **Connection pool** — sizing guidance (pgx `pool_max_conns`, `database/sql` `SetMaxOpenConns`/
  `SetMaxIdleConns`/`SetConnMaxLifetime`), relative to relay/scheduler concurrency.
- **Statement timeout & isolation** — recommend a server- or session-level `statement_timeout`
  (PG) / `max_execution_time` (MySQL); note the store relies on `READ COMMITTED` semantics and
  optimistic CAS, so a stricter isolation level is unnecessary and a looser one unsafe.
- **Opt-in MUST-DOs for production** — (a) multi-replica exactly-once child notification requires
  the call-link lease/ownership wiring; (b) `WithHistoryCap` to bound snapshot growth; (c) a
  consumer-owned pruning cron (links to `retention.md`). Each states the failure mode if skipped.

Cross-link from `README` and `docs/retention.md`.

**Helper — `persistence.WarnUnsafeConfig(logger *slog.Logger, p DeploymentProfile)`.** Opt-in; the
consumer calls it explicitly at their own startup.

```go
type DeploymentProfile struct {
    MultiReplica       bool // more than one engine replica running
    CallLinksEnabled   bool // call-activity / sub-process wiring in use
    CallLinkLeaseWired bool // lease/ownership configured for exactly-once
    HistoryCapSet      bool // WithHistoryCap applied
    PruningScheduled   bool // a retention/pruning job is running
}

// WarnUnsafeConfig emits one slog.Warn per known-risky combination in p. It is a
// no-op for a safe profile and never returns an error. Intended to be called once
// at consumer startup. It does NOT inspect the live system — the consumer asserts
// their own topology via p.
func WarnUnsafeConfig(logger *slog.Logger, p DeploymentProfile)
```

Warn rules (each a distinct, documented message):
- `MultiReplica && CallLinksEnabled && !CallLinkLeaseWired` → duplicate child notifications risk.
- `!HistoryCapSet` → unbounded snapshot growth.
- `!PruningScheduled` → unbounded outbox/call-link/chain-link/dedup/timer growth.

A nil `logger` falls back to `slog.Default()`. Testable by installing a capturing `slog.Handler`
and asserting the emitted records for representative profiles.

ADR-0086 records the decision and, crucially, the rejected alternative (automatic constructor-time
warnings) and why (library can't know topology → false positives).

### Sub-project 4 — `PruneTimers` on the public interface

Add to `persistence.Pruner`:

```go
// PruneTimers deletes timer rows whose fire_at is strictly before cutoff.
// Returns the number of rows deleted. Applies to all dialects.
PruneTimers(ctx context.Context, cutoff time.Time) (int64, error)
```

The neutral `store.Pruner` already implements it, so the `var _ Pruner = (*store.Pruner)(nil)`
compile-time check keeps compiling. Add a facade test exercising `PruneTimers` through the
interface (mem/SQLite-backed as the other pruner facade tests are).

## Testing strategy (TDD strict)

Every new exported symbol is preceded by a failing test (red observable in the transcript), per
CLAUDE.md TDD Operational Discipline. Concretely:

- **Migrator methods** — unit/integration tests per dialect: `Up` then `Version` returns head;
  `Status` lists all sources with correct applied flags; `HasPending` true before / false after;
  `DownTo(0)` drops all `wrkflw_` tables; `UpTo(n)`/`UpByOne` land on the right version. Nil-conn
  constructors return wrapped `ErrNilDependency` (incl. typed-nil). PG uses `RunTestDatabase`
  (note: unlike `RunTestMySQL` it does NOT auto-migrate — the test drives the migrator itself),
  MySQL uses `RunTestMySQL`, SQLite in-memory.
- **Back-compat wrappers** — existing `Migrate*` tests must stay green (pure refactor underneath).
- **Schema-parity** — the guardrail test above (its own red state: write it, watch it fail if a
  column is intentionally perturbed in a scratch run, then keep the real assertion).
- **`WarnUnsafeConfig`** — table test over `DeploymentProfile` inputs → expected warn messages,
  via a captured slog handler (uses the project `table-test` skill closure form).
- **`PruneTimers` facade** — deletes rows before cutoff, keeps rows at/after cutoff, through the
  public interface.
- **CLI** — a smoke test invoking the subcommands against SQLite in-memory (or a thin
  `run(args, deps)` seam tested directly) so `examples/migrate` is not the only path exercised.

Coverage target ≥85% per touched package; `go test -race ./...` green; `golangci-lint run ./...`
clean.

## Rollout / sequencing

1. Internal `Migrator` refactor + back-compat wrappers (behavior-preserving; existing tests guard).
2. Public facade `Migrator` + `MigrationStatus` + constructors (ADR-0085).
3. Reference CLI `examples/migrate`.
4. Schema-parity guardrail test.
5. `docs/production-checklist.md` + `WarnUnsafeConfig` (ADR-0086) + cross-links.
6. `PruneTimers` on the public `Pruner` interface.

Each is an independent, individually-reviewable task; suitable for subagent-driven development with
a final whole-branch opus review before merge to `main`.

## Open questions

None outstanding — the four brainstorming forks are resolved: Migrator type (A); rollback exposed
plainly with strong godoc; safety surfaced as docs + opt-in warn helper; full P1-C scope in one
spec.
