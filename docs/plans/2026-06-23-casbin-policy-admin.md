# casbin policy-admin — Implementation Plan

> Executed via superpowers:subagent-driven-development. Strict TDD. Mirrors the DLQ-admin pattern (ADR-0029). Non-engine.

**Goal:** Expose a casbin policy-admin (`service.PolicyAdmin`) via `WithPolicyAdmin` on REST + gRPC; `casbinauthz.PolicyAdminFor` adapts the shared enforcer. Engine/model untouched.

## Global Constraints
- Module `github.com/zakyalvan/krtlwrkflw`; no `pkg/` prefix.
- Strict TDD; RED before GREEN.
- Engine/model production diff ZERO. casbin confined to `casbinauthz`/`internal/authz/casbin` (existing confinement guard).
- `workflow-` error prefix; black-box tests; table-test assert-closure; `t.Context()`.
- Optional seam + default-deny pattern identical to DLQ admin (ADR-0029): REST routes only when wired (else 404), behind `cfg.adminMiddleware`; gRPC `codes.Unimplemented` when unwired; `WithPolicyAdmin(nil)` panics.
- Proto regen reproducible (`go generate ./transport/grpc/...`; toolchain installed earlier).
- Gate: `go test -race -p 1 ./...` green; ≥85% on casbinauthz, transport/rest, transport/grpc; lint clean.
- Spec: docs/specs/2026-06-23-casbin-policy-admin-design.md. ADR-0036.

## File Structure
- `service/policyadmin.go` (**create**) — `PolicyAdmin` interface + `PolicyRule`/`RoleBinding`.
- `casbinauthz/policyadmin.go` (**create**) — `PolicyAdminFor` + the enforcer adapter.
- `casbinauthz/policyadmin_test.go` (**create**).
- `transport/rest/options.go`, `admin.go`, `view.go`, `handler.go` (**modify**) — option + handlers + views + conditional routes.
- `transport/rest/policy_admin_test.go` (**create**).
- `transport/grpc/proto/workflow.proto` (**modify**) + regen; `options.go`, `server.go` (**modify**).
- `transport/grpc/policy_admin_test.go` (**create**).

---

### Task 1: service.PolicyAdmin seam + casbinauthz adapter

**Files:** create `service/policyadmin.go`, `casbinauthz/policyadmin.go`, `casbinauthz/policyadmin_test.go`.

**Context:** `casbinauthz.Authorizer` (casbinauthz/casbinauthz.go) is the concrete type returned (as `authz.Authorizer`) by `NewCasbinAuthorizer(e)` / `NewCasbinAuthorizerFromDB(...)`; it holds `enforcer *casbinv2.SyncedEnforcer` privately (only `ReloadPolicy` exposed). `*SyncedEnforcer` has `AddPolicy(params...)` `(bool,error)`, `RemovePolicy(...)`, `GetPolicy() [][]string`, `AddGroupingPolicy(...)`, `RemoveGroupingPolicy(...)`, `GetGroupingPolicy() [][]string`. Read casbinauthz.go first to confirm the field name + the casbin import alias.

**Produces:**
- `service.PolicyAdmin` interface + `service.PolicyRule{Subject,Object,Action}` + `service.RoleBinding{User,Role}` (per spec §2).
- `casbinauthz.PolicyAdminFor(a authz.Authorizer) (service.PolicyAdmin, bool)`.

