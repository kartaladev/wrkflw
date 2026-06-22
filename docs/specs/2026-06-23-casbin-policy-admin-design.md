# Design: casbin policy-admin (REST + gRPC) + casbin ctx-propagation finding

**Date:** 2026-06-23
**Status:** Approved (user-chosen high-value subset)
**Track:** Backlog (API completeness + Correctness). Follow-up to ADR-0010/0023.
**ADR:** 0036.

## 0. casbin context-propagation — investigated, **non-issue** (documented, no code change)

The backlog listed "casbin adapter/watcher `context` propagation". Investigation: casbin's
`persist.Adapter` interface methods (`LoadPolicy`/`SavePolicy`/`AddPolicy`/`RemovePolicy`/
`RemoveFilteredPolicy`) take **no `ctx` parameter** — fixed by the upstream library — so the
`internal/authz/casbin` `pgAdapter` *cannot* thread a caller ctx and correctly uses a per-call
`context.Background()`. The `pgWatcher.listen` loop already threads its lifecycle ctx
(`Acquire`/`Exec`/`WaitForNotification` all use it); `pgWatcher.Update` is a fire-and-forget NOTIFY
where `context.Background()` is appropriate. **No actionable change** — recorded here so the item is
closed rather than reopened. The `authz.Authorizer.Authorize` already takes `ctx` (casbin's
`Enforce` ignores it, also a library constraint).

## 1. Problem & scope (policy-admin)

casbin policies (RBAC `p` rules + `g` role-assignments) can only be changed today by editing the DB
table directly or rebuilding the enforcer — there is no programmatic admin surface. The casbin
`*SyncedEnforcer` (held privately by `casbinauthz.Authorizer`) supports thread-safe
`AddPolicy`/`RemovePolicy`/`GetPolicy`/`AddGroupingPolicy`/`RemoveGroupingPolicy`/`GetGroupingPolicy`,
and (with the DB adapter + watcher, ADR-0023) a mutation auto-persists and notifies other replicas.

This track exposes a **policy-admin** through the public API on **both transports**, mirroring the
DLQ-admin optional-seam pattern (ADR-0029) exactly.

**In scope:** a `service.PolicyAdmin` optional seam; a `casbinauthz` adapter over the enforcer;
`WithPolicyAdmin(...)` on REST + gRPC; admin-gated routes/RPCs; tests.

**Out of scope:** casbin ABAC-in-matchers, richer Privilege modeling, `FilteredAdapter`/`WatcherEx`
(separate backlog items). Per-method authz beyond the transport admin gate (same posture as DLQ).

## 2. The seam — `service.PolicyAdmin`

```go
// service/policyadmin.go
package service

import "context"

// PolicyRule is a casbin RBAC policy line: subject may perform action on object.
type PolicyRule struct{ Subject, Object, Action string }

// RoleBinding is a casbin grouping line: user has role.
type RoleBinding struct{ User, Role string }

// PolicyAdmin is the optional admin port for managing casbin authorization policies
// at runtime. It is intentionally separate from Service (authz is a cross-cutting
// concern, and a consumer using a non-casbin Authorizer simply never wires it).
type PolicyAdmin interface {
    AddPolicy(ctx context.Context, r PolicyRule) (added bool, err error)
    RemovePolicy(ctx context.Context, r PolicyRule) (removed bool, err error)
    ListPolicies(ctx context.Context) ([]PolicyRule, error)
    AddRole(ctx context.Context, b RoleBinding) (added bool, err error)
    RemoveRole(ctx context.Context, b RoleBinding) (removed bool, err error)
    ListRoles(ctx context.Context) ([]RoleBinding, error)
}
```
References only stdlib `context` (+ the two value types) — no new `service` deps.

## 3. casbin adapter (`casbinauthz`)

The enforcer is shared with the live `Authorizer` so admin mutations take effect immediately (no
notify round-trip) and persist via the DB adapter. Expose it **non-breakingly**:

- Add `func PolicyAdminFor(a authz.Authorizer) (service.PolicyAdmin, bool)` — type-asserts the
  concrete `*casbinauthz.Authorizer` (returned, wrapped, by the existing constructors) and returns a
  policy-admin over its enforcer; `ok=false` for a non-casbin authorizer.
