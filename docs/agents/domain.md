# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

**Layout: single-context.** One `CONTEXT.md` at the repo root, holding the glossary of domain terms.

## Before exploring, read this

- **`CONTEXT.md`** at the repo root: the glossary of domain terms.

If it doesn't exist, **proceed silently**. Don't flag its absence; don't suggest creating it upfront. The `/domain-modeling` skill (reached via `/grill-with-docs` and `/improve-codebase-architecture`) creates it lazily when terms actually get resolved.

**Where decisions live.** Record a resolved decision in the commit message and the PR body, and state the constraint it produced in a comment on the code it governs — naming an identifier a reader can jump to. This repo keeps no ADR directory: the one it had was deleted deliberately, and `scripts/check-doc-refs.sh` fails the build on any `*.go` citation of it.

## File structure

```
/
├── CONTEXT.md
├── engine/
├── persistence/
├── transport/
└── … (Go packages at the repo root)
```

This is a single-module Go repo: packages live at the root (`engine/`, `persistence/`, `transport/`, `runtime/`, …), not under `src/`. There is no `CONTEXT-MAP.md`; the one root `CONTEXT.md` covers the whole module.

If this repo ever splits into genuinely separate bounded contexts, promote it to multi-context by adding a root `CONTEXT-MAP.md` pointing at per-context `CONTEXT.md` files, and update this file.

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `CONTEXT.md`. Don't drift to synonyms the glossary explicitly avoids.

If the concept you need isn't in the glossary yet, that's a signal: either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/domain-modeling`).

## Flag decision conflicts

If your output contradicts a decision already recorded — as a constraint in a comment on the code it governs, or in the commit or PR that introduced it — surface it explicitly rather than silently overriding:

> _Contradicts the constraint stated on `ErrScopeLocalWithCompensateRef`, but worth reopening because…_
