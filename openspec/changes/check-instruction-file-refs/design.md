# Design

## Context

See proposal.md, *What Changes*, for the measured premises: the #172 rows are the only dangling repo-relative paths across the 30 tracked instruction files, and the other non-resolving strings are illustrative.

## Goals / Non-Goals

**Goals:**
- The reference check is shown red under a planted dangling reference, and green on an at-limit neighbor (a resolving path, an illustrative block, a placeholder path) that it must still accept.
- The check is at zero on the tree once the testcontainers skill file is corrected.
- The check's header states its matched domain and its limits accurately; a documented limit fails closed or is fixed.

**Non-Goals:**
- Any new guard beyond the instruction-file reference check.
- `pkg.Ident` resolution, unless Q1 picks it.
- References inside Go comments (owned by `fix-doc-rot`).

## Decisions

### D1. Correct the skill file before the guard lands

The #172 rows are fixed by hand first (task 1.1), so the guard lands at zero and its red can be shown by reverting that fix, which is a real regression rather than a synthetic one.

## Risks / Trade-offs

- [The instruction-file guard fires on the day `remove-casbin` lands] → `docs/agents/eventually-waits.md` cites `internal/authz/casbin/...` paths in historical tables. Whatever Q1 decides about historical citations must cover them. Otherwise the change that deletes casbin also has to edit agent docs. Record the exemption form before landing.
- [`fix-doc-rot` Q1 may add a doc-link guard while this change adds a reference guard] → They check different things (rendered `href`s in Go comments versus repo paths in instruction files). If both land, CONTRIBUTING.md lists each once, and `harden-ci-guard-wiring`'s wiring check covers both.

## Migration Plan

Contributor tooling only, so there is nothing to deploy. Rollback is a revert.

## Coupling

| Change | Shared surface | Owner / order |
|---|---|---|
| `harden-ci-guard-wiring` | `ci.yml` (this check's invocation), `CONTRIBUTING.md` guard list | That change asserts every `scripts/check-*.sh` is wired, so it covers this check. Either order: if this lands first, its invocation is in place when the wiring check arrives; if second, the wiring check refuses it until it is wired. |
| `remove-casbin` / `replace-casbin-with-composed-deciders` | casbin paths in `docs/agents/` | They own the deletion. Q1's historical-table answer decides who edits the agent docs. |
| `fix-doc-rot` | a possible doc-link guard, `CONTRIBUTING.md` | That change owns references in Go comments. Either order. |

## Open Questions

This is a decision the maintainer has not made, so Readiness is `human`. Tasks follow the recommendation and are marked *pending Q1*. A different answer changes only task group 2.

**Q1 (#173). What the instruction-file guard resolves.**
- **Recommended:** scope is the 30 tracked instruction files. It resolves repo-relative file and directory paths (relative to the repo root, or to the citing file for `./` and `../`) and `github.com/kartaladev/wrkflw/...` import paths. It skips fenced blocks marked as illustrative and paths written with a placeholder (`<module>`, `…`). It fails on anything else that does not resolve. `pkg.Ident` resolution is deferred: it needs a symbol index, and the one live instance (#172) is caught through its paths. This scope is at zero once #172 is fixed, by the measurement in the proposal.
- Open within Q1: whether historical tables in `docs/agents/` (paths that were true when written) are exempt, or are rewritten when the path goes away. That decides who edits them when `remove-casbin` lands.
