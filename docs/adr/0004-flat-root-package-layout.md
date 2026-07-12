# 4. Public packages live at the module root, not under pkg/

- Status: Accepted
- Date: 2026-06-20

## Context

CLAUDE.md's Repository Layout section prescribes a `pkg/` prefix for the public
engine library and `internal/` for non-exported implementation. The `pkg/`
convention is widely used but not required by Go; many established libraries
expose their public API directly at the module root, giving shorter, cleaner
import paths. The project owner decided the public engine packages should sit at
the module root rather than under `pkg/`.

## Decision

We will place the **public engine packages at the module root** — e.g.
`engine/`, `model/`, `action/`, `authz/`, `runtime/` — with no `pkg/` prefix.

- `internal/` is retained for non-exported implementation details consumers must
  not import (concrete persistence, outbox plumbing, casbin adapters, watermill
  wiring).
- `examples/` (reference wiring) and `docs/` are unchanged.
- The module import path is the repository URL
  (`github.com/kartaladev/wrkflw`), so a consumer imports e.g.
  `github.com/kartaladev/wrkflw/engine`.

This decision supersedes the `pkg/`-based layout described in CLAUDE.md's
Repository Layout section; CLAUDE.md must be updated to match (follow-up).

## Consequences

- Shorter, more direct import paths for library consumers.
- The CLAUDE.md Repository Layout section and any `pkg/`-referencing guidance
  must be updated so future work does not re-introduce `pkg/`. Until that edit
  lands, this ADR is the authority.
- The library-first rules from CLAUDE.md still hold verbatim — only the
  directory prefix changes; the root packages *are* the product, transports stay
  mountable, and the core stays free of transport/storage/bus imports.
- The `internal/` boundary still enforces what consumers may not import, so the
  loss of the `pkg/` signal does not weaken encapsulation.
```
