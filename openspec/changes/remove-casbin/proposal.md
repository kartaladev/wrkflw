# Proposal

## Why

kartaladev/wrkflw#114 (decided) removes casbin: wrkflw stores no policy, and authorization is a composite of single-purpose deciders over a complete actor. The composite shipped (PR #174, `authz.NewComposite`, baselined in `human-task-authorization`), so nothing still needs casbin, yet it remains on the production path, in every consumer's module graph, and behind six policy-administration routes. This change deletes it before the first tag.

Re-measured on `main` (`9c6dd2c1`, go1.26.9):

| Premise | Re-measurement |
|---|---|
| #114: casbin is "2,414 non-test lines" and pins casbin, govaluate, doublestar, pgx and goose | **Stale count.** Non-test Go is 1,055 lines (`casbinauthz` plus `internal/authz/casbin`); about 2,459 lines including tests. `go mod graph` shows casbin v2.135.0 pulls doublestar, govaluate, golang/mock and uuid. `go mod why` reaches govaluate and doublestar only through casbin. pgx and goose stay, because `internal/persistence/store`, `internal/database` and `scheduler` import them. So three modules leave the graph: `casbin/v2`, `govaluate` and `doublestar/v4`, in the root and `examples` modules. |
| #115 item 8: `RoleAuthorizer` stays one release as a deprecated constructor | **Stale.** `authz.RoleAuthorizer` no longer exists. Only a comment in `transport/http/httpcore/errors.go:90` still names it. |
| #115 scope comment: `internal/atrest` `render.go`, `classification.go` and four counts go red | **Stale paths, counts hold.** The package is now `discover.go` and `schema.go`. The counts are `discover_test.go:191` (87 = 79 + 8 `casbin_rule`) and `schema_test.go:452` (29 keyed of 87). The cross-check legs are in `internal/persistence/store/atrest_crosscheck_test.go` (`…_CasbinRule`, `:81`, `:928`). |
| #179: `casbinauthz.DBOption` (`casbinauthz.go:241`) is carried by a self-cleaning leak-guard entry | **Holds.** The entry is at `runtime/monitor/internal_leak_test.go:272`. Self-cleaning is enforced at `:556-563`. |

## What Changes

Decided and applied, not reopened: kartaladev/wrkflw#114 (casbin removed; no policy store; no role model in the library).

- **BREAKING (go.mod, flagged): casbin is removed.** `casbinauthz`, `internal/authz/casbin` and its `casbin_rule` migration are deleted. So are `service.PolicyAdmin` with its `servicetest` mock, the `Policies` field on `AdminRoutes` in all three adapters, the six `/admin/policies` and `/admin/role-bindings` routes, and their `httpcore` endpoints and DTOs. `go mod tidy` takes `github.com/casbin/casbin/v2`, `github.com/casbin/govaluate` and `github.com/bmatcuk/doublestar/v4` out of the root and `examples` modules. `examples/scenarios/attribute_authz` is rewritten on `authz.NewComposite`.
- **kartaladev/wrkflw#179 is superseded.** Deleting `casbinauthz.DBOption` retires its leak-guard entry. The allowlist entry is removed in the same commit, because the guard fails on a stale entry.
- **Persisted format (flagged).** The library never created `casbin_rule` automatically: `MigrateCasbin` was an explicit call. No migration drops the table, so a deployment that created it drops it itself. `internal/atrest` and its cross-checks stop knowing the table.
- **Default security posture: unchanged.** No route that remains changes its gate, and no default authorizer changes. The six policy routes are gone rather than re-gated, so they answer 404.
- **Docs.** `doc.go`, the `authz` package doc, `runtime/shutdown.go`, `httpcore/errors.go`, `scripts/testdb.sh`, `docs/agents/eventually-waits.md` and `CONTRIBUTING.md` stop naming casbin, except the purity denylist. `cut-v0-release` collects the release-note lines.

Absorbed issues:
- kartaladev/wrkflw#115: the casbin-removal part (115b) only. Item 8 and the `internal/atrest` paths were found stale (table above). The rest of #115 (actor model, claimant-only tasks) is owned by `replace-casbin-with-composed-deciders`.
- kartaladev/wrkflw#179: superseded by the removal, per the maintainer's disposition. It is closed with this change.
- kartaladev/wrkflw#114: decision issue, already closed and cited as decided.

No open decision: every removal is decided by #114.

Out of scope:
- The single-role actor, `on_behalf`, `Actor.Validate()`, privilege stripping and claimant-only tasks with implicit claim: kartaladev/wrkflw#115 (rest) and #119, owned by `replace-casbin-with-composed-deciders`.
- Relocating `internal/atrest` in the module split: owned by `split-persistence-modules`.
- The route table as data: kartaladev/wrkflw#40, owned by `unify-http-routes-as-data`.
- Release notes: `cut-v0-release`.

**Coordination**
- Size: M
- Depends on: nothing
- Blocks: replace-casbin-with-composed-deciders, cut-v0-release, `split-persistence-modules`, `decide-module-denylist-policy`
- Readiness: agent

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `http-transport`:
  - ADDED: no policy administration routes are mounted.

## Impact

- **Deleted:**
  - `casbinauthz/`, `internal/authz/casbin/`, `service/policyadmin.go`, `service/servicetest/policyadmin_mock.go`, `runtime/casbin_privilege_e2e_test.go`.
  - The policy endpoints in `transport/http/httpcore/admin_endpoints.go` and `dto.go`, and the `Policies` blocks in `transport/http/{stdlib,gin,fiber}/groups.go`.
- **go.mod / go.sum:** root and `examples`. This change holds the go.mod slot, so no other change touching go.mod runs concurrently.
- **Guards and fixtures:** `internal/atrest` and `atrest_crosscheck_test.go` (shared with `split-persistence-modules`), `runtime/monitor/internal_leak_test.go`, `engine/purity_test.go` (denylist entry kept).
- **Docs and examples:** `doc.go` (shared with `fix-doc-rot`, `define-access-and-disclosure` and `replace-casbin-with-composed-deciders`), `runtime/shutdown.go`, `scripts/testdb.sh`, `docs/agents/eventually-waits.md:94`, `CONTRIBUTING.md:123`, `examples/scenarios/attribute_authz`.
