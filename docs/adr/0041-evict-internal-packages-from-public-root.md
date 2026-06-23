# 41. Evict test/internal-only packages from the public root; reaffirm flat layout

- Status: Accepted
- Date: 2026-06-23

## Context

FOLLOWUPS.md item ① proposed introducing a `pkg/` directory and moving
"plumbing" out of the module root so the root holds "only workflow code". A
review (docs/specs/2026-06-23-followups-resolution-design.md) found:

- The repository already implements the façade pattern: `internal/` holds the
  concrete implementations (authz, eventing, observability, persistence,
  scheduling) and the matching root packages are thin consumer-facing façades.
  Plumbing is therefore already hidden.
- Re-introducing `pkg/` would reverse ADR-0004 (flat root layout, an explicit
  owner decision), break the import path of every moved package — the precise
  harm the library-first rule exists to prevent — and `pkg/X` is still public,
  so it would not encapsulate anything.
- Exactly two root packages are not part of the public API:
  - `database` — a single testcontainers helper (`RunTestDatabase`) imported
    only by `_test.go` files. Sitting in the public root, it drags the heavy
    testcontainers dependency into any consumer's import graph.
  - `expreval` — the expr-lang wrapper. It exposes only `New()` and
    `EvalBool/EvalDuration/EvalString(code, env)`; the engine drives it with
    process variables. It has no consumer extension surface (no custom-function
    registration, no options).

## Decision

We will **reject the `pkg/` reorg** and instead move only the two non-public
packages under `internal/`:

- `database/` → `internal/database` (package name stays `database`).
- `expreval/` → `internal/expreval` (package name stays `expreval`).

All other root packages remain flat at the module root. This **reaffirms
ADR-0004**. A root `doc.go` provides a "start here" overview so consumers can
tell the ~5 entry-point packages from the supporting façades — solving the
"unclear entry point" pain with documentation rather than directory surgery.

## Consequences

- The public import surface drops from 16 to 14 packages; testcontainers no
  longer appears in a consumer's import graph.
- `internal/database` and `internal/expreval` remain importable by every
  in-module package, so all existing callers keep working after only their
  import paths are updated; package-qualified call sites are unchanged.
- ADR-0004's flat layout stands; future work must not re-introduce `pkg/`.
- `engine` and `authz` now import `internal/expreval`. This still satisfies the
  rule that engine/workflow code reaches the expression vendor only through the
  in-repo wrapper — the wrapper merely moved into `internal/`, which is more
  honest about it being an implementation detail.
