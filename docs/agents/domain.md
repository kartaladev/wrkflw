# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

**Layout: single-context.** One `CONTEXT.md` at the repo root, one `docs/adr/` for all decisions.

## Before exploring, read these

- **`CONTEXT.md`** at the repo root: the glossary of domain terms.
- **`docs/adr/`**: read the ADRs that touch the area you're about to work in.

If any of these files don't exist, **proceed silently**. Don't flag their absence; don't suggest creating them upfront. The `/domain-modeling` skill (reached via `/grill-with-docs` and `/improve-codebase-architecture`) creates them lazily when terms or decisions actually get resolved.

## File structure

```
/
├── CONTEXT.md
├── docs/
│   └── adr/
│       ├── 0001-record-architecture-decisions.md
│       └── 0002-engine-core-execution-model.md
├── engine/
├── persistence/
├── transport/
└── … (Go packages at the repo root)
```

This is a single-module Go repo: packages live at the root (`engine/`, `persistence/`, `transport/`, `runtime/`, …), not under `src/`. There is no `CONTEXT-MAP.md` and no per-package `docs/adr/`; every ADR lives in the one root `docs/adr/`.

If this repo ever splits into genuinely separate bounded contexts, promote it to multi-context by adding a root `CONTEXT-MAP.md` pointing at per-context `CONTEXT.md` files, and update this file.

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `CONTEXT.md`. Don't drift to synonyms the glossary explicitly avoids.

If the concept you need isn't in the glossary yet, that's a signal: either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/domain-modeling`).

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than silently overriding:

> _Contradicts the ADR on the per-step transactional store, but worth reopening because…_
