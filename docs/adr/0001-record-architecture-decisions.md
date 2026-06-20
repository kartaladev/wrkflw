# 1. Record architecture decisions

- Status: Accepted
- Date: 2026-06-20

## Context

`wrkflw` is a library-first Go workflow engine with several load-bearing
architectural properties (token-based execution, transport-agnostic core,
outbox eventing, pluggable authz/compensation) and a locked tech stack whose
choices each carry trade-offs. We need a durable, reviewable record of *why*
each significant decision was made, so future contributors do not silently
re-litigate or erode them.

We will use Architecture Decision Records (ADRs), as described by Michael
Nygard, to capture these decisions. This document is both the first ADR and the
**format template** that every subsequent ADR follows.

## Decision

We record every architecturally significant decision as a numbered ADR under
`docs/adr/`, named `NNNN-<slug>.md` (zero-padded, monotonically increasing).

Each ADR uses the section structure of this file:

- **Title** — `# N. <short imperative phrase>`.
- **Status** — one of `Proposed`, `Accepted`, `Deprecated`, or
  `Superseded by [ADR-XXXX](XXXX-...md)`, plus the date.
- **Context** — the forces at play: what makes the decision necessary, the
  constraints, and the relevant facts. Neutral; no decision yet.
- **Decision** — the choice made, stated in active voice ("We will…").
- **Consequences** — what becomes easier and what becomes harder as a result,
  including follow-up obligations.

ADRs are immutable once `Accepted`. To change a decision, write a new ADR that
supersedes the old one and update the old one's status to point at it.

A decision is "architecturally significant" when it constrains future work
across packages: anything in the locked Tech Stack table, any new seam in the
Architecture section, or any change to those — see `CLAUDE.md`.

## Consequences

- The rationale behind each decision is preserved and reviewable in git history,
  decoupled from the code that implements it.
- Contributors have a consistent, low-friction template, lowering the bar to
  documenting decisions.
- ADRs add a small per-decision authoring cost; trivial or easily reversible
  choices should *not* become ADRs, to keep the log signal-dense.
- Superseding rather than editing preserves the historical reasoning, at the
  cost of requiring a reader to follow the supersede chain to find the current
  decision.
