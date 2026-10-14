# Design

## Context

See proposal.md (Why) for the motivation and the re-measured premises. The state that shapes the approach:

- **The replacement exists.** `authz.Request`, `Decision`, `Decider`, `SpecReader`, `Composite` and `NewComposite` are on `main` (`authz/authz.go`, `authz/decider.go`, `authz/composite.go`).
- **casbin is reachable only through its own facade.** `go mod why` reaches govaluate and doublestar only via `casbinauthz` → `casbin/v2`. pgx and goose are also imported by `internal/persistence/store`, `internal/database` and `scheduler`, so they stay.
- **Three guards fail closed on the deletion:** `internal/atrest` (migration discovery counts), `atrest_crosscheck_test.go` (`_CasbinRule` legs), and the self-cleaning leak allowlist in `runtime/monitor/internal_leak_test.go`.

## Goals / Non-Goals

**Goals:**
- Remove casbin and its module graph in one commit that holds the go.mod slot.

**Non-Goals:**
- Any policy store, role hierarchy or wildcard expansion. #114 rejected them.
- Any change to `authz.Actor` or the deciders. `replace-casbin-with-composed-deciders` owns that, and lands after this change so it does not rewrite code this change deletes.

## Decisions

### D1. Remove casbin outright, with no replacement policy store (decided, kartaladev/wrkflw#114)
- **Rejected by the maintainer on #114:**
  - a separate policy store;
  - a definition-scoped role model. Both put identity or policy data in a library whose posture (#21) is that identity is the consumer's.
- **Rejected here:** keeping `casbinauthz` as an optional sub-module. It would still be a second authorization model to document and keep at parity. #114 is explicit that casbin goes.
- **Evidence:**
  - the import graph above;
  - 1,055 non-test lines;
  - nothing outside the facade, one example and one e2e test imports it (`grep -rl casbin --include=*.go`).

The port-first order recorded on #115 has already been met: `NewComposite` shipped in PR #174. So removing casbin now opens no window without a privilege-evaluating authorizer.

### D2. Removal mechanics
- **Delete the casbin code:** delete the packages and routes listed in the proposal's Impact.
- **Fix the guards that fail closed:**
  - `internal/atrest` stops listing `internal/authz/casbin/migrations`. The counts become `discover_test.go:191` 87 → 79, `schema_test.go:452` 29 → the measured keyed count without `casbin_rule`, and `discover_test.go:27-31` four → three directories.
  - The `_CasbinRule` cross-check legs and their exemption rows in `atrest_crosscheck_test.go` go away.
  - The `casbinauthz/casbinauthz.go:DBOption` entry in `runtime/monitor/internal_leak_test.go:272` is removed in the same commit, because the guard self-cleans at `:556-563`. That closes #179.
  - `engine/purity_test.go`'s `"casbin"` denylist entry stays, so the dependency cannot come back.
- **Update the module files:** `go mod tidy` in the root and `examples` modules. Verify with `go mod graph` that `casbin/v2`, `govaluate` and `doublestar/v4` are absent and that pgx and goose remain. While this runs no other change touches `go.mod`, `go.sum` or `go.work`.

Rejected alternative: deleting the routes in a later change than the packages. `PolicyAdmin`'s only implementation is casbin's, so the routes would have nothing to serve.

## Risks / Trade-offs

- **A deployment that ran `MigrateCasbin` keeps an orphan `casbin_rule` table.** → Mitigation: a release-note line. The library never created it implicitly.
- **A consumer calling `/admin/policies` gets 404.** → Mitigation: release note; the library stores no policy, so the consumer's identity provider holds roles and privileges.

## Migration Plan

For consumers (release notes, owned by `cut-v0-release`):
1. Replace `casbinauthz.NewCasbinAuthorizer(...)` with `authz.NewComposite()`. Flatten roles and privileges in `RequestActorFunc` or `ActorResolver`, then drop `casbin_rule` if it was created.
2. Remove any `AdminRoutes.Policies` wiring and any client of `/admin/policies` or `/admin/role-bindings`.

Rollback is a revert of the change. No schema migration ships.

## Cross-change coupling

| Change | Coupling | Owner and order |
|---|---|---|
| `replace-casbin-with-composed-deciders` (#115 rest, #119) | `authz` package doc, `doc.go`, `examples`; casbin reads `Actor.Roles`, which that change removes | This change lands first (it is in Blocks), so that change never migrates casbin code to the new actor |
| `split-persistence-modules` (#39) | Relocates `internal/atrest`, whose exemptions name `casbin_rule` | Land this change first, so the split moves less |
| `unify-http-routes-as-data` (#40) | `groups.go` in three adapters; the `Policies` blocks disappear here | Either order |
| `fix-doc-rot` | `doc.go` | Either order |
| `cut-v0-release` (#48) | Release-note lines from the Migration Plan | Blocked by this change |
