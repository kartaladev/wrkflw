# 36. casbin policy-admin via an optional PolicyAdmin seam (REST + gRPC)

- Status: Accepted
- Date: 2026-06-23

## Context

casbin authorization policies (RBAC `p` rules + `g` role-assignments) can only be changed today by
editing the `casbin_rule` table directly or rebuilding the enforcer — there is no programmatic admin
surface through the public API. The casbin `*SyncedEnforcer` (held privately by
`casbinauthz.Authorizer`) supports thread-safe mutation/query, and with the DB adapter + watcher
(ADR-0023) a mutation auto-persists and notifies other replicas. The backlog lists "casbin
policy-admin REST/gRPC".

A related backlog item — "casbin adapter/watcher context propagation" — was investigated and found to
be a **non-issue**: casbin's upstream `persist.Adapter` interface methods take no `ctx`, so the
`pgAdapter`'s per-call `context.Background()` is the only option; the `pgWatcher.listen` loop already
threads its lifecycle ctx. No code change is warranted there.

## Decision

Expose a casbin policy-admin through an **optional `service.PolicyAdmin` seam** + `WithPolicyAdmin`
on both transports, mirroring the DLQ-admin optional-seam pattern (ADR-0029):

- `service.PolicyAdmin` (new): `AddPolicy`/`RemovePolicy`/`ListPolicies` (over `p` rules) and
  `AddRole`/`RemoveRole`/`ListRoles` (over `g` role-bindings), using value types `PolicyRule` and
  `RoleBinding`. Stdlib-only deps.
- `casbinauthz.PolicyAdminFor(authz.Authorizer) (service.PolicyAdmin, bool)` — non-breakingly
  type-asserts the concrete `*casbinauthz.Authorizer` and returns an admin over its **shared**
  enforcer (mutations take effect immediately and persist via the DB adapter); `ok=false` for a
  non-casbin authorizer. Existing constructors are unchanged.
- REST: `WithPolicyAdmin` + admin-gated (default-deny middleware) routes registered only when wired
  (`/admin/policies`, `/admin/role-bindings` — GET/POST/DELETE). gRPC: `WithPolicyAdmin` + RPCs that
  return `codes.Unimplemented` when unwired; consumer interceptor gates auth (like `ListInstances`/DLQ).

Engine/model untouched; casbin stays confined to `casbinauthz`/`internal/authz/casbin`.

## Consequences

**Positive**
- Runtime policy/role management through the public library API on both transports; reuses the proven
  optional-seam + default-deny pattern, so the surface is consistent and consumers opt in explicitly.
- Operates on the **shared** enforcer, so admin changes are immediately authoritative and persist +
  propagate (DB adapter + watcher) without a duplicate enforcer.
- Non-breaking: existing `casbinauthz` constructors and the `authz.Authorizer` interface unchanged;
  `PolicyAdminFor` is additive.

**Negative / trade-offs**
- Like `ListInstances`/DLQ on gRPC, the policy-admin RPCs have no built-in per-method authz — the
  consumer must supply an interceptor (documented). REST sits behind the default-deny admin middleware.
- `PolicyAdminFor` relies on the concrete `*casbinauthz.Authorizer` type; a consumer wrapping the
  authorizer in their own type would get `ok=false` (acceptable — they hold the enforcer and can wire
  their own admin).
- The `ctx` on the `PolicyAdmin` methods is accepted for the port contract but casbin's mutators are
  synchronous and ignore it (documented; same constraint as `Authorize`).

**Closed as non-issue**
- casbin adapter/watcher ctx-propagation — upstream interface constraint; watcher already correct.
