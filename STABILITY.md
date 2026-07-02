# Stability and versioning policy

This document states the compatibility promise for the `github.com/zakyalvan/krtlwrkflw`
module. It is intentionally honest about the project's current maturity.

## Current status: pre-1.0, unreleased

The module has **no released version yet** (no `v0.x` or `v1.x` git tag). It is under
active development. Until a tagged release exists, **every exported symbol in every
root package is subject to change without notice**, and consumers should pin to a
specific commit (Go modules pseudo-version) and expect to read the diff on each bump.

Do not assume any API is frozen until this document says a version line is.

## Versioning: Semantic Versioning (SemVer 2.0.0)

Once releases begin, the module follows [Semantic Versioning](https://semver.org/):

- **MAJOR** (`vX`) — breaking changes to the public root-package API. Per Go module rules,
  `v2+` ships under a versioned module path (`/v2`, `/v3`, …); `v0`/`v1` share the base path.
- **MINOR** (`vX.Y`) — backwards-compatible additions (new functions, options, fields,
  node kinds, transports).
- **PATCH** (`vX.Y.Z`) — backwards-compatible bug fixes only.

### The `v0.y.z` (pre-1.0) phase

While the module is in `v0`, SemVer permits breaking changes in any release. We will use
this latitude responsibly:

- `v0.y.z` → `v0.(y+1).0` **may** contain breaking changes; the CHANGELOG/release notes
  will call them out explicitly.
- `v0.y.z` → `v0.y.(z+1)` is reserved for bug fixes and is intended to be safe to take.

A `v1.0.0` release is the point at which the public root-package API
(`engine/`, `model/`, `runtime/`, `action/`, `authz/`, the transport adapters, …) is
considered stable and the full MAJOR/MINOR/PATCH promise above applies.

## What "public API" means

The compatibility promise covers only the **exported, module-root packages** — the
importable surface a consumer embeds (see the README "Package map"). It does **not** cover:

- Anything under `internal/` — these are implementation details, never importable by
  consumers, and may change at any time regardless of version.
- The `examples/` reference wiring — illustrative `main` packages, not a supported API.
- Behaviour explicitly documented as reserved, experimental, or "not yet emitted".
- The on-disk database schema and migration files as a *direct* contract — they are an
  implementation detail of each backend adapter (Postgres, MySQL, SQLite), evolved via
  the migration mechanism, not a hand-editable surface.

## Deprecation taxonomy

When an exported symbol must be retired, we deprecate before removal rather than breaking
abruptly (subject to the pre-1.0 latitude above):

1. **Mark.** The symbol gets a Go `// Deprecated:` doc comment, as the first line of a
   paragraph, naming the replacement and the reason. Tooling (`gopls`, `staticcheck`'s
   `SA1019`, IDEs) surfaces these to consumers automatically:

   ```go
   // Deprecated: use NewRunnerWithConfig instead; NewRunner cannot express the
   // retry-policy option and will be removed in v2.
   func NewRunner(...) *Runner { ... }
   ```

2. **Keep working.** A deprecated symbol continues to function for at least one MINOR
   release line after it is marked, so consumers have a release in which both the old and
   the replacement exist.

3. **Remove.** Deprecated symbols are removed only in a MAJOR release (or, during `v0`, in
   a MINOR release that the notes flag as breaking). The release notes list every removal.

We do not silently change the behaviour of a non-deprecated symbol; a behaviour change to
an existing symbol is treated as breaking and follows the same MAJOR/`v0`-MINOR rule.

## Go and dependency baseline

- **Go 1.25** is the minimum supported toolchain (a hard requirement; see the README
  "Locked tech stack").
- Locked dependencies (PostgreSQL 17, MySQL 8.0+, SQLite (`modernc.org/sqlite`), `expr-lang/expr`,
  `watermill`, `gocron` pinned to v2.21.2, `clockwork`, `casbin`, `samber/do` v2) are changed only
  via an ADR. A change to the minimum Go version or a locked dependency major is treated as a
  breaking change.

The SQLite backend is test/single-node-oriented — it is not supported for multi-replica deployments
(`persistence.NewSQLiteAdvisoryLockOwnership` is fail-loud). The `Deduper.Seen` signature change
(driver-tx param dropped) is one of the pre-1.0 breaking changes flagged in the CHANGELOG (ADR-0081).
