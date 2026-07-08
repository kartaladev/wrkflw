# Rename `action.NewMapCatalog` → `action.NewCatalog`

Date: 2026-07-08
Status: Approved (design) — pending implementation plan
Scope: single public-constructor rename, behavior-preserving

## Context

`action.NewMapCatalog(m map[string]Action) MapCatalog` (`action/catalog.go:22`) constructs the
map-backed `Catalog`. The `action` package has exactly **two** `Catalog` implementations:
`MapCatalog` (this one) and `Registry` (which already owns the unqualified constructor
`NewRegistry`). Since the map-backed catalog is the default, ergonomic implementation, its
constructor should own the unqualified name `NewCatalog`.

## Decision

Hard-rename the constructor `NewMapCatalog` → `NewCatalog`, identical signature and behavior. No
deprecated alias (consistent with the repo's prior hard renames, e.g. `Run`→`Drive`,
`Deliver`→`ApplyTrigger` / ADR-0107). Add ADR-0108.

Deliberate non-changes (minimal churn, idiomatic Go — "return concrete types"):
- The **type** `MapCatalog` keeps its name (it accurately names the map-backed impl).
- `NewCatalog` keeps returning the concrete `MapCatalog` (not the `Catalog` interface).
- `Registry`/`NewRegistry`, `DefaultCatalog`, and the `Catalog` interface are untouched.

## Scope

- Constructor definition: `action/catalog.go:22`.
- All ~111 call sites of `NewMapCatalog(...)` across runtime, engine, service, transport, examples,
  processtest, and tests.
- Godoc/prose mentions of `NewMapCatalog` (e.g. `action/doc.go`).
- New `docs/adr/0108-rename-newmapcatalog-to-newcatalog.md`.

## Non-goals

- No signature/behavior change. The `MapCatalog` **type** name stays. `Registry`, `NewRegistry`,
  `DefaultCatalog`, and `Catalog` stay.
- Historical ADRs/plans markdown left unchanged; ADR-0108 supersedes the naming.

## Execution approach

`NewMapCatalog` is a **unique token** with no substring collisions (verified: the only match is
`NewMapCatalog` itself; `action.NewCatalog` does not already exist). So a single safe global
replace suffices — no receiver-scoping needed (unlike the `Deliver`/`processtest.Deliver`
collision in ADR-0107):

```
find . -name '*.go' -print0 | xargs -0 perl -pi -e 's/\bNewMapCatalog\b/NewCatalog/g'
```

The `\b` word boundaries keep the type `MapCatalog` (no `New` prefix) untouched.

## Verification checklist

- [ ] `go build ./...` clean.
- [ ] `go vet ./...` clean.
- [ ] `go test -race ./...` — 0 failures, 0 races (existing suite is the safety net; behavior-
      preserving refactor → no new tests).
- [ ] `golangci-lint run ./...` — 0 issues.
- [ ] Runnable examples still execute.
- [ ] Grep audit: zero `NewMapCatalog` remain; `action.NewCatalog` exists; `MapCatalog` type intact.

## Risks

- Minimal. Unique token, no collision, no interface change. Only risk is a missed file — mitigated
  by the `find` over all `*.go` plus the post-replace grep audit.