- The adapter (`type policyAdmin struct{ e *casbinv2.SyncedEnforcer }`) maps:
  `AddPolicy→e.AddPolicy(sub,obj,act)`, `RemovePolicy→e.RemovePolicy(...)`,
  `ListPolicies→e.GetPolicy()`, `AddRole→e.AddGroupingPolicy(user,role)`,
  `RemoveRole→e.RemoveGroupingPolicy(...)`, `ListRoles→e.GetGroupingPolicy()`. The `bool` return is
  casbin's "was the rule actually added/removed" (false = already present/absent). Errors wrapped
  `workflow-casbin:`. `ctx` is accepted for the port contract; casbin's mutators are synchronous (no
  ctx) — documented.
- Existing `casbinauthz` constructors are unchanged (no signature break).

## 4. Transports (mirror the DLQ-admin pattern, ADR-0029)

### REST (`transport/rest`)
- `config.policyAdmin service.PolicyAdmin`; `WithPolicyAdmin(pa) Option` (nil-panic).
- Routes registered **only when wired**, behind `cfg.adminMiddleware` (default-deny):
  - `GET  /admin/policies`            → list `p` rules → `{"policies":[{subject,object,action}]}`
  - `POST /admin/policies`            → body `{subject,object,action}` → add → `{"added":bool}`
  - `DELETE /admin/policies`          → body `{subject,object,action}` → remove → `{"removed":bool}`
  - `GET  /admin/role-bindings`       → list `g` rules → `{"role_bindings":[{user,role}]}`
  - `POST /admin/role-bindings`       → body `{user,role}` → add → `{"added":bool}`
  - `DELETE /admin/role-bindings`     → body `{user,role}` → remove → `{"removed":bool}`
- Reuse `decodeBody`/`WriteHTTPError`/`writeJSON`; fixed view structs in `view.go`.

### gRPC (`transport/grpc`)
- `serverConfig.policyAdmin`; `WithPolicyAdmin(pa) Option` (nil-panic); `server.policyAdmin`.
- Proto: `AddPolicy`/`RemovePolicy`/`ListPolicies`/`AddRole`/`RemoveRole`/`ListRoles` RPCs +
  `PolicyRule`/`RoleBinding`/request/response messages. Regen via `go generate`.
- Handlers return `codes.Unimplemented` when `policyAdmin == nil`; else delegate. SECURITY doc
  extended (admin-scoped, consumer interceptor responsibility — like `ListInstances`/DLQ).

## 5. Testing strategy

- **casbinauthz (`casbinauthz_test`):** in-memory `SyncedEnforcer` (string adapter, mirror
  `authorizer_test.go`): `PolicyAdminFor` returns a working admin; Add/Remove/List p-rules and
  g-bindings round-trip; `AddPolicy` twice → second returns `added=false`; `PolicyAdminFor` on a
  non-casbin authorizer → `ok=false`. No DB needed (enforcer-level).
- **transport/rest (`rest_test`):** stub `service.PolicyAdmin`; wired+admin-allow → 200 + bodies;
  default-deny (no admin mw) → 403; not wired → 404; bad body → 400; `WithPolicyAdmin(nil)` panics.
- **transport/grpc (`grpctransport_test`):** stub admin; wired → results; not wired →
  `codes.Unimplemented`; `WithPolicyAdmin(nil)` panics.

**Gate:** `go test -race -p 1 ./...` green; ≥85% on casbinauthz, transport/rest, transport/grpc;
`golangci-lint` clean; engine/model diff ZERO (non-engine track); casbin confined to
`casbinauthz`/`internal/authz/casbin` (existing confinement guard); proto regen reproducible.

## 6. ADR

| ADR | Decision |
|---|---|
| **0036** | Expose a casbin **policy-admin** via an optional `service.PolicyAdmin` seam + `WithPolicyAdmin` on REST + gRPC (mirroring the DLQ-admin pattern, ADR-0029): list/add/remove `p` rules and `g` role-bindings over the shared `*SyncedEnforcer` (obtained non-breakingly via `casbinauthz.PolicyAdminFor`). Admin-gated (REST default-deny middleware; gRPC consumer interceptor + `Unimplemented` when unwired). The casbin adapter/watcher **ctx-propagation** backlog item is closed as a non-issue (upstream `persist.Adapter` interface has no ctx; watcher already threads its lifecycle ctx). Non-engine; engine/model untouched. |
