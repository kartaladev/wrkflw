# 8. Consumer-facing persistence façade over an internal implementation

- Status: Accepted
- Date: 2026-06-21

## Context

CLAUDE.md states two things that are in tension for concrete persistence:

- "`internal/` — non-exported implementation details (**concrete persistence**,
  outbox plumbing, casbin adapters, watermill wiring) that consumers must not
  import."
- "Library-first, always. The product is the module-root public API ... Every
  feature must be reachable and ergonomic through that API."

A consumer embedding the engine must be able to construct a Postgres-backed store
and wire it into their `Runner`. If the concrete implementation lives entirely in
`internal/`, it is unreachable; if it lives entirely at module root, it leaks SQL
and pgx plumbing into the public surface.

## Decision

Split persistence into a **thin module-root façade** over an **internal
implementation**:

- `internal/persistence/postgres/` — the concrete implementation: SQL, `pgx`
  wiring, the `DBTX` querier seam, row mapping, the trigger codec, the embedded
  migrations, and the relay-loop internals. Consumers never import it; it may
  change freely without a public semver impact.
- `persistence/` (module root) — the consumer-facing API, the product surface:
  - `OpenPostgres(ctx, *pgxpool.Pool, ...Option) (*Store, error)` returning a
    type that satisfies the `runtime.Store` port,
  - `Migrate(ctx, db) error` (consumer-run, never auto),
  - the `Relay`, `Publisher`, and definition-cache types + options,
  - re-exported sentinels (`ErrConcurrentUpdate`, `ErrInstanceNotFound`).

  Its functions delegate to `internal/persistence/postgres`. The façade returns
  **stable interface/port types**, so the public API is stable even as the
  internal impl evolves.

A plain constructor is always offered (no DI container required), per the
library-first DI rule.

## Consequences

**Easier:** honours both CLAUDE.md rules literally — concrete persistence is in
`internal/`, yet every persistence feature is reachable through a module-root
package; the public surface stays small and stable (interfaces, constructors,
sentinels); internal SQL/pgx churn never breaks consumers.

**Harder / trade-offs:** a thin indirection layer to maintain (root constructors
that delegate inward); care needed to keep genuinely-public types out of
`internal/` (e.g. `OutboxEvent`/`Store` live in `runtime/`, options in
`persistence/`). This façade/internal split becomes the **template** for the
later watermill (Eventing), gocron (Scheduling), and casbin (Authorization)
sub-projects, which face the identical tension.