**Steps (TDD):**
1. Write `casbinauthz/policyadmin_test.go` (black-box `casbinauthz_test`, in-memory enforcer via the existing `newEnforcer`/string-adapter helper pattern from authorizer_test.go): build a casbin authorizer (`casbinauthz.NewCasbinAuthorizer(e)`), `pa, ok := casbinauthz.PolicyAdminFor(auth)` → ok true; `AddPolicy(PolicyRule{"mgr","approve","*"})` → added true; second add → added false; `ListPolicies` contains it; `RemovePolicy` → removed true; same for `AddRole/ListRoles/RemoveRole` (grouping). `PolicyAdminFor(authz.AllowAll{})` (or any non-casbin authorizer) → ok=false. Run RED (undefined).
2. Implement `service/policyadmin.go` (interface + value types, godoc). Implement `casbinauthz/policyadmin.go`: `policyAdmin struct{ e *casbinv2.SyncedEnforcer }` mapping each method to the enforcer's add/remove/get (List* converts `[][]string` → `[]service.PolicyRule`/`[]service.RoleBinding`, indices [0]=sub/user [1]=obj/role [2]=act; guard slice length), errors wrapped `workflow-casbin: policy admin: %w`. `PolicyAdminFor` type-asserts `*Authorizer` and returns `&policyAdmin{e: a.enforcer}, true` else `nil, false`. Add a compile-time `var _ service.PolicyAdmin = (*policyAdmin)(nil)`. Run GREEN. Lint + confinement guard.
3. Commit `feat(service,casbinauthz): PolicyAdmin seam + casbin enforcer adapter`.

---

### Task 2: REST policy-admin

**Files:** modify `transport/rest/options.go`, `admin.go`, `view.go`, `handler.go`; create `transport/rest/policy_admin_test.go`.

Mirror the DLQ-admin REST work (ADR-0029) exactly: `config.policyAdmin service.PolicyAdmin`;
`WithPolicyAdmin(pa) Option` (nil-panic); conditional route registration in `NewHandler` behind
`cfg.adminMiddleware` when `cfg.policyAdmin != nil`:
`GET/POST/DELETE /admin/policies`, `GET/POST/DELETE /admin/role-bindings`. Handlers decode bodies
(`{subject,object,action}` / `{user,role}`) via `decodeBody`, call the admin, render
`{"policies":[…]}`/`{"role_bindings":[…]}`/`{"added":bool}`/`{"removed":bool}` via fixed view structs
in `view.go`. Errors via `WriteHTTPError`.

**Steps (TDD):** stub `service.PolicyAdmin`; tests: wired+admin-allow each verb → 200 + body; default-deny → 403; not wired → 404; bad body → 400; `WithPolicyAdmin(nil)` panics. RED→implement→GREEN. Commit `feat(transport/rest): casbin policy-admin endpoints`.

---

### Task 3: gRPC policy-admin

**Files:** modify `transport/grpc/proto/workflow.proto` (+ regen `workflowpb`), `options.go`, `server.go`; create `transport/grpc/policy_admin_test.go`.

Proto: add `AddPolicy`/`RemovePolicy`/`ListPolicies`/`AddRole`/`RemoveRole`/`ListRoles` RPCs +
`PolicyRule{subject,object,action}`/`RoleBinding{user,role}`/request/response messages. Regen via the
`//go:generate` directive (`cd transport/grpc && protoc … proto/workflow.proto`; toolchain installed).
`serverConfig.policyAdmin` + `WithPolicyAdmin(pa) Option` (nil-panic); `server.policyAdmin` threaded in
`RegisterWorkflowServiceServer`. Handlers return `codes.Unimplemented` when `s.policyAdmin == nil`,
else delegate (mirror `ListDeadLetters`/`RedriveDeadLetters`). Extend the SECURITY doc-comment.

**Steps (TDD):** stub admin via the bufconn `newStubHarnessWithOpts` pattern; tests: wired → results; not wired → `codes.Unimplemented`; `WithPolicyAdmin(nil)` panics. RED→implement→GREEN (regen first so the package compiles via embedded Unimplemented defaults). Commit `feat(transport/grpc): casbin policy-admin RPCs`.

---

### Task 4: docs + gate (controller)
ADR-0036 written. Controller verifies, updates HANDOVER + memory, full gate, whole-branch review, merge.

## Verification Checklist
- [ ] `go test -race -p 1 ./...` green; ≥85% casbinauthz/rest/grpc.
- [ ] `golangci-lint run ./...` clean; casbin confinement guard green.
- [ ] Engine/model production diff ZERO.
- [ ] Proto regen reproducible (re-run leaves workflowpb clean).
- [ ] Whole-branch review; merge + push; HANDOVER + memory.

## Spec coverage self-check
- §0 ctx non-issue → documented (no task). §2 seam → Task 1. §3 adapter → Task 1. §4 REST → Task 2, gRPC → Task 3. §5 tests → per-task. ✓
