# 0108. Rename action.NewMapCatalog → action.NewCatalog

Status: **Accepted — 2026-07-08.**
Follows the hard-rename convention of [ADR-0107](0107-rename-deliver-to-apply-trigger.md).

## Context

The `action` package exposes exactly two `Catalog` implementations: `MapCatalog` (map-backed,
constructed by `NewMapCatalog`) and `Registry` (constructed by `NewRegistry`). The map-backed
catalog is the default, ergonomic implementation used throughout tests and examples, yet its
constructor carried the more verbose, impl-qualified name `NewMapCatalog` while the less-common
`Registry` already owned an unqualified-per-type constructor. Since there is no third catalog to
disambiguate against, the default catalog should own the unqualified `NewCatalog`.

## Decision

Hard-rename the constructor `NewMapCatalog` → `NewCatalog`, identical signature
`func NewCatalog(m map[string]Action) MapCatalog` and identical behavior. No deprecated alias
(consistent with `Run`→`Drive`, `Deliver`→`ApplyTrigger`); pre-1.0.

Deliberate non-changes for minimal churn and Go idiom ("return concrete types"):

- The **type** `MapCatalog` keeps its name — it accurately describes the map-backed implementation.
- `NewCatalog` keeps returning the concrete `MapCatalog`, not the `Catalog` interface.
- `Registry`, `NewRegistry`, `DefaultCatalog`, and the `Catalog` interface are unchanged.

`NewMapCatalog` is a unique token (no substring collisions, and `NewCatalog` did not pre-exist),
so the rename was a single safe whole-word replace across ~111 sites — no receiver-scoping was
needed, unlike ADR-0107's `processtest.Deliver` collision.

## Consequences

- **Positive.** The default catalog constructor reads as the unqualified `action.NewCatalog`,
  matching its status as the go-to implementation and pairing cleanly with `NewRegistry`.
- **Neutral.** Breaking change for consumers calling `action.NewMapCatalog(...)`; a trivial
  mechanical update, and pre-1.0.
- **No behavior change**, so no new tests; the existing suite is the regression net and stayed
  green before and after.
